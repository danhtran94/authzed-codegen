package extsvc_test

import (
	"context"
	"sync"
	"testing"

	"github.com/open-policy-agent/opa/v1/rego"

	extsvc "github.com/danhtran94/authzed-codegen/v2/example/authzed/extsvc"
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

allow if extsvc.check_folder_browse({"extsvc/user": "opa-u-1"}, "opa-fb-1", {})
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

allow if extsvc.check_folder_tenanted_browse({"extsvc/user": "opa-u-tb-match"}, "opa-tb-match", {"tenant": "acme"})
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
allow if extsvc.check_folder_tenanted_browse({"extsvc/user": "opa-u-tb-wrong"}, "opa-tb-wrong", {"tenant": "not-acme"})
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

resources := extsvc.lookup_folder_browse_resources({"extsvc/user": "opa-u-lk-1"}, {})
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

// TestOPA_CheckFolderBrowse_MultiSubject exercises the keyed-object subject
// argument (AUZ-022): multiple subject-type keys (AND semantics, matching
// the typed Check<X> method) and the list-of-ids value form. folder.viewer
// accepts extsvc/user | extsvc/group | extsvc/role as direct subjects, and
// folder.browse = viewer.
func TestOPA_CheckFolderBrowse_MultiSubject(t *testing.T) {
	ctx := context.Background()

	folder := extsvc.Folder("opa-ms-1")
	require.NoError(t, folder.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User:  []extsvc.User{"ms-u"},
		Group: []extsvc.Group{"ms-g"},
	}))

	eval := func(t *testing.T, query, module string) any {
		t.Helper()
		opts := []func(*rego.Rego){rego.Query(query), rego.Module("test.rego", module)}
		opts = append(opts, extsvc.SpiceDBBuiltins(sb.Engine, ctx)...)
		rs, err := rego.New(opts...).Eval(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, rs)
		return rs[0].Expressions[0].Value
	}

	// Both keys granted → AND → true.
	assert.Equal(t, true, eval(t, "data.t.x", `
package t
import future.keywords.if
x if extsvc.check_folder_browse({"extsvc/user": "ms-u", "extsvc/group": "ms-g"}, "opa-ms-1", {})
`), "user AND group both granted")

	// One key not granted → AND → false.
	assert.Equal(t, false, eval(t, "data.t.x", `
package t
import future.keywords.if
default x := false
x if extsvc.check_folder_browse({"extsvc/user": "ms-u", "extsvc/group": "ms-g-missing"}, "opa-ms-1", {})
`), "group key denies → AND fails")

	// List-of-ids value form resolves like a single id.
	assert.Equal(t, true, eval(t, "data.t.x", `
package t
import future.keywords.if
x if extsvc.check_folder_browse({"extsvc/user": ["ms-u"]}, "opa-ms-1", {})
`), "list-of-ids subject value")
}

// TestOPA_GlobalRegistration exercises the AUZ-021 global-registration
// variant: extsvc.RegisterSpiceDBBuiltinsGlobal registers the SpiceDB
// builtins into OPA's process-global registry (ast.Builtins + topdown
// map). A fresh rego.New built with NO per-instance SpiceDBBuiltins
// options must still resolve extsvc.check_folder_browse — proving the
// global path is what runtime.NewRuntime would pick up.
//
// Note: RegisterSpiceDBBuiltinsGlobal mutates process-global state
// (ast.RegisterBuiltin appends to ast.Builtins), so every global test goes
// through registerGlobalBuiltins, which registers exactly once per process.
func TestOPA_GlobalRegistration(t *testing.T) {
	ctx := context.Background()

	folder := extsvc.Folder("opa-glob-1")
	user := extsvc.User("opa-u-glob-1")
	require.NoError(t, folder.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{user},
	}))

	// Register globally — this is what runtime.NewRuntime needs.
	registerGlobalBuiltins()

	// Build a fresh rego instance with NO per-instance SpiceDBBuiltins
	// options. If global registration worked, the builtin resolves.
	r := rego.New(
		rego.Query("data.test.allow"),
		rego.Module("test.rego", `
package test
import future.keywords.if

allow if extsvc.check_folder_browse({"extsvc/user": "opa-u-glob-1"}, "opa-glob-1", {})
`),
	)
	rs, err := r.Eval(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, rs)
	assert.Equal(t, true, rs[0].Expressions[0].Value, "globally-registered builtin resolves without per-instance options")
}

