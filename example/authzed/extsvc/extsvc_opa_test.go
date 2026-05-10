package extsvc_test

import (
	"context"
	"testing"

	"github.com/open-policy-agent/opa/v1/rego"

	extsvc "github.com/danhtran94/authzed-codegen/example/authzed/extsvc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOPA_CheckFolderBrowse_NoCaveat exercises the OPA binding's no-caveat
// dispatch path against a live SpiceDB testcontainer. Asserts the OPA
// builtin's bool return matches the typed CheckBrowse result.
//
// Per scope SC9 — covers the no-caveat call site `extsvc.check_folder_browse(uid, rid, {})`.
func TestOPA_CheckFolderBrowse_NoCaveat(t *testing.T) {
	ctx := context.Background()

	folder := extsvc.Folder("opa-fb-1")
	user := extsvc.User("opa-u-1")
	require.NoError(t, folder.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{user},
	}))

	opts := []func(*rego.Rego){
		rego.Query("data.test.allow"),
		rego.Module("test.rego", `
package test
import future.keywords.if

allow if extsvc.check_folder_browse("extsvc/user:opa-u-1", "opa-fb-1", {})
`),
	}
	opts = append(opts, extsvc.SpiceDBBuiltins(sb.Engine, ctx)...)
	r := rego.New(opts...)

	rs, err := r.Eval(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, rs)
	assert.Equal(t, true, rs[0].Expressions[0].Value)
}

// TestOPA_CheckFolderTenantedBrowse_WithCaveat exercises the with-caveat
// dispatch path. The tuple is written without pre-context; the OPA binding
// supplies the caveat context map at eval time.
//
// Per scope SC9 — covers the with-caveat call site by passing a populated
// caveat_context object that exercises a string caveat parameter.
func TestOPA_CheckFolderTenantedBrowse_WithCaveat_Match(t *testing.T) {
	ctx := context.Background()

	folder := extsvc.Folder("opa-tb-match")
	user := extsvc.User("opa-u-tb-match")
	require.NoError(t, folder.CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{user},
	}))

	opts := []func(*rego.Rego){
		rego.Query("data.test.allow"),
		rego.Module("test.rego", `
package test
import future.keywords.if

allow if extsvc.check_folder_tenanted_browse("extsvc/user:opa-u-tb-match", "opa-tb-match", {"tenant": "acme"})
`),
	}
	opts = append(opts, extsvc.SpiceDBBuiltins(sb.Engine, ctx)...)
	r := rego.New(opts...)

	rs, err := r.Eval(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, rs)
	assert.Equal(t, true, rs[0].Expressions[0].Value, "tenant=acme matches the from_subnet caveat")
}

// TestOPA_CheckFolderTenantedBrowse_WrongTenant verifies the with-caveat
// path returns false when the caveat evaluates false. The runtime ctx
// supplies a non-matching tenant value; SpiceDB returns ErrPermissionDenied
// which the binding maps to BooleanTerm(false) per SPEC C4.
func TestOPA_CheckFolderTenantedBrowse_WithCaveat_Mismatch(t *testing.T) {
	ctx := context.Background()

	folder := extsvc.Folder("opa-tb-wrong")
	user := extsvc.User("opa-u-tb-wrong")
	require.NoError(t, folder.CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{user},
	}))

	opts := []func(*rego.Rego){
		rego.Query("data.test.allow"),
		rego.Module("test.rego", `
package test
import future.keywords.if

default allow := false
allow if extsvc.check_folder_tenanted_browse("extsvc/user:opa-u-tb-wrong", "opa-tb-wrong", {"tenant": "not-acme"})
`),
	}
	opts = append(opts, extsvc.SpiceDBBuiltins(sb.Engine, ctx)...)
	r := rego.New(opts...)

	rs, err := r.Eval(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, rs)
	assert.Equal(t, false, rs[0].Expressions[0].Value, "tenant=not-acme rejected by caveat → policy denies")
}

// TestOPA_LookupFolderBrowseResources_NoCaveat exercises the Lookup binding's
// Definite extraction path. Two folders granted to the user via the viewer
// relation; the Lookup builtin returns both folder IDs as a Rego []string.
func TestOPA_LookupFolderBrowseResources_NoCaveat(t *testing.T) {
	ctx := context.Background()

	user := extsvc.User("opa-u-lk-1")
	require.NoError(t, extsvc.Folder("opa-lk-fa").CreateViewerRelations(ctx, extsvc.FolderViewerObjects{User: []extsvc.User{user}}))
	require.NoError(t, extsvc.Folder("opa-lk-fb").CreateViewerRelations(ctx, extsvc.FolderViewerObjects{User: []extsvc.User{user}}))

	opts := []func(*rego.Rego){
		rego.Query("data.test.resources"),
		rego.Module("test.rego", `
package test

resources := extsvc.lookup_folder_browse_resources("extsvc/user:opa-u-lk-1", {})
`),
	}
	opts = append(opts, extsvc.SpiceDBBuiltins(sb.Engine, ctx)...)
	r := rego.New(opts...)

	rs, err := r.Eval(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, rs)
	got, ok := rs[0].Expressions[0].Value.([]any)
	require.True(t, ok, "result should be []any list")
	assert.Contains(t, got, "opa-lk-fa")
	assert.Contains(t, got, "opa-lk-fb")
}
