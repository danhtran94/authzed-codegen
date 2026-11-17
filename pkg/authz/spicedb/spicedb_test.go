package spicedb_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/danhtran94/authzed-codegen/v2/pkg/authz"
	"github.com/danhtran94/authzed-codegen/v2/pkg/authz/spicedbtest"
	"github.com/stretchr/testify/assert"
)

const schemaPath = "../../../example/schema.zed"

func TestMain(m *testing.M) {
	ctx := context.Background()

	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "read schema (%s): %v\n", schemaPath, err)
		os.Exit(1)
	}

	sb, err := spicedbtest.Start(ctx, string(schema))
	if err != nil {
		if errors.Is(err, spicedbtest.ErrDockerUnavailable) {
			fmt.Println("SKIP: Docker not available — skipping SpiceDB tests")
			os.Exit(0)
		}
		_, _ = fmt.Fprintf(os.Stderr, "start spicedb sandbox: %v\n", err)
		os.Exit(1)
	}
	authz.SetDefaultEngine(sb.Engine)

	code := m.Run()
	_ = sb.Close(ctx)
	os.Exit(code)
}

// AUZ-026 — an empty subject ID list is a caller error, never a grant.
// Before AUZ-026 each Check* looped over zero IDs and returned nil.
func TestEngine_Check_EmptyIDs_ReturnsErrNoInput(t *testing.T) {
	ctx := context.Background()
	eng := authz.GetEngine(ctx)
	folder := authz.Resource{Type: authz.Type("extsvc/folder"), ID: authz.ID("empty-ids-f1")}

	for _, ids := range [][]authz.ID{nil, {}} {
		assert.ErrorIs(t, eng.CheckPermission(ctx, folder, "browse", "extsvc/user", ids), authz.ErrNoInput)
		assert.ErrorIs(t, eng.CheckPermissionWithCaveat(ctx, folder, "tenanted_browse", "extsvc/user", ids, map[string]any{"tenant": "acme"}), authz.ErrNoInput)
		assert.ErrorIs(t, eng.CheckPermissionUserset(ctx, folder, "collab_view", "extsvc/team", ids, "admin", nil), authz.ErrNoInput)
	}
}