// AUZ-025 — the caveated branch of the nested union through the OPA
// builtin. Before AUZ-025 the builtin dropped tenant_match from the
// caveat context it forwards, so this grant was unreachable.
func TestOPA_CheckFolderNestedAccess_CaveatedBranch(t *testing.T) {
	ctx := context.Background()

	folder := extsvc.Folder("opa-nested-cav")
	require.NoError(t, folder.CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"opa-u-nested-cav"},
	}))

	opts := []func(*rego.Rego){
		rego.Query("data.test.allow"),
		rego.Module("test.rego", `
package test
import future.keywords.if

allow if extsvc.check_folder_nested_access({"extsvc/user": "opa-u-nested-cav"}, "opa-nested-cav", {"tenant": "acme"})
`),
	}
	opts = append(opts, extsvc.SpiceDBBuiltins(sb.Engine, ctx)...)
	r := rego.New(opts...)

	rs, err := r.Eval(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, rs)
	assert.Equal(t, true, rs[0].Expressions[0].Value, "tenant=acme satisfies the tenanted_viewer branch")
}

// evalOPA evaluates query against a one-rule policy using the per-instance
// builtins and returns the first result (nil when undefined).
func evalOPA(t *testing.T, ctx context.Context, query, rule string) any {
	t.Helper()
	opts := []func(*rego.Rego){
		rego.Query(query),
		rego.Module("test.rego", "package test\nimport future.keywords.if\n\n"+rule+"\n"),
	}
	opts = append(opts, extsvc.SpiceDBBuiltins(sb.Engine, ctx)...)
	rs, err := rego.New(opts...).Eval(ctx)
	require.NoError(t, err)
	if len(rs) == 0 {
		return nil
	}
	return rs[0].Expressions[0].Value
}

// AUZ-026 — an empty subject ID list must not grant. Before AUZ-026 the
// entry with zero IDs slipped past the empty-entries guard, the engine
// looped over nothing and returned nil, and the builtin returned true on
// a folder with no tuples at all.
func TestOPA_CheckFolderBrowse_EmptySubjectIDs_Denies(t *testing.T) {
	ctx := context.Background()

	got := evalOPA(t, ctx, "data.test.allow",
		`allow := extsvc.check_folder_browse({"extsvc/user": []}, "opa-empty-ids-no-tuples", {})`)
	assert.Equal(t, false, got, "empty subject ID list must deny")
}

// AUZ-026 — an empty key is treated as absent, matching the typed
// CheckBrowse (which skips empty slices). The user key alone decides.
func TestOPA_CheckFolderBrowse_EmptyKeyIgnored(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("opa-empty-key-chk").CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"opa-u-empty-key-chk"},
	}))

	got := evalOPA(t, ctx, "data.test.allow",
		`allow := extsvc.check_folder_browse({"extsvc/group": [], "extsvc/user": ["opa-u-empty-key-chk"]}, "opa-empty-key-chk", {})`)
	assert.Equal(t, true, got)
}

// AUZ-026 — lookup uses the first present key in sorted order. Before
// AUZ-026 the empty "extsvc/group" key sorted first and won, returning [].
func TestOPA_LookupFolderBrowse_EmptyKeyIgnored(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("opa-empty-key-lk").CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"opa-u-empty-key-lk"},
	}))

	got := evalOPA(t, ctx, "data.test.resources",
		`resources := extsvc.lookup_folder_browse_resources({"extsvc/group": [], "extsvc/user": ["opa-u-empty-key-lk"]}, {})`)
	assert.Equal(t, []any{"opa-empty-key-lk"}, got)
}

var globalBuiltinsOnce sync.Once

// registerGlobalBuiltins registers the SpiceDB builtins in OPA's
// process-global registry exactly once. RegisterSpiceDBBuiltinsGlobal
// appends to ast.Builtins, so a second call would duplicate entries.
func registerGlobalBuiltins() {
	globalBuiltinsOnce.Do(func() {
		extsvc.RegisterSpiceDBBuiltinsGlobal(sb.Engine, context.Background())
	})
}

// evalGlobalOPA is evalOPA without per-instance options: the builtin must
// resolve from the global registry.
func evalGlobalOPA(t *testing.T, ctx context.Context, query, rule string) any {
	t.Helper()
	registerGlobalBuiltins()
	rs, err := rego.New(
		rego.Query(query),
		rego.Module("test.rego", "package test\nimport future.keywords.if\n\n"+rule+"\n"),
	).Eval(ctx)
	require.NoError(t, err)
	if len(rs) == 0 {
		return nil
	}
	return rs[0].Expressions[0].Value
}

// AUZ-026 via the GLOBAL registration. The per-instance builtins are
// covered by TestOPA_CheckFolderBrowse_EmptySubjectIDs_Denies and
// TestOPA_LookupFolderBrowse_EmptyKeyIgnored; both registrations render
// the same template, and this pins the global one at runtime.
func TestOPA_Global_EmptySubjectIDs_Denies(t *testing.T) {
	ctx := context.Background()

	got := evalGlobalOPA(t, ctx, "data.test.allow",
		`allow := extsvc.check_folder_browse({"extsvc/user": []}, "opa-glob-empty-no-tuples", {})`)
	assert.Equal(t, false, got, "empty subject ID list must deny through the global builtin")
}

