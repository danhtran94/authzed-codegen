package authzed_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	authzed "github.com/danhtran94/authzed-codegen/example/authzed"
	"github.com/danhtran94/authzed-codegen/pkg/authz"
	"github.com/danhtran94/authzed-codegen/pkg/authz/spicedbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const baselineSchemaPath = "../schema.zed"

func loadBaseline(t *testing.T) string {
	t.Helper()
	bytes, err := os.ReadFile(baselineSchemaPath)
	require.NoError(t, err, "read baseline schema")
	return string(bytes)
}

// startSandboxOrSkip boots a fresh SpiceDB sandbox with the given schema.
// Skips the test cleanly when Docker isn't available (matches the
// extsvc_test pattern).
func startSandboxOrSkip(t *testing.T, schema string) *spicedbtest.Sandbox {
	t.Helper()
	ctx := context.Background()
	sb, err := spicedbtest.Start(ctx, schema)
	if err != nil {
		if errors.Is(err, spicedbtest.ErrDockerUnavailable) {
			t.Skip("Docker not available — skipping schema drift tests")
		}
		t.Fatalf("start spicedb sandbox: %v", err)
	}
	t.Cleanup(func() { _ = sb.Close(ctx) })
	authz.SetDefaultEngine(sb.Engine)
	return sb
}

// AUZ-015 — schema drift detection.
//
// Each test boots a fresh sandbox with a specific deployed schema variant.
// VerifySchema compares the codegen baseline (authzed.SchemaText) against
// the deployed schema and partitions the result into Added / Removed /
// Changed / Cosmetic buckets.

func TestVerifySchema_Clean_NoDrift(t *testing.T) {
	ctx := context.Background()
	startSandboxOrSkip(t, loadBaseline(t))

	drift, err := authzed.VerifySchema(ctx)
	require.NoError(t, err)
	assert.True(t, drift.IsClean(), "deployed schema matches codegen baseline → no drift")
	assert.False(t, drift.IsBreaking())
	assert.Empty(t, drift.Added)
	assert.Empty(t, drift.Removed)
	assert.Empty(t, drift.Changed)
	assert.Empty(t, drift.Cosmetic)
}

func TestVerifySchema_AdditiveDrift_DeployedHasExtraDefinition(t *testing.T) {
	ctx := context.Background()

	// Deploy baseline + one extra definition. Codegen baseline doesn't
	// know about it; expect 1 Added entry.
	deployed := loadBaseline(t) + "\ndefinition extsvc/extra_marker {}\n"
	startSandboxOrSkip(t, deployed)

	drift, err := authzed.VerifySchema(ctx)
	require.NoError(t, err)
	assert.False(t, drift.IsBreaking(), "additive drift is not breaking")
	assert.False(t, drift.IsClean(), "additive drift is still drift")
	require.Len(t, drift.Added, 1, "exactly one extra definition")

	added := drift.Added[0]
	// SpiceDB returns *_Removed for "deployed has extra" — that's our Added bucket.
	require.IsType(t, &v1.ReflectionSchemaDiff_DefinitionRemoved{}, added.Raw.GetDiff())
	assert.Contains(t, added.Description, "extsvc/extra_marker")
}

func TestVerifySchema_BreakingDrift_DeployedMissingDefinition(t *testing.T) {
	ctx := context.Background()

	// Remove a definition the baseline expects (extsvc/article — no
	// other definition references it, so the schema is still valid).
	deployed := stripDefinition(loadBaseline(t), "extsvc/article")
	startSandboxOrSkip(t, deployed)

	drift, err := authzed.VerifySchema(ctx)
	require.NoError(t, err)
	assert.True(t, drift.IsBreaking(), "missing definition is breaking drift")
	require.NotEmpty(t, drift.Removed, "extsvc/article removal lands in Removed")

	// At least one entry references extsvc/article (the definition
	// itself, plus its relations and permissions).
	found := false
	for _, e := range drift.Removed {
		if strings.Contains(e.Description, "extsvc/article") {
			found = true
			break
		}
	}
	assert.True(t, found, "Removed bucket must reference extsvc/article")
}

func TestVerifySchema_CosmeticDrift_DocCommentDifferenceOnly(t *testing.T) {
	ctx := context.Background()

	// Add a doc comment to an existing definition that doesn't have one.
	// Baseline has `definition extsvc/group {}` with no doc comment.
	// Replace with a documented version to surface a Cosmetic diff.
	deployed := strings.Replace(
		loadBaseline(t),
		"definition extsvc/group {}",
		"// extsvc/group — added a doc comment in the deployed variant.\ndefinition extsvc/group {}",
		1,
	)
	require.NotEqual(t, loadBaseline(t), deployed, "stub: baseline must contain the target string")
	startSandboxOrSkip(t, deployed)

	drift, err := authzed.VerifySchema(ctx)
	require.NoError(t, err)
	assert.False(t, drift.IsBreaking(), "doc comment change is not breaking")
	assert.False(t, drift.IsClean(), "doc comment change is still drift")
	require.NotEmpty(t, drift.Cosmetic, "doc comment changes land in Cosmetic")

	// At least one Cosmetic entry references the changed definition.
	found := false
	for _, e := range drift.Cosmetic {
		if strings.Contains(e.Description, "extsvc/group") {
			found = true
			break
		}
	}
	assert.True(t, found, "Cosmetic bucket must reference extsvc/group")
}

// stripDefinition returns the schema with the named definition block
// removed. Naive line-based parser sufficient for test fixtures —
// finds `definition <name> {`, walks to matching `}`, drops the range.
// Returns the original string unchanged if the definition isn't found
// (the test's NotEmpty assertion catches that).
func stripDefinition(schema, name string) string {
	header := fmt.Sprintf("definition %s {", name)
	startIdx := strings.Index(schema, header)
	if startIdx == -1 {
		return schema
	}
	depth := 0
	for i := startIdx; i < len(schema); i++ {
		switch schema[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				endIdx := i + 1
				if endIdx < len(schema) && schema[endIdx] == '\n' {
					endIdx++
				}
				return schema[:startIdx] + schema[endIdx:]
			}
		}
	}
	return schema
}