func TestOPA_Global_LookupEmptyKeyIgnored(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("opa-glob-empty-key-lk").CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"opa-u-glob-empty-key-lk"},
	}))

	got := evalGlobalOPA(t, ctx, "data.test.resources",
		`resources := extsvc.lookup_folder_browse_resources({"extsvc/group": [], "extsvc/user": ["opa-u-glob-empty-key-lk"]}, {})`)
	assert.Equal(t, []any{"opa-glob-empty-key-lk"}, got)
}

// AUZ-028 — through the OPA builtins, the fields documented "is never
// granted" on CheckArticleAuthorOnlyInputs / CheckArticleEditorInputs are
// denied, and User behaves per the operator: an author who can also
// browse the parent is excluded from author_only but satisfies editor.
func TestOPA_Article_NeverGrantedSubjectsAndOperators(t *testing.T) {
	ctx := context.Background()

	folder := extsvc.Folder("opa-ng-folder")
	article := extsvc.Article("opa-ng-article")
	require.NoError(t, folder.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User:  []extsvc.User{"opa-ng-browser"},
		Group: []extsvc.Group{"opa-ng-g"},
		Role:  []extsvc.Role{"opa-ng-r"},
	}))
	require.NoError(t, article.CreateParentRelations(ctx, extsvc.ArticleParentObjects{Folder: []extsvc.Folder{folder}}))
	require.NoError(t, article.CreateAuthorRelations(ctx, extsvc.ArticleAuthorObjects{
		User: []extsvc.User{"opa-ng-browser", "opa-ng-plain"},
	}))

	cases := []struct {
		builtin, subject string
		want             bool
	}{
		// Never-granted fields: group and role DO hold browse on the parent.
		{"check_article_author_only", `{"extsvc/group": "opa-ng-g"}`, false},
		{"check_article_author_only", `{"extsvc/role": "opa-ng-r"}`, false},
		{"check_article_editor", `{"extsvc/group": "opa-ng-g"}`, false},
		{"check_article_editor", `{"extsvc/role": "opa-ng-r"}`, false},
		// User: exclusion removes the author who can browse; intersection requires it.
		{"check_article_author_only", `{"extsvc/user": "opa-ng-plain"}`, true},
		{"check_article_author_only", `{"extsvc/user": "opa-ng-browser"}`, false},
		{"check_article_editor", `{"extsvc/user": "opa-ng-browser"}`, true},
		{"check_article_editor", `{"extsvc/user": "opa-ng-plain"}`, false},
	}
	for _, c := range cases {
		t.Run(c.builtin+" "+c.subject, func(t *testing.T) {
			got := evalOPA(t, ctx, "data.test.allow",
				"allow := extsvc."+c.builtin+"("+c.subject+`, "opa-ng-article", {})`)
			assert.Equal(t, c.want, got)
		})
	}
}

// evalOPAErr is evalOPA with rego.StrictBuiltinErrors, for policies
// expected to fail evaluation. Without strict mode OPA turns a builtin
// error into an undefined result (see the non-strict assertion below).
func evalOPAErr(t *testing.T, ctx context.Context, query, rule string) error {
	t.Helper()
	opts := []func(*rego.Rego){
		rego.Query(query),
		rego.Module("test.rego", "package test\nimport future.keywords.if\n\n"+rule+"\n"),
		rego.StrictBuiltinErrors(true),
	}
	opts = append(opts, extsvc.SpiceDBBuiltins(sb.Engine, ctx)...)
	_, err := rego.New(opts...).Eval(ctx)
	return err
}

// AUZ-029 — userset subjects in OPA builtins: the key "<type>#<subrel>"
// routes through CheckPermissionUserset / LookupResourcesUserset, with the
// same literal-userset semantics as the typed API.
func TestOPA_UsersetSubject_CheckAndLookup(t *testing.T) {
	ctx := context.Background()

	folder := extsvc.Folder("opa-us-folder")
	require.NoError(t, folder.CreateCollabRelations(ctx, extsvc.FolderCollabObjects{
		TeamAdmin: []extsvc.Team{"opa-us-team"},
	}))

	assert.Equal(t, true, evalOPA(t, ctx, "data.test.allow",
		`allow := extsvc.check_folder_collab_view({"extsvc/team#admin": "opa-us-team"}, "opa-us-folder", {})`))
	assert.Equal(t, false, evalOPA(t, ctx, "data.test.allow",
		`allow := extsvc.check_folder_collab_view({"extsvc/team#admin": "opa-us-other"}, "opa-us-folder", {})`))
	assert.Equal(t, []any{"opa-us-folder"}, evalOPA(t, ctx, "data.test.resources",
		`resources := extsvc.lookup_folder_collab_view_resources({"extsvc/team#admin": ["opa-us-team"]}, {})`))
}

// AUZ-029 — a caveated userset: request-time context reaches the engine.
func TestOPA_UsersetSubject_WithCaveat(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("opa-us-gated").CreateGatedCollabRelations(ctx, extsvc.FolderGatedCollabObjects{
		TeamAdmin: []extsvc.Team{"opa-us-gated-team"},
	}))

	assert.Equal(t, true, evalOPA(t, ctx, "data.test.allow",
		`allow := extsvc.check_folder_gated_collab_view({"extsvc/team#admin": "opa-us-gated-team"}, "opa-us-gated", {"tenant": "acme"})`))
	assert.Equal(t, false, evalOPA(t, ctx, "data.test.allow",
		`allow := extsvc.check_folder_gated_collab_view({"extsvc/team#admin": "opa-us-gated-team"}, "opa-us-gated", {"tenant": "other"})`))
	assert.Equal(t, []any{"opa-us-gated"}, evalOPA(t, ctx, "data.test.resources",
		`resources := extsvc.lookup_folder_gated_collab_view_resources({"extsvc/team#admin": ["opa-us-gated-team"]}, {"tenant": "acme"})`))
}

// AUZ-029 — mixed keys: check is AND across direct and userset keys;
// lookup takes the direct key first, as the typed Lookup*Resources does,
// even though "extsvc/team#admin" sorts before "extsvc/user".
func TestOPA_UsersetSubject_MixedKeys(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("opa-us-mixed-user").CreateMixedViewRelations(ctx, extsvc.FolderMixedViewObjects{
		User:      []extsvc.User{"opa-us-mixed-u"},
		TeamAdmin: []extsvc.Team{"opa-us-mixed-t"},
	}))
	require.NoError(t, extsvc.Folder("opa-us-mixed-team").CreateMixedViewRelations(ctx, extsvc.FolderMixedViewObjects{
		TeamAdmin: []extsvc.Team{"opa-us-mixed-t"},
	}))

	assert.Equal(t, true, evalOPA(t, ctx, "data.test.allow",
		`allow := extsvc.check_folder_mixed_browse({"extsvc/user": "opa-us-mixed-u", "extsvc/team#admin": "opa-us-mixed-t"}, "opa-us-mixed-user", {})`))
	assert.Equal(t, false, evalOPA(t, ctx, "data.test.allow",
		`allow := extsvc.check_folder_mixed_browse({"extsvc/user": "opa-us-mixed-u", "extsvc/team#admin": "opa-us-mixed-t"}, "opa-us-mixed-team", {})`),
		"AND: the user is not granted on the team-only folder")
	assert.Equal(t, []any{"opa-us-mixed-user"}, evalOPA(t, ctx, "data.test.resources",
		`resources := extsvc.lookup_folder_mixed_browse_resources({"extsvc/team#admin": ["opa-us-mixed-t"], "extsvc/user": ["opa-us-mixed-u"]}, {})`),
		"direct key wins over the userset key")
}

// AUZ-029 — "<type>#" with an empty sub-relation is a caller error.
func TestOPA_UsersetSubject_EmptySubRelation_Errors(t *testing.T) {
	ctx := context.Background()

	rule := `allow := extsvc.check_folder_collab_view({"extsvc/team#": "t"}, "opa-us-folder", {})`
	err := evalOPAErr(t, ctx, "data.test.allow", rule)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty sub-relation")

	// Default (non-strict) OPA: the builtin error leaves allow undefined.
	assert.Nil(t, evalOPA(t, ctx, "data.test.allow", rule))
}

// AUZ-029 (review of #21) — malformed keys are rejected even when their id
// list is empty, and even when a valid direct key would win the lookup.
func TestOPA_UsersetSubject_MalformedKeys_Error(t *testing.T) {
	ctx := context.Background()

	cases := map[string]string{
		"empty sub-relation, empty ids": `allow := extsvc.check_folder_collab_view({"extsvc/team#": []}, "f", {})`,
		"second separator":              `allow := extsvc.check_folder_collab_view({"extsvc/team#admin#": "t"}, "f", {})`,
		"empty subject type":            `allow := extsvc.check_folder_collab_view({"#admin": "t"}, "f", {})`,
		"malformed beside direct key":   `allow := extsvc.lookup_folder_mixed_browse_resources({"extsvc/team#admin#": ["t"], "extsvc/user": ["u"]}, {})`,
	}
	for name, rule := range cases {
		t.Run(name, func(t *testing.T) {
			err := evalOPAErr(t, ctx, "data.test.allow", rule)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid subject")
		})
	}
}
