package extsvc_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	extsvc "github.com/danhtran94/authzed-codegen/example/authzed/extsvc"
	"github.com/danhtran94/authzed-codegen/pkg/authz"
	"github.com/danhtran94/authzed-codegen/pkg/authz/spicedbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const schemaPath = "../../schema.zed"

type strID string

func (s strID) String() string { return string(s) }

// sb is the package-scoped Sandbox so tests that need direct API access
// (writing relationships outside the codegen's CreateXRelations methods,
// e.g. for fixtures the codegen doesn't yet cover) can reach
// authzed.Client. After AUZ-007 the codegen handles caveated writes
// natively, so this is no longer required for the tenant_match path —
// but the field stays available for future tests.
var sb *spicedbtest.Sandbox

func TestMain(m *testing.M) {
	ctx := context.Background()

	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "read schema (%s): %v\n", schemaPath, err)
		os.Exit(1)
	}

	sandbox, err := spicedbtest.Start(ctx, string(schema))
	if err != nil {
		if errors.Is(err, spicedbtest.ErrDockerUnavailable) {
			fmt.Println("SKIP: Docker not available — skipping extsvc tests")
			os.Exit(0)
		}
		_, _ = fmt.Fprintf(os.Stderr, "start spicedb sandbox: %v\n", err)
		os.Exit(1)
	}
	sb = sandbox
	authz.SetDefaultEngine(sb.Engine)

	code := m.Run()
	_ = sb.Close(ctx)
	os.Exit(code)
}


// --- Boilerplate identity tests (User / Group / Role) ---

func TestUser_Boilerplate(t *testing.T) {
	u := extsvc.UserStringer(strID("u-1"))
	require.Equal(t, extsvc.User("u-1"), u)

	us := extsvc.UserStringers(strID("u-a"), strID("u-b"))
	assert.Equal(t, []extsvc.User{"u-a", "u-b"}, us)

	assert.Equal(t, []extsvc.User{"u-l"}, extsvc.User("u-l").ToList())
}

func TestGroup_Boilerplate(t *testing.T) {
	g := extsvc.GroupStringer(strID("g-1"))
	require.Equal(t, extsvc.Group("g-1"), g)

	gs := extsvc.GroupStringers(strID("g-x"), strID("g-y"))
	assert.Equal(t, []extsvc.Group{"g-x", "g-y"}, gs)

	assert.Equal(t, []extsvc.Group{"g-l"}, extsvc.Group("g-l").ToList())
}

func TestRole_Boilerplate(t *testing.T) {
	r := extsvc.RoleStringer(strID("r-1"))
	require.Equal(t, extsvc.Role("r-1"), r)

	rs := extsvc.RoleStringers(strID("r-1"), strID("r-2"))
	assert.Equal(t, []extsvc.Role{"r-1", "r-2"}, rs)

	assert.Equal(t, []extsvc.Role{"r-l"}, extsvc.Role("r-l").ToList())
}

// --- Folder: union viewer + wildcard guest ---

func TestFolder_CheckBrowse_DirectViewer(t *testing.T) {
	ctx := context.Background()

	f := extsvc.Folder("tf-v1")
	require.NoError(t, f.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"tv1"},
	}))

	ok, err := f.CheckBrowse(ctx, extsvc.CheckFolderBrowseInputs{
		User: []extsvc.User{"tv1"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestFolder_CheckBrowse_Denied(t *testing.T) {
	ctx := context.Background()

	_, err := extsvc.Folder("tf-deny-1").CheckBrowse(ctx, extsvc.CheckFolderBrowseInputs{
		User: []extsvc.User{"nobody"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

// folder.browse = viewer (not viewer + guest); the guest relation is
// wildcard-only data not flowing into any permission. So a viewer of any
// allowed type unlocks browse.
func TestFolder_CheckBrowse_ViaGroup(t *testing.T) {
	ctx := context.Background()

	f := extsvc.Folder("tf-vg1")
	require.NoError(t, f.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		Group: []extsvc.Group{"tg-vg1"},
	}))

	ok, err := f.CheckBrowse(ctx, extsvc.CheckFolderBrowseInputs{
		Group: []extsvc.Group{"tg-vg1"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestFolder_ReadGuestUserWildcard_Set(t *testing.T) {
	ctx := context.Background()

	f := extsvc.Folder("tf-w-yes")
	require.NoError(t, f.CreateGuestRelations(ctx, extsvc.FolderGuestObjects{
		Wildcards: extsvc.FolderGuestWildcards{User: true},
	}))

	_, isWildcard, err := f.ReadGuestUserWildcard(ctx)
	require.NoError(t, err)
	assert.True(t, isWildcard)
}

func TestFolder_ReadGuestUserWildcard_Unset(t *testing.T) {
	ctx := context.Background()

	// guest is wildcard-only on the schema; an unset wildcard surfaces as false
	// without writing any tuple.
	f := extsvc.Folder("tf-w-no")

	_, isWildcard, err := f.ReadGuestUserWildcard(ctx)
	require.NoError(t, err)
	assert.False(t, isWildcard)
}

func TestFolder_ReadViewerUserRelations(t *testing.T) {
	ctx := context.Background()

	f := extsvc.Folder("tf-vu3")
	require.NoError(t, f.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User:  []extsvc.User{"tvu3"},
		Group: []extsvc.Group{"tg1"},
		Role:  []extsvc.Role{"tr1"},
	}))

	users, err := f.ReadViewerUserRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []extsvc.User{"tvu3"}, authz.IDsOf(users))
}

func TestFolder_LookupBrowseUserSubjects(t *testing.T) {
	ctx := context.Background()

	f := extsvc.Folder("tf-vu4")
	require.NoError(t, f.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"tvu4a", "tvu4b"},
	}))

	ids, err := f.LookupBrowseUserSubjects(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []extsvc.User{"tvu4a", "tvu4b"}, ids.Definite)
}

// --- Document: arrow-only view + mixed identifier+arrow edit ---

func TestDocument_CheckView_ParentBrowse(t *testing.T) {
	ctx := context.Background()

	f := extsvc.Folder("fv1")
	require.NoError(t, f.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"do1"},
	}))

	doc := extsvc.Document("doc1")
	require.NoError(t, doc.CreateParentRelations(ctx, extsvc.DocumentParentObjects{
		Folder: []extsvc.Folder{f},
	}))
	require.NoError(t, doc.CreateOwnerRelations(ctx, extsvc.DocumentOwnerObjects{
		User:  []extsvc.User{"do1"},
		Group: []extsvc.Group{"dg1"},
	}))

	// view = parent->browse (arrow only)
	ok, err := doc.CheckView(ctx, extsvc.CheckDocumentViewInputs{
		User: []extsvc.User{"do1"},
	})
	require.NoError(t, err)
	assert.True(t, ok)

	_, err = doc.CheckView(ctx, extsvc.CheckDocumentViewInputs{
		User: []extsvc.User{"nottheviewer"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

func TestDocument_CheckEdit_OwnerOrParentBrowse(t *testing.T) {
	ctx := context.Background()

	f := extsvc.Folder("fv2")
	require.NoError(t, f.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"de2"},
	}))

	doc := extsvc.Document("doc2")
	require.NoError(t, doc.CreateParentRelations(ctx, extsvc.DocumentParentObjects{
		Folder: []extsvc.Folder{f},
	}))
	require.NoError(t, doc.CreateOwnerRelations(ctx, extsvc.DocumentOwnerObjects{
		User:  []extsvc.User{"do2"},
		Group: []extsvc.Group{"dg2"},
	}))

	// edit = owner + parent->browse — direct owner branch
	ok, err := doc.CheckEdit(ctx, extsvc.CheckDocumentEditInputs{
		User: []extsvc.User{"do2"},
	})
	require.NoError(t, err)
	assert.True(t, ok)

	// owner-by-group branch
	ok, err = doc.CheckEdit(ctx, extsvc.CheckDocumentEditInputs{
		Group: []extsvc.Group{"dg2"},
	})
	require.NoError(t, err)
	assert.True(t, ok)

	// parent->browse branch (de2 is folder viewer, not owner)
	ok, err = doc.CheckEdit(ctx, extsvc.CheckDocumentEditInputs{
		User: []extsvc.User{"de2"},
	})
	require.NoError(t, err)
	assert.True(t, ok)

	// neither owner nor browser
	_, err = doc.CheckEdit(ctx, extsvc.CheckDocumentEditInputs{
		User: []extsvc.User{"nobody"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

func TestDocument_ReadParent(t *testing.T) {
	ctx := context.Background()

	doc := extsvc.Document("doc4")
	require.NoError(t, doc.CreateParentRelations(ctx, extsvc.DocumentParentObjects{
		Folder: []extsvc.Folder{"fv4"},
	}))

	folders, err := doc.ReadParentFolderRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []extsvc.Folder{"fv4"}, authz.IDsOf(folders))
}

// --- Article: intersection (editor) and exclusion (author_only) ---

func TestArticle_CheckEditor_AuthorOnly_NotEditor(t *testing.T) {
	ctx := context.Background()

	// author=au1, no parent → editor (= author & parent->browse) should fail
	art := extsvc.Article("art1")
	require.NoError(t, art.CreateAuthorRelations(ctx, extsvc.ArticleAuthorObjects{
		User: []extsvc.User{"au1"},
	}))

	_, err := art.CheckEditor(ctx, extsvc.CheckArticleEditorInputs{
		User: []extsvc.User{"au1"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

func TestArticle_CheckEditor_AuthorPlusFolderViewer(t *testing.T) {
	ctx := context.Background()

	// folder.browse = viewer, so au2 needs to be in folder.viewer (not guest)
	// to get parent->browse on the article.
	f := extsvc.Folder("af1")
	require.NoError(t, f.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"au2"},
	}))

	art := extsvc.Article("art2")
	require.NoError(t, art.CreateAuthorRelations(ctx, extsvc.ArticleAuthorObjects{
		User: []extsvc.User{"au2"},
	}))
	require.NoError(t, art.CreateParentRelations(ctx, extsvc.ArticleParentObjects{
		Folder: []extsvc.Folder{f},
	}))

	// editor = author & parent->browse — both legs hold for au2
	ok, err := art.CheckEditor(ctx, extsvc.CheckArticleEditorInputs{
		User: []extsvc.User{"au2"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestArticle_CheckAuthorOnly_NoParent(t *testing.T) {
	ctx := context.Background()

	// author=au3, no parent → author_only (= author - parent->browse) should be true
	art := extsvc.Article("art3")
	require.NoError(t, art.CreateAuthorRelations(ctx, extsvc.ArticleAuthorObjects{
		User: []extsvc.User{"au3"},
	}))

	ok, err := art.CheckAuthorOnly(ctx, extsvc.CheckArticleAuthorOnlyInputs{
		User: []extsvc.User{"au3"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestArticle_CheckAuthorOnly_ExcludedByFolderViewer(t *testing.T) {
	ctx := context.Background()

	// folder.browse = viewer; au4 in viewer means au4 has parent->browse,
	// which excludes au4 from author_only (= author - parent->browse).
	f := extsvc.Folder("af2")
	require.NoError(t, f.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"au4"},
	}))

	art := extsvc.Article("art4")
	require.NoError(t, art.CreateAuthorRelations(ctx, extsvc.ArticleAuthorObjects{
		User: []extsvc.User{"au4"},
	}))
	require.NoError(t, art.CreateParentRelations(ctx, extsvc.ArticleParentObjects{
		Folder: []extsvc.Folder{f},
	}))

	_, err := art.CheckAuthorOnly(ctx, extsvc.CheckArticleAuthorOnlyInputs{
		User: []extsvc.User{"au4"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

func TestArticle_ReadAuthor(t *testing.T) {
	ctx := context.Background()

	art := extsvc.Article("art5")
	require.NoError(t, art.CreateAuthorRelations(ctx, extsvc.ArticleAuthorObjects{
		User: []extsvc.User{"au5"},
	}))
	require.NoError(t, art.CreateParentRelations(ctx, extsvc.ArticleParentObjects{
		Folder: []extsvc.Folder{"af3"},
	}))

	users, err := art.ReadAuthorUserRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []extsvc.User{"au5"}, authz.IDsOf(users))
}

func TestArticle_ReadParent(t *testing.T) {
	ctx := context.Background()

	art := extsvc.Article("art6")
	require.NoError(t, art.CreateParentRelations(ctx, extsvc.ArticleParentObjects{
		Folder: []extsvc.Folder{"af4"},
	}))

	folders, err := art.ReadParentFolderRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []extsvc.Folder{"af4"}, authz.IDsOf(folders))
}

func TestArticle_LookupEditorUserSubjects(t *testing.T) {
	ctx := context.Background()

	f := extsvc.Folder("af5")
	require.NoError(t, f.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"au7"},
	}))

	art := extsvc.Article("art7")
	require.NoError(t, art.CreateAuthorRelations(ctx, extsvc.ArticleAuthorObjects{
		User: []extsvc.User{"au7"},
	}))
	require.NoError(t, art.CreateParentRelations(ctx, extsvc.ArticleParentObjects{
		Folder: []extsvc.Folder{f},
	}))

	ids, err := art.LookupEditorUserSubjects(ctx)
	require.NoError(t, err)
	assert.Contains(t, ids.Definite, extsvc.User("au7"))
}

// --- Folder.tenanted_browse: caveat codegen path (AUZ-006 read + AUZ-007 write) ---
//
// Schema: relation tenanted_viewer: extsvc/user with extsvc/tenant_match,
// caveat tenant_match(tenant string) { tenant == "acme" }.
// Writes go through the codegen's CreateTenantedViewerRelations with
// UserCaveat: nil — equivalent to a name-only attach on the wire,
// deferring all parameter binding to check time. SPEC-003 A6 [B1]
// verified this is wire-legal.

func TestFolder_CheckTenantedBrowse_MatchTenant(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("tcb-match").CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-match"}, // Caveats zero-value defers all binding to check time
	}))

	ok, err := extsvc.Folder("tcb-match").CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User: []extsvc.User{"u-match"},
		Caveats: extsvc.CheckFolderTenantedBrowseCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestFolder_CheckTenantedBrowse_WrongTenant(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("tcb-wrong").CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-wrong"},
	}))

	_, err := extsvc.Folder("tcb-wrong").CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User: []extsvc.User{"u-wrong"},
		Caveats: extsvc.CheckFolderTenantedBrowseCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("not-acme")},
		},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

// Nil Caveat → SpiceDB returns CONDITIONAL_PERMISSION (caveat couldn't
// bind the `tenant` param). errorIfDenied maps anything other than
// HAS_PERMISSION to ErrPermissionDenied, so the conservative default is
// deny. A future job may surface CONDITIONAL as a distinct signal.
func TestFolder_CheckTenantedBrowse_NilCaveat(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("tcb-nil").CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-nil"},
	}))

	_, err := extsvc.Folder("tcb-nil").CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User: []extsvc.User{"u-nil"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

// AUZ-007 wildcard + caveat — exercises the wildcard branch of
// CreateGuardedViewerRelations (objects.Wildcards.User = true) with a
// nil UserCaveat (defer pattern). The generated wildcard branch must
// route through CreateRelationsWithCaveat (not the existing
// CreateRelations) and embed "extsvc/tenant_match" as the caveat-name
// literal. Schema legality of `type:* with caveat` confirmed by
// SPEC-003 A5 (Authzed docs).
func TestFolder_CreateGuardedViewer_WildcardDefer(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("gv-defer").CreateGuardedViewerRelations(ctx, extsvc.FolderGuardedViewerObjects{
		Wildcards: extsvc.FolderGuardedViewerWildcards{User: true},
	}))

	ok, err := extsvc.Folder("gv-defer").CheckGuardedBrowse(ctx, extsvc.CheckFolderGuardedBrowseInputs{
		User: []extsvc.User{"any-user"},
		Caveats: extsvc.CheckFolderGuardedBrowseCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
	})
	require.NoError(t, err)
	assert.True(t, ok, "wildcard caveat tuple must grant any user when check supplies the matching tenant")
}

// AUZ-007 wildcard + caveat — pre-binding variant. Write supplies
// {tenant=acme} at write time; check supplies a conflicting tenant.
// Write-time wins (SPEC-003 A6 [A3]).
func TestFolder_CreateGuardedViewer_WildcardPreBound(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("gv-prebind").CreateGuardedViewerRelations(ctx, extsvc.FolderGuardedViewerObjects{
		Wildcards: extsvc.FolderGuardedViewerWildcards{User: true},
		Caveats: extsvc.FolderGuardedViewerCaveats{
			User: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
	}))

	ok, err := extsvc.Folder("gv-prebind").CheckGuardedBrowse(ctx, extsvc.CheckFolderGuardedBrowseInputs{
		User: []extsvc.User{"some-other-user"},
		Caveats: extsvc.CheckFolderGuardedBrowseCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("would-fail-if-check-won")},
		},
	})
	require.NoError(t, err)
	assert.True(t, ok, "wildcard caveat with write-time pre-binding must override conflicting check-time context")
}

// AUZ-007 write-time pre-binding — write {tenant=acme}, then attempt to
// override at check time with a different tenant. SPEC-003 A6 [A3]:
// write-time wins on key collision, so the override is silently ignored
// and the check still grants. Locks in the precedence semantics.
func TestFolder_CreateTenantedViewer_PreBound_WriteWins(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("tcb-prebind").CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-prebind"},
		Caveats: extsvc.FolderTenantedViewerCaveats{
			User: &extsvc.TenantMatchArgs{Tenant: new("acme")}, // pre-bind at write
		},
	}))

	ok, err := extsvc.Folder("tcb-prebind").CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User: []extsvc.User{"u-prebind"},
		Caveats: extsvc.CheckFolderTenantedBrowseCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("attacker-override")}, // would deny if check could win
		},
	})
	require.NoError(t, err)
	assert.True(t, ok, "write-time pre-binding must override conflicting check-time context")
}

// --- Multi-param caveat: extsvc/within_window(allowed_actions list<string>, requested_action string) ---
//
// Exercises the codegen's multi-key context map emission and the
// list<string> → []string type mapping. Test pattern uses defer-all
// (write nil, check supplies both) to side-step the "pre-bind binds
// zero values" gotcha that single-struct partial binding can't avoid
// without per-field pointer types (deferred to a future job).

func TestFolder_Act_DeferAll_ActionAllowed(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("act-allow").CreateActorRelations(ctx, extsvc.FolderActorObjects{
		User: []extsvc.User{"u-actor"},
	}))

	ok, err := extsvc.Folder("act-allow").CheckAct(ctx, extsvc.CheckFolderActInputs{
		User: []extsvc.User{"u-actor"},
		Caveats: extsvc.CheckFolderActCaveats{
			WithinWindow: &extsvc.WithinWindowArgs{
				AllowedActions:  []string{"read", "write"},
				RequestedAction: new("read"),
			},
		},
	})
	require.NoError(t, err)
	assert.True(t, ok, "requested_action in allowed_actions must grant")
}

func TestFolder_Act_DeferAll_ActionDenied(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("act-deny").CreateActorRelations(ctx, extsvc.FolderActorObjects{
		User: []extsvc.User{"u-act-deny"},
	}))

	_, err := extsvc.Folder("act-deny").CheckAct(ctx, extsvc.CheckFolderActInputs{
		User: []extsvc.User{"u-act-deny"},
		Caveats: extsvc.CheckFolderActCaveats{
			WithinWindow: &extsvc.WithinWindowArgs{
				AllowedActions:  []string{"read", "write"},
				RequestedAction: new("delete"),
			},
		},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

// Pre-bind {AllowedActions: ["read"], RequestedAction: "read"} at write
// time, confirming the multi-key wire encoding works end-to-end. Check
// supplies a different action; write-time wins, eval evaluates against
// the write-time RequestedAction = "read". "read" in ["read"] → grant.
func TestFolder_Act_PreBoundBoth_WriteWinsOnAction(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("act-prebind").CreateActorRelations(ctx, extsvc.FolderActorObjects{
		User: []extsvc.User{"u-act-prebind"},
		Caveats: extsvc.FolderActorCaveats{
			User: &extsvc.WithinWindowArgs{
				AllowedActions:  []string{"read"},
				RequestedAction: new("read"),
			},
		},
	}))

	ok, err := extsvc.Folder("act-prebind").CheckAct(ctx, extsvc.CheckFolderActInputs{
		User: []extsvc.User{"u-act-prebind"},
		Caveats: extsvc.CheckFolderActCaveats{
			WithinWindow: &extsvc.WithinWindowArgs{
				AllowedActions:  []string{"read"},
				RequestedAction: new("delete"), // would deny if check could win
			},
		},
	})
	require.NoError(t, err)
	assert.True(t, ok, "write-time RequestedAction must take precedence over check-time on key collision")
}

// --- Mixed caveated/non-caveated allowed types: relation collaborator: user with within_window | group ---
//
// The User branch routes through CreateRelationsWithCaveat (caveated);
// the Group branch routes through plain CreateRelations (non-caveated).
// Both within the same generated method body. permCaveat walker finds
// one distinct caveat reachable from `collaborate = collaborator`, so
// CheckCollaborateInputs gets a single Caveat *WithinWindowArgs field.
// For Group subjects, SpiceDB doesn't evaluate the caveat (the Group
// tuple is non-caveated).

func TestFolder_Collaborator_UserBranchCaveated(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("co-u").CreateCollaboratorRelations(ctx, extsvc.FolderCollaboratorObjects{
		User: []extsvc.User{"u-co"},
	}))

	ok, err := extsvc.Folder("co-u").CheckCollaborate(ctx, extsvc.CheckFolderCollaborateInputs{
		User: []extsvc.User{"u-co"},
		Caveats: extsvc.CheckFolderCollaborateCaveats{
			WithinWindow: &extsvc.WithinWindowArgs{
				AllowedActions:  []string{"edit"},
				RequestedAction: new("edit"),
			},
		},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestFolder_Collaborator_GroupBranchNonCaveated(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("co-g").CreateCollaboratorRelations(ctx, extsvc.FolderCollaboratorObjects{
		Group: []extsvc.Group{"g-co"},
	}))

	// Group tuple is non-caveated; the Caveat field on CheckCollaborateInputs
	// can be nil for Group-only checks.
	ok, err := extsvc.Folder("co-g").CheckCollaborate(ctx, extsvc.CheckFolderCollaborateInputs{
		Group: []extsvc.Group{"g-co"},
	})
	require.NoError(t, err)
	assert.True(t, ok, "non-caveated Group tuple must grant without caveat context")
}

// Both branches in one Create call — User via CreateRelationsWithCaveat,
// Group via CreateRelations, atomically.
func TestFolder_Collaborator_BothBranchesInOneCall(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("co-both").CreateCollaboratorRelations(ctx, extsvc.FolderCollaboratorObjects{
		User:  []extsvc.User{"u-both"},
		Group: []extsvc.Group{"g-both"},
	}))

	// Check via User (caveat eval required)
	okUser, err := extsvc.Folder("co-both").CheckCollaborate(ctx, extsvc.CheckFolderCollaborateInputs{
		User: []extsvc.User{"u-both"},
		Caveats: extsvc.CheckFolderCollaborateCaveats{
			WithinWindow: &extsvc.WithinWindowArgs{
				AllowedActions:  []string{"edit"},
				RequestedAction: new("edit"),
			},
		},
	})
	require.NoError(t, err)
	assert.True(t, okUser)

	// Check via Group (no caveat eval)
	okGroup, err := extsvc.Folder("co-both").CheckCollaborate(ctx, extsvc.CheckFolderCollaborateInputs{
		Group: []extsvc.Group{"g-both"},
	})
	require.NoError(t, err)
	assert.True(t, okGroup)
}

// --- Bool + int param types: caveat quota_check(within_quota bool, max_uses int) ---
//
// Exercises caveatTypeToGo for non-string types: `int` → `int64`,
// `bool` → `bool`. Generated QuotaCheckArgs has typed fields, the
// generated map building emits the correct value types, and SpiceDB
// evaluates the CEL boolean expression against them.

func TestFolder_RateCheck_QuotaActive(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("rc-ok").CreateRateLimitedRelations(ctx, extsvc.FolderRateLimitedObjects{
		User: []extsvc.User{"u-rc-ok"},
		Caveats: extsvc.FolderRateLimitedCaveats{
			User: &extsvc.QuotaCheckArgs{
				WithinQuota: new(true),
				MaxUses:     new(5),
			},
		},
	}))

	ok, err := extsvc.Folder("rc-ok").CheckRateCheck(ctx, extsvc.CheckFolderRateCheckInputs{
		User: []extsvc.User{"u-rc-ok"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestFolder_RateCheck_QuotaExhausted(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("rc-no").CreateRateLimitedRelations(ctx, extsvc.FolderRateLimitedObjects{
		User: []extsvc.User{"u-rc-no"},
		Caveats: extsvc.FolderRateLimitedCaveats{
			User: &extsvc.QuotaCheckArgs{
				WithinQuota: new(false),
				MaxUses:     new(5),
			},
		},
	}))

	_, err := extsvc.Folder("rc-no").CheckRateCheck(ctx, extsvc.CheckFolderRateCheckInputs{
		User: []extsvc.User{"u-rc-no"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied, "WithinQuota=false must collapse the && to deny")
}

// --- AUZ-007 extension: type-coverage gap closure ---

// SPEC-003 C5 / A3 verification — DeleteRelations on a caveated tuple
// matches by 6-column identity (no caveat needed). Source-line evidence
// (memdb readwrite.go:66-75 + postgres readwrite.go:779-788) confirmed
// the semantics; this test verifies it empirically end-to-end. Write a
// caveated tuple, prove it grants, delete via codegen Delete<Rel>, prove
// it denies (tuple gone).
func TestFolder_TenantedViewer_DeleteRemovesCaveatedTuple(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("tcb-del").CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-del"},
	}))

	ok, err := extsvc.Folder("tcb-del").CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User: []extsvc.User{"u-del"},
		Caveats: extsvc.CheckFolderTenantedBrowseCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
	})
	require.NoError(t, err)
	require.True(t, ok, "tuple must exist before delete")

	// Delete via codegen — Delete<Rel>Relations uses plain DeleteRelations
	// with no OptionalCaveat (SPEC-003 C5). The 6-column identity match
	// removes the caveated tuple.
	require.NoError(t, extsvc.Folder("tcb-del").DeleteTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-del"},
	}))

	_, err = extsvc.Folder("tcb-del").CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User: []extsvc.User{"u-del"},
		Caveats: extsvc.CheckFolderTenantedBrowseCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied, "tuple must be gone after delete — SPEC-003 C5/A3")
}

// --- double param type → Go float64 ---

func TestFolder_ScoreCheck_AboveMin_Grants(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("sc-ok").CreateScoredViewerRelations(ctx, extsvc.FolderScoredViewerObjects{
		User: []extsvc.User{"u-sc-ok"},
	}))

	ok, err := extsvc.Folder("sc-ok").CheckScoreCheck(ctx, extsvc.CheckFolderScoreCheckInputs{
		User: []extsvc.User{"u-sc-ok"},
		Caveats: extsvc.CheckFolderScoreCheckCaveats{
			MinScore: &extsvc.MinScoreArgs{
				MinRequired: new(0.5),
				Current:     new(0.7),
			},
		},
	})
	require.NoError(t, err)
	assert.True(t, ok, "current 0.7 >= min 0.5 must grant")
}

func TestFolder_ScoreCheck_BelowMin_Denies(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("sc-no").CreateScoredViewerRelations(ctx, extsvc.FolderScoredViewerObjects{
		User: []extsvc.User{"u-sc-no"},
	}))

	_, err := extsvc.Folder("sc-no").CheckScoreCheck(ctx, extsvc.CheckFolderScoreCheckInputs{
		User: []extsvc.User{"u-sc-no"},
		Caveats: extsvc.CheckFolderScoreCheckCaveats{
			MinScore: &extsvc.MinScoreArgs{
				MinRequired: new(0.5),
				Current:     new(0.3),
			},
		},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

// --- bytes param type → Go []byte ---

func TestFolder_TokenCheck_NonEmptyToken_Grants(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("tk-ok").CreateTokenViewerRelations(ctx, extsvc.FolderTokenViewerObjects{
		User: []extsvc.User{"u-tk"},
		Caveats: extsvc.FolderTokenViewerCaveats{
			User: &extsvc.HasTokenArgs{
				Token: []byte("hello"),
			},
		},
	}))

	ok, err := extsvc.Folder("tk-ok").CheckTokenCheck(ctx, extsvc.CheckFolderTokenCheckInputs{
		User: []extsvc.User{"u-tk"},
	})
	require.NoError(t, err)
	assert.True(t, ok, "size(token) > 0 must grant for non-empty bytes")
}

// --- uint param type → Go uint64 ---

func TestFolder_VersionCheck_PositiveVersion_Grants(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("vc-ok").CreateVersionedViewerRelations(ctx, extsvc.FolderVersionedViewerObjects{
		User: []extsvc.User{"u-vc"},
		Caveats: extsvc.FolderVersionedViewerCaveats{
			User: &extsvc.VersionCheckArgs{
				MinVersion: new(uint(1)),
			},
		},
	}))

	ok, err := extsvc.Folder("vc-ok").CheckVersionCheckPerm(ctx, extsvc.CheckFolderVersionCheckPermInputs{
		User: []extsvc.User{"u-vc"},
	})
	require.NoError(t, err)
	assert.True(t, ok, "min_version 1 > 0 must grant")
}

// --- nested list type list<list<string>> → Go [][]string ---
//
// Exercises the reflection fallback in coerceStructpbValue. Without it,
// structpb.NewStruct would reject [][]string. With it, the outer slice
// converts to []any of []any of string. This test would fail if the
// reflection fallback were removed.
func TestFolder_MatrixCheck_NonEmptyMatrix_Grants(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("mx-ok").CreateMatrixViewerRelations(ctx, extsvc.FolderMatrixViewerObjects{
		User: []extsvc.User{"u-mx"},
		Caveats: extsvc.FolderMatrixViewerCaveats{
			User: &extsvc.MatrixCheckArgs{
				Rows: [][]string{{"a", "b"}, {"c"}},
			},
		},
	}))

	ok, err := extsvc.Folder("mx-ok").CheckMatrixCheckPerm(ctx, extsvc.CheckFolderMatrixCheckPermInputs{
		User: []extsvc.User{"u-mx"},
	})
	require.NoError(t, err)
	assert.True(t, ok, "non-empty rows must grant — verifies reflection-based slice coercion")
}

// --- Multi-caveat-per-permission: lifts AUZ-006 single-caveat cap ---
//
// Schema: permission multi_check = tenanted_user + windowed_user, where
//   tenanted_user: user with tenant_match
//   windowed_user: user with within_window
// Generated CheckFolderMultiCheckInputs.Caveats has one field per
// reachable caveat (TenantMatch + WithinWindow). The Check method
// body merges non-nil entries into one wire Context map; SpiceDB
// routes keys to whichever tuple's caveat needs them.

func TestFolder_MultiCheck_GrantsViaTenantedPath(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("mc-t").CreateTenantedUserRelations(ctx, extsvc.FolderTenantedUserObjects{
		User: []extsvc.User{"u-mc-t"},
	}))

	ok, err := extsvc.Folder("mc-t").CheckMultiCheck(ctx, extsvc.CheckFolderMultiCheckInputs{
		User: []extsvc.User{"u-mc-t"},
		Caveats: extsvc.CheckFolderMultiCheckCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("acme")},
			// WithinWindow nil — irrelevant since no windowed_user tuple
		},
	})
	require.NoError(t, err)
	assert.True(t, ok, "tenanted_user tuple matches via tenant_match alone")
}

func TestFolder_MultiCheck_GrantsViaWindowedPath(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("mc-w").CreateWindowedUserRelations(ctx, extsvc.FolderWindowedUserObjects{
		User: []extsvc.User{"u-mc-w"},
	}))

	ok, err := extsvc.Folder("mc-w").CheckMultiCheck(ctx, extsvc.CheckFolderMultiCheckInputs{
		User: []extsvc.User{"u-mc-w"},
		Caveats: extsvc.CheckFolderMultiCheckCaveats{
			WithinWindow: &extsvc.WithinWindowArgs{
				AllowedActions:  []string{"edit"},
				RequestedAction: new("edit"),
			},
		},
	})
	require.NoError(t, err)
	assert.True(t, ok, "windowed_user tuple matches via within_window alone")
}

func TestFolder_MultiCheck_DeniedWhenNeitherCaveatBound(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("mc-no").CreateTenantedUserRelations(ctx, extsvc.FolderTenantedUserObjects{
		User: []extsvc.User{"u-mc-no"},
	}))

	// No caveats supplied — tenanted_user tuple's tenant_match can't bind
	// `tenant`, returns CONDITIONAL_PERMISSION which errorIfDenied maps
	// to ErrPermissionDenied.
	_, err := extsvc.Folder("mc-no").CheckMultiCheck(ctx, extsvc.CheckFolderMultiCheckInputs{
		User: []extsvc.User{"u-mc-no"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

// --- Caveat × Arrow ---
//
// `permission via_gated_root = gated_root->browse` where
// `gated_root: extsvc/folder with extsvc/tenant_match`.
// walkPermCaveats collects LeftRel caveats only — TenantMatch reaches
// the permission, but the right side's `browse` permission resolves
// through a different folder object's permission tree and is not part
// of this Check call's wire Context.

func TestFolder_ViaGatedRoot_GrantsWhenTenantMatches(t *testing.T) {
	ctx := context.Background()

	// Folder B has user X via plain (non-caveated) viewer.
	require.NoError(t, extsvc.Folder("vgr-b").CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"u-vgr"},
	}))

	// Folder A has gated_root → folder B with tenant_match attached, deferred.
	require.NoError(t, extsvc.Folder("vgr-a").CreateGatedRootRelations(ctx, extsvc.FolderGatedRootObjects{
		Folder: []extsvc.Folder{"vgr-b"},
	}))

	ok, err := extsvc.Folder("vgr-a").CheckViaGatedRoot(ctx, extsvc.CheckFolderViaGatedRootInputs{
		User: []extsvc.User{"u-vgr"},
		Caveats: extsvc.CheckFolderViaGatedRootCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
	})
	require.NoError(t, err)
	assert.True(t, ok, "arrow + caveat: LeftRel caveat satisfied + arrow target grants → grant")
}

func TestFolder_ViaGatedRoot_DeniesWhenTenantMismatches(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("vgr-x-b").CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"u-vgr-x"},
	}))
	require.NoError(t, extsvc.Folder("vgr-x-a").CreateGatedRootRelations(ctx, extsvc.FolderGatedRootObjects{
		Folder: []extsvc.Folder{"vgr-x-b"},
	}))

	_, err := extsvc.Folder("vgr-x-a").CheckViaGatedRoot(ctx, extsvc.CheckFolderViaGatedRootInputs{
		User: []extsvc.User{"u-vgr-x"},
		Caveats: extsvc.CheckFolderViaGatedRootCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("wrong")},
		},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

// --- Caveat × Intersection ---
//
// `permission elite_access = scored_viewer & token_viewer` — both legs
// caveated. CheckEliteAccessInputs.Caveats has both MinScore + HasToken
// fields. SpiceDB grants only when both legs match AND both caveats
// evaluate true.

func TestFolder_EliteAccess_GrantsWhenBothLegsHold(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("ea-ok").CreateScoredViewerRelations(ctx, extsvc.FolderScoredViewerObjects{
		User: []extsvc.User{"u-ea"},
	}))
	require.NoError(t, extsvc.Folder("ea-ok").CreateTokenViewerRelations(ctx, extsvc.FolderTokenViewerObjects{
		User: []extsvc.User{"u-ea"},
		Caveats: extsvc.FolderTokenViewerCaveats{
			User: &extsvc.HasTokenArgs{Token: []byte("opaque")},
		},
	}))

	ok, err := extsvc.Folder("ea-ok").CheckEliteAccess(ctx, extsvc.CheckFolderEliteAccessInputs{
		User: []extsvc.User{"u-ea"},
		Caveats: extsvc.CheckFolderEliteAccessCaveats{
			MinScore: &extsvc.MinScoreArgs{MinRequired: new(0.5), Current: new(0.9)},
			// HasToken pre-bound at write — check-side empty is fine
		},
	})
	require.NoError(t, err)
	assert.True(t, ok, "intersection + caveat: both legs held with caveats satisfied → grant")
}

// AUZ-007 ext — cross-caveat split merge through codegen.
//
// elite_access intersects scored_viewer (caveat: min_score) and
// token_viewer (caveat: has_token). Each caveat is bound on a
// DIFFERENT side: min_score is fully pre-bound at WRITE time,
// has_token is deferred at write and supplied at CHECK time.
//
// SpiceDB's per-tuple evaluation merges write-time + check-time
// context for each tuple independently:
//   - scored_viewer tuple sees {min_required:0.5, current:0.9} from
//     write only → eval true.
//   - token_viewer tuple sees {token:<bytes>} from check only → eval true.
// Intersection: both legs grant → grant.
//
// This proves the codegen-driven path matches SpiceDB's documented
// per-key union behavior (SPEC-003 A6 [A1]) end-to-end through nested
// Caveats sub-structs on both Objects and CheckXInputs.
func TestFolder_EliteAccess_SplitWriteCheckMerge_Grants(t *testing.T) {
	ctx := context.Background()

	// scored_viewer: write supplies all min_score params.
	require.NoError(t, extsvc.Folder("ea-split").CreateScoredViewerRelations(ctx, extsvc.FolderScoredViewerObjects{
		User: []extsvc.User{"u-ea-split"},
		Caveats: extsvc.FolderScoredViewerCaveats{
			User: &extsvc.MinScoreArgs{MinRequired: new(0.5), Current: new(0.9)},
		},
	}))

	// token_viewer: write defers (no Caveats), check will supply Token.
	require.NoError(t, extsvc.Folder("ea-split").CreateTokenViewerRelations(ctx, extsvc.FolderTokenViewerObjects{
		User: []extsvc.User{"u-ea-split"},
	}))

	// Check supplies HasToken only — MinScore omitted (write-bound).
	ok, err := extsvc.Folder("ea-split").CheckEliteAccess(ctx, extsvc.CheckFolderEliteAccessInputs{
		User: []extsvc.User{"u-ea-split"},
		Caveats: extsvc.CheckFolderEliteAccessCaveats{
			HasToken: &extsvc.HasTokenArgs{Token: []byte("opaque")},
			// MinScore deliberately nil — its keys come from the
			// write-time pre-context attached to the scored_viewer tuple.
		},
	})
	require.NoError(t, err)
	assert.True(t, ok, "intersection across one write-bound caveat and one check-bound caveat must grant when each side's keys come together via per-tuple merge")
}

// Same setup but check supplies NEITHER caveat. min_score still
// satisfies (write-bound), has_token can't bind → CONDITIONAL → deny.
func TestFolder_EliteAccess_SplitMerge_DeniesWhenCheckTokenMissing(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("ea-split-no").CreateScoredViewerRelations(ctx, extsvc.FolderScoredViewerObjects{
		User: []extsvc.User{"u-ea-split-no"},
		Caveats: extsvc.FolderScoredViewerCaveats{
			User: &extsvc.MinScoreArgs{MinRequired: new(0.5), Current: new(0.9)},
		},
	}))
	require.NoError(t, extsvc.Folder("ea-split-no").CreateTokenViewerRelations(ctx, extsvc.FolderTokenViewerObjects{
		User: []extsvc.User{"u-ea-split-no"},
	}))

	// Check supplies neither caveat. token_viewer leg can't bind.
	_, err := extsvc.Folder("ea-split-no").CheckEliteAccess(ctx, extsvc.CheckFolderEliteAccessInputs{
		User: []extsvc.User{"u-ea-split-no"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

func TestFolder_EliteAccess_DeniesWhenScoreFails(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("ea-no").CreateScoredViewerRelations(ctx, extsvc.FolderScoredViewerObjects{
		User: []extsvc.User{"u-ea-no"},
	}))
	require.NoError(t, extsvc.Folder("ea-no").CreateTokenViewerRelations(ctx, extsvc.FolderTokenViewerObjects{
		User: []extsvc.User{"u-ea-no"},
		Caveats: extsvc.FolderTokenViewerCaveats{
			User: &extsvc.HasTokenArgs{Token: []byte("opaque")},
		},
	}))

	// Score below threshold — intersection: any leg failing → deny
	_, err := extsvc.Folder("ea-no").CheckEliteAccess(ctx, extsvc.CheckFolderEliteAccessInputs{
		User: []extsvc.User{"u-ea-no"},
		Caveats: extsvc.CheckFolderEliteAccessCaveats{
			MinScore: &extsvc.MinScoreArgs{MinRequired: new(0.5), Current: new(0.3)},
		},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

// --- Caveat × Exclusion ---
//
// `permission scored_minus_token = scored_viewer - token_viewer` —
// grants when user is in scored_viewer (caveat satisfied) AND NOT in
// token_viewer. Both legs' caveats are collected by walkPermCaveats
// (SPEC-001: intersection/exclusion treated as union for caveat reach).

func TestFolder_ScoredMinusToken_GrantsWhenInScoredOnly(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("smt-only").CreateScoredViewerRelations(ctx, extsvc.FolderScoredViewerObjects{
		User: []extsvc.User{"u-smt-only"},
	}))
	// Deliberately NOT writing to token_viewer

	ok, err := extsvc.Folder("smt-only").CheckScoredMinusToken(ctx, extsvc.CheckFolderScoredMinusTokenInputs{
		User: []extsvc.User{"u-smt-only"},
		Caveats: extsvc.CheckFolderScoredMinusTokenCaveats{
			MinScore: &extsvc.MinScoreArgs{MinRequired: new(0.5), Current: new(0.9)},
		},
	})
	require.NoError(t, err)
	assert.True(t, ok, "exclusion + caveat: left leg holds, right leg has no tuple → grant")
}

func TestFolder_ScoredMinusToken_DeniesWhenInBoth(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("smt-both").CreateScoredViewerRelations(ctx, extsvc.FolderScoredViewerObjects{
		User: []extsvc.User{"u-smt-both"},
	}))
	require.NoError(t, extsvc.Folder("smt-both").CreateTokenViewerRelations(ctx, extsvc.FolderTokenViewerObjects{
		User: []extsvc.User{"u-smt-both"},
		Caveats: extsvc.FolderTokenViewerCaveats{
			User: &extsvc.HasTokenArgs{Token: []byte("opaque")},
		},
	}))

	// User holds both legs with caveats satisfied → exclusion excludes → deny
	_, err := extsvc.Folder("smt-both").CheckScoredMinusToken(ctx, extsvc.CheckFolderScoredMinusTokenInputs{
		User: []extsvc.User{"u-smt-both"},
		Caveats: extsvc.CheckFolderScoredMinusTokenCaveats{
			MinScore: &extsvc.MinScoreArgs{MinRequired: new(0.5), Current: new(0.9)},
		},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied, "exclusion + caveat: both legs grant → exclude → deny")
}

// AUZ-007 ext — within-a-single-caveat partial binding via per-field
// pointers. The within_window caveat has two parameters
// (allowed_actions: list<string>, requested_action: string). Pre-bind
// allowed_actions as POLICY at write time, defer requested_action as
// REQUEST DATA to check time. Per SPEC-003 A6, SpiceDB merges write +
// check per-tuple per-key — write supplies allowed_actions, check
// supplies requested_action, eval evaluates against the union.
//
// Without per-field pointers (the pre-AUZ-007-ext-pointers state), the
// typed Args struct would force RequestedAction to its zero value ""
// at write time, which write-time-precedence would lock in, causing
// any check to evaluate against "" → false → deny.
//
// With per-field pointers: omitting RequestedAction in the write-time
// Args (leaving it nil) causes the codegen to omit the wire key
// entirely. Check-time then supplies it; the merged context contains
// both keys; eval succeeds.
func TestFolder_Act_PartialBindWithinSingleCaveat_Grants(t *testing.T) {
	ctx := context.Background()

	// Write: AllowedActions pre-bound (policy), RequestedAction left nil.
	require.NoError(t, extsvc.Folder("act-partial").CreateActorRelations(ctx, extsvc.FolderActorObjects{
		User: []extsvc.User{"u-act-partial"},
		Caveats: extsvc.FolderActorCaveats{
			User: &extsvc.WithinWindowArgs{
				AllowedActions: []string{"read", "write"},
				// RequestedAction left nil — deferred to check time
			},
		},
	}))

	// Check: supplies RequestedAction (request data). Merged context is
	// {allowed_actions: [...], requested_action: "read"}. Eval:
	// "read" in ["read", "write"] → true → grant.
	ok, err := extsvc.Folder("act-partial").CheckAct(ctx, extsvc.CheckFolderActInputs{
		User: []extsvc.User{"u-act-partial"},
		Caveats: extsvc.CheckFolderActCaveats{
			WithinWindow: &extsvc.WithinWindowArgs{
				RequestedAction: new("read"),
				// AllowedActions left nil — pre-bound at write time
			},
		},
	})
	require.NoError(t, err)
	assert.True(t, ok, "partial binding within a single caveat: write supplies one key, check supplies the other, merge produces eval-ready context")
}

// Same setup but check supplies a RequestedAction NOT in AllowedActions
// → eval false → deny. Confirms the merge actually flows the policy
// value from write through to eval.
func TestFolder_Act_PartialBind_DeniesWhenActionNotPolicyAllowed(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("act-partial-no").CreateActorRelations(ctx, extsvc.FolderActorObjects{
		User: []extsvc.User{"u-act-partial-no"},
		Caveats: extsvc.FolderActorCaveats{
			User: &extsvc.WithinWindowArgs{
				AllowedActions: []string{"read"},
			},
		},
	}))

	_, err := extsvc.Folder("act-partial-no").CheckAct(ctx, extsvc.CheckFolderActInputs{
		User: []extsvc.User{"u-act-partial-no"},
		Caveats: extsvc.CheckFolderActCaveats{
			WithinWindow: &extsvc.WithinWindowArgs{
				RequestedAction: new("delete"),
			},
		},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied, "delete not in [read] → eval false → deny")
}

// AUZ-008 — Lookup with caveat context + CONDITIONAL filter.
//
// Schema: tenanted_viewer: extsvc/user with extsvc/tenant_match,
// permission tenanted_browse = tenanted_viewer.
// Generated:
//   - LookupTenantedBrowseFolderResources(ctx, CheckFolderTenantedBrowseInputs)
//   - (folder).LookupTenantedBrowseUserSubjects(ctx, CheckFolderTenantedBrowseCaveats)
// Engine routes through LookupResourcesWithCaveat / LookupSubjectsWithCaveat;
// streamed responses with Permissionship != HAS_PERMISSION are filtered out.

func TestFolder_LookupTenantedBrowseUserSubjects_GrantedWithCaveat(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("lk-s-ok").CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-lk-s-ok"},
	}))

	users, err := extsvc.Folder("lk-s-ok").LookupTenantedBrowseUserSubjects(ctx, extsvc.CheckFolderTenantedBrowseCaveats{
		TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("acme")},
	})
	require.NoError(t, err)
	assert.Contains(t, users.Definite, extsvc.User("u-lk-s-ok"), "matching tenant must surface as definite grant")
}

func TestFolder_LookupTenantedBrowseFolderResources_GrantedWithCaveat(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("lk-r-ok").CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-lk-r-ok"},
	}))

	folders, err := extsvc.LookupTenantedBrowseFolderResources(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User: []extsvc.User{"u-lk-r-ok"},
		Caveats: extsvc.CheckFolderTenantedBrowseCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, folders.Definite, extsvc.Folder("lk-r-ok"), "resource lookup with matching caveat returns the folder")
}

// Pre-AUZ-008, an empty Caveats input would cause SpiceDB to return
// CONDITIONAL_PERMISSION; the engine appended every response ID
// regardless, so the user appeared in the result even though access
// wasn't actually granted. Post-AUZ-008, the filter drops CONDITIONAL
// from the .Definite slice. Post-AUZ-013, the conditional row surfaces
// in .Conditional with MissingKeys populated for caller recovery.
func TestFolder_LookupTenantedBrowseUserSubjects_ConditionalFiltered(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("lk-s-cond").CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-lk-s-cond"},
	}))

	// No Caveats supplied — SpiceDB returns CONDITIONAL because `tenant`
	// has nothing to bind. Filter drops it from .Definite.
	users, err := extsvc.Folder("lk-s-cond").LookupTenantedBrowseUserSubjects(ctx, extsvc.CheckFolderTenantedBrowseCaveats{})
	require.NoError(t, err)
	assert.NotContains(t, users.Definite, extsvc.User("u-lk-s-cond"), "CONDITIONAL_PERMISSION must be filtered out of .Definite")
}

// AUZ-013 — conditional Lookup rows surface in .Conditional with MissingKeys.

func TestFolder_LookupTenantedBrowseUserSubjects_ConditionalSurfacedWithMissingKeys(t *testing.T) {
	ctx := context.Background()

	// Defer the caveat at write — caller will omit context at Lookup.
	require.NoError(t, extsvc.Folder("lk-s-cond-rich").CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-lk-s-cond-rich"},
	}))

	result, err := extsvc.Folder("lk-s-cond-rich").LookupTenantedBrowseUserSubjects(ctx, extsvc.CheckFolderTenantedBrowseCaveats{})
	require.NoError(t, err)

	assert.Empty(t, result.Definite, "no caveat context → no definite grants")
	require.Len(t, result.Conditional, 1, "the user's row surfaces as conditional")
	assert.Equal(t, extsvc.User("u-lk-s-cond-rich"), result.Conditional[0].ID)
	assert.Contains(t, result.Conditional[0].MissingKeys, "tenant",
		"PartialCaveatInfo.MissingRequiredContext surfaces the caveat key the caller forgot")
}

func TestFolder_LookupTenantedBrowseFolderResources_ConditionalSurfacedWithMissingKeys(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("lk-r-cond-rich").CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-lk-r-cond-rich"},
	}))

	result, err := extsvc.LookupTenantedBrowseFolderResources(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User: []extsvc.User{"u-lk-r-cond-rich"},
		// Caveats omitted — tenant unbound
	})
	require.NoError(t, err)

	assert.Empty(t, result.Definite)
	require.Len(t, result.Conditional, 1)
	assert.Equal(t, extsvc.Folder("lk-r-cond-rich"), result.Conditional[0].ID)
	assert.Contains(t, result.Conditional[0].MissingKeys, "tenant")
}

func TestFolder_LookupTenantedBrowseFolderResources_HardDenyEmptyConditional(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("lk-r-hard").CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-lk-r-hard"},
	}))

	// Wrong tenant — CEL false → NO_PERMISSION, not CONDITIONAL.
	// SpiceDB never streams the row to begin with; both slices empty.
	result, err := extsvc.LookupTenantedBrowseFolderResources(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User: []extsvc.User{"u-lk-r-hard"},
		Caveats: extsvc.CheckFolderTenantedBrowseCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("not-acme")},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, result.Definite, "CEL false → not in Definite")
	assert.Empty(t, result.Conditional, "CEL false ≠ indeterminate; not in Conditional either")
}

func TestFolder_LookupTenantedBrowseUserSubjects_MixedDefiniteAndConditional(t *testing.T) {
	ctx := context.Background()

	folder := extsvc.Folder("lk-s-mixed")

	// User A — pre-bind tenant=acme at write (definite when looked up without caveat input).
	require.NoError(t, folder.CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-mixed-def"},
		Caveats: extsvc.FolderTenantedViewerCaveats{
			User: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
	}))

	// User B — defer the caveat at write (conditional when no caveat input).
	require.NoError(t, folder.CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-mixed-cond"},
	}))

	// Lookup without caveat input — SpiceDB evaluates per row:
	//   pre-bound row → CEL eval succeeds with the stored value → HAS_PERMISSION
	//   deferred row → tenant unbound → CONDITIONAL_PERMISSION
	result, err := folder.LookupTenantedBrowseUserSubjects(ctx, extsvc.CheckFolderTenantedBrowseCaveats{})
	require.NoError(t, err)

	assert.Contains(t, result.Definite, extsvc.User("u-mixed-def"),
		"pre-bound caveat → definite grant when context unspecified")
	require.Len(t, result.Conditional, 1)
	assert.Equal(t, extsvc.User("u-mixed-cond"), result.Conditional[0].ID,
		"deferred caveat → conditional entry with the user's ID")
	assert.Contains(t, result.Conditional[0].MissingKeys, "tenant")
}

// Lookup with a non-matching caveat: SpiceDB evaluates the expression
// to false → NO_PERMISSION → never streamed in the first place. Equivalent
// observable behavior as CONDITIONAL filtering for the caller.
func TestFolder_LookupTenantedBrowseFolderResources_WrongCaveatFiltered(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, extsvc.Folder("lk-r-wrong").CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-lk-r-wrong"},
	}))

	folders, err := extsvc.LookupTenantedBrowseFolderResources(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User: []extsvc.User{"u-lk-r-wrong"},
		Caveats: extsvc.CheckFolderTenantedBrowseCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("not-acme")},
		},
	})
	require.NoError(t, err)
	assert.NotContains(t, folders.Definite, extsvc.Folder("lk-r-wrong"), "mismatched caveat must not surface the folder")
}

// AUZ-009 — `with expiration` per-tuple TTL.
//
// Schema:
//   relation expiring_viewer: extsvc/user with expiration
//   permission expiring_browse = expiring_viewer
//   relation gated_token: extsvc/user with extsvc/tenant_match and expiration
//   permission gated_token_check = gated_token
//
// Generated `Create*Relations` route through Engine.CreateRelationsWithExpiration
// (OPERATION_TOUCH, OptionalExpiresAt set, optional OptionalCaveat).
// SpiceDB filters expired tuples server-side from Check / Lookup / Read.

func TestFolder_ExpiringBrowse_GrantsBeforeExpiry(t *testing.T) {
	ctx := context.Background()

	expiresAt := time.Now().Add(1 * time.Hour)
	require.NoError(t, extsvc.Folder("exp-ok").CreateExpiringViewerRelations(ctx, extsvc.FolderExpiringViewerObjects{
		User: []extsvc.User{"u-exp-ok"},
		Expirations: extsvc.FolderExpiringViewerExpirations{
			User: &expiresAt,
		},
	}))

	ok, err := extsvc.Folder("exp-ok").CheckExpiringBrowse(ctx, extsvc.CheckFolderExpiringBrowseInputs{
		User: []extsvc.User{"u-exp-ok"},
	})
	require.NoError(t, err)
	assert.True(t, ok, "tuple within TTL window must grant")
}

func TestFolder_ExpiringBrowse_DeniesAfterExpiry(t *testing.T) {
	ctx := context.Background()

	// Short TTL — 100ms in the future.
	expiresAt := time.Now().Add(100 * time.Millisecond)
	require.NoError(t, extsvc.Folder("exp-no").CreateExpiringViewerRelations(ctx, extsvc.FolderExpiringViewerObjects{
		User: []extsvc.User{"u-exp-no"},
		Expirations: extsvc.FolderExpiringViewerExpirations{
			User: &expiresAt,
		},
	}))

	// Sleep past the expiration; SpiceDB filters at evaluation time
	// (no GC wait needed for filtering).
	time.Sleep(200 * time.Millisecond)

	_, err := extsvc.Folder("exp-no").CheckExpiringBrowse(ctx, extsvc.CheckFolderExpiringBrowseInputs{
		User: []extsvc.User{"u-exp-no"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied, "expired tuple must be filtered server-side")
}

func TestFolder_GatedToken_GrantsWhenCaveatAndExpiryHold(t *testing.T) {
	ctx := context.Background()

	expiresAt := time.Now().Add(1 * time.Hour)
	require.NoError(t, extsvc.Folder("gt-ok").CreateGatedTokenRelations(ctx, extsvc.FolderGatedTokenObjects{
		User: []extsvc.User{"u-gt-ok"},
		Caveats: extsvc.FolderGatedTokenCaveats{
			User: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
		Expirations: extsvc.FolderGatedTokenExpirations{
			User: &expiresAt,
		},
	}))

	ok, err := extsvc.Folder("gt-ok").CheckGatedTokenCheck(ctx, extsvc.CheckFolderGatedTokenCheckInputs{
		User: []extsvc.User{"u-gt-ok"},
		Caveats: extsvc.CheckFolderGatedTokenCheckCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
	})
	require.NoError(t, err)
	assert.True(t, ok, "both gates passed: caveat eval true AND not yet expired")
}

func TestFolder_GatedToken_DeniesWhenCaveatFailsEvenIfNotExpired(t *testing.T) {
	ctx := context.Background()

	// Defer the caveat at write time (Caveats sub-struct empty) so the
	// check-time tenant value reaches eval. Per SPEC-003 A6 — write-time
	// values would otherwise win on key collision; defer gives the
	// caller's check-time value priority for unbound keys.
	expiresAt := time.Now().Add(1 * time.Hour)
	require.NoError(t, extsvc.Folder("gt-cv-no").CreateGatedTokenRelations(ctx, extsvc.FolderGatedTokenObjects{
		User: []extsvc.User{"u-gt-cv-no"},
		Expirations: extsvc.FolderGatedTokenExpirations{
			User: &expiresAt,
		},
	}))

	// Check supplies wrong tenant — eval `"not-acme" == "acme"` → false.
	// Tuple is not yet expired, so the deny here is purely caveat-driven.
	_, err := extsvc.Folder("gt-cv-no").CheckGatedTokenCheck(ctx, extsvc.CheckFolderGatedTokenCheckInputs{
		User: []extsvc.User{"u-gt-cv-no"},
		Caveats: extsvc.CheckFolderGatedTokenCheckCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("not-acme")},
		},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied, "caveat eval false → deny, expiration irrelevant")
}

// SPEC-004 A2: TOUCH allows over-write of an expired-but-not-yet-GC'd
// tuple at the same identity. CREATE would error in that window. The
// codegen routes expiring writes through CreateRelationsWithExpiration
// which hard-codes OPERATION_TOUCH, so a re-write succeeds and the
// expiration is refreshed.
func TestFolder_ExpiringBrowse_TouchAllowsRewriteAfterExpiry(t *testing.T) {
	ctx := context.Background()

	// First write: short TTL.
	shortExpiry := time.Now().Add(100 * time.Millisecond)
	require.NoError(t, extsvc.Folder("exp-touch").CreateExpiringViewerRelations(ctx, extsvc.FolderExpiringViewerObjects{
		User: []extsvc.User{"u-exp-touch"},
		Expirations: extsvc.FolderExpiringViewerExpirations{
			User: &shortExpiry,
		},
	}))

	// Sleep past expiration. Tuple is filtered but storage still holds
	// the row (un-GC'd). CREATE would fail at this point; TOUCH succeeds.
	time.Sleep(200 * time.Millisecond)

	// Second write: longer TTL. Same tuple identity, refreshes expiration.
	longExpiry := time.Now().Add(1 * time.Hour)
	require.NoError(t, extsvc.Folder("exp-touch").CreateExpiringViewerRelations(ctx, extsvc.FolderExpiringViewerObjects{
		User: []extsvc.User{"u-exp-touch"},
		Expirations: extsvc.FolderExpiringViewerExpirations{
			User: &longExpiry,
		},
	}))

	ok, err := extsvc.Folder("exp-touch").CheckExpiringBrowse(ctx, extsvc.CheckFolderExpiringBrowseInputs{
		User: []extsvc.User{"u-exp-touch"},
	})
	require.NoError(t, err)
	assert.True(t, ok, "TOUCH-after-expiry refresh should grant — verifies SPEC-004 A2")
}

// AUZ-009.1 — wildcard + expiration ("public for users but in time").
//
// Schema:
//   relation public_until: extsvc/user:* with expiration
//   permission public_browse = public_until
//
// Wildcard branch routes through CreateRelationsWithExpiration with
// authz.WildcardID as the ID sentinel; SpiceDB filters expired wildcard
// tuples server-side identically to concrete-subject tuples.

func TestFolder_PublicBrowse_WildcardGrantsBeforeExpiry(t *testing.T) {
	ctx := context.Background()

	expiresAt := time.Now().Add(1 * time.Hour)
	require.NoError(t, extsvc.Folder("pub-ok").CreatePublicUntilRelations(ctx, extsvc.FolderPublicUntilObjects{
		Wildcards: extsvc.FolderPublicUntilWildcards{User: true},
		Expirations: extsvc.FolderPublicUntilExpirations{
			User: &expiresAt,
		},
	}))

	// Any concrete user resolves through the wildcard within TTL.
	ok, err := extsvc.Folder("pub-ok").CheckPublicBrowse(ctx, extsvc.CheckFolderPublicBrowseInputs{
		User: []extsvc.User{"u-anyone"},
	})
	require.NoError(t, err)
	assert.True(t, ok, "wildcard tuple within TTL must grant any user")
}

func TestFolder_PublicBrowse_WildcardDeniesAfterExpiry(t *testing.T) {
	ctx := context.Background()

	// Short TTL — sleep past it.
	expiresAt := time.Now().Add(100 * time.Millisecond)
	require.NoError(t, extsvc.Folder("pub-no").CreatePublicUntilRelations(ctx, extsvc.FolderPublicUntilObjects{
		Wildcards: extsvc.FolderPublicUntilWildcards{User: true},
		Expirations: extsvc.FolderPublicUntilExpirations{
			User: &expiresAt,
		},
	}))

	time.Sleep(200 * time.Millisecond)

	_, err := extsvc.Folder("pub-no").CheckPublicBrowse(ctx, extsvc.CheckFolderPublicBrowseInputs{
		User: []extsvc.User{"u-anyone"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied, "expired wildcard tuple must be filtered like concrete tuples")
}

// AUZ-009.1 — triple: wildcard + caveat + expiration.
//
// Schema:
//   relation public_gated: extsvc/user:* with extsvc/tenant_match and expiration
//   permission public_gated_check = public_gated
//
// All three flags compose: wildcard subject, request-time caveat eval,
// per-tuple TTL. Verifies the codegen wires Caveats AND Expirations
// sub-structs together on the wildcard branch.

func TestFolder_PublicGated_WildcardAndCaveatAndExpiration(t *testing.T) {
	ctx := context.Background()

	expiresAt := time.Now().Add(1 * time.Hour)
	require.NoError(t, extsvc.Folder("pgt-ok").CreatePublicGatedRelations(ctx, extsvc.FolderPublicGatedObjects{
		Wildcards: extsvc.FolderPublicGatedWildcards{User: true},
		Caveats: extsvc.FolderPublicGatedCaveats{
			User: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
		Expirations: extsvc.FolderPublicGatedExpirations{
			User: &expiresAt,
		},
	}))

	// Caveat passes (write-time tenant=acme), within TTL — grant.
	ok, err := extsvc.Folder("pgt-ok").CheckPublicGatedCheck(ctx, extsvc.CheckFolderPublicGatedCheckInputs{
		User: []extsvc.User{"u-any"},
	})
	require.NoError(t, err)
	assert.True(t, ok, "wildcard + matching caveat + within TTL must grant")
}

func TestFolder_PublicGated_WildcardCaveatFailsEvenIfNotExpired(t *testing.T) {
	ctx := context.Background()

	// Defer the caveat at write — check-time tenant value reaches eval.
	expiresAt := time.Now().Add(1 * time.Hour)
	require.NoError(t, extsvc.Folder("pgt-no").CreatePublicGatedRelations(ctx, extsvc.FolderPublicGatedObjects{
		Wildcards: extsvc.FolderPublicGatedWildcards{User: true},
		Expirations: extsvc.FolderPublicGatedExpirations{
			User: &expiresAt,
		},
	}))

	// Wrong tenant at check — caveat eval false → deny, despite valid TTL.
	_, err := extsvc.Folder("pgt-no").CheckPublicGatedCheck(ctx, extsvc.CheckFolderPublicGatedCheckInputs{
		User: []extsvc.User{"u-any"},
		Caveats: extsvc.CheckFolderPublicGatedCheckCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("not-acme")},
		},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied, "caveat fail on wildcard branch denies regardless of TTL")
}

// AUZ-010 — Read with metadata. SPEC-005.
//
// Verifies the new metadata fields on <Rel><Type>Relation actually populate
// from the SpiceDB ReadRelationships response — not just compile clean.

func TestFolder_ReadViewerUser_NonTraitedTuple_HasNilMetadata(t *testing.T) {
	ctx := context.Background()

	f := extsvc.Folder("read-meta-plain")
	require.NoError(t, f.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"u-plain"},
	}))

	rels, err := f.ReadViewerUserRelations(ctx)
	require.NoError(t, err)
	require.Len(t, rels, 1)
	assert.Equal(t, extsvc.User("u-plain"), rels[0].ID)
	assert.Equal(t, "", rels[0].CaveatName, "non-traited tuple → empty CaveatName")
	assert.Nil(t, rels[0].CaveatContext, "non-traited tuple → nil CaveatContext")
	assert.Nil(t, rels[0].ExpiresAt, "non-traited tuple → nil ExpiresAt")
}

func TestFolder_ReadTenantedViewerUser_CaveatedTuple_PopulatesCaveatFields(t *testing.T) {
	ctx := context.Background()

	f := extsvc.Folder("read-meta-cav")
	require.NoError(t, f.CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-cav"},
		Caveats: extsvc.FolderTenantedViewerCaveats{
			User: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
	}))

	rels, err := f.ReadTenantedViewerUserRelations(ctx)
	require.NoError(t, err)
	require.Len(t, rels, 1)
	assert.Equal(t, extsvc.User("u-cav"), rels[0].ID)
	assert.Equal(t, "extsvc/tenant_match", rels[0].CaveatName, "caveat name surfaces from OptionalCaveat")
	require.NotNil(t, rels[0].CaveatContext, "pre-context should decode to map")
	assert.Equal(t, "acme", rels[0].CaveatContext["tenant"], "pre-context value travels through structpb round-trip")
	assert.Nil(t, rels[0].ExpiresAt)
}

func TestFolder_ReadExpiringViewerUser_ExpiringTuple_PopulatesExpiresAt(t *testing.T) {
	ctx := context.Background()

	f := extsvc.Folder("read-meta-exp")
	expiresAt := time.Now().Add(1 * time.Hour)
	require.NoError(t, f.CreateExpiringViewerRelations(ctx, extsvc.FolderExpiringViewerObjects{
		User: []extsvc.User{"u-exp"},
		Expirations: extsvc.FolderExpiringViewerExpirations{
			User: &expiresAt,
		},
	}))

	rels, err := f.ReadExpiringViewerUserRelations(ctx)
	require.NoError(t, err)
	require.Len(t, rels, 1)
	assert.Equal(t, extsvc.User("u-exp"), rels[0].ID)
	assert.Equal(t, "", rels[0].CaveatName)
	require.NotNil(t, rels[0].ExpiresAt, "expiring tuple → ExpiresAt populated")
	assert.WithinDuration(t, expiresAt, *rels[0].ExpiresAt, 2*time.Second, "stored expiry within ±2s of write timestamp")
}

func TestFolder_ReadGatedTokenUser_CombinedTrait_PopulatesBoth(t *testing.T) {
	ctx := context.Background()

	f := extsvc.Folder("read-meta-both")
	expiresAt := time.Now().Add(1 * time.Hour)
	require.NoError(t, f.CreateGatedTokenRelations(ctx, extsvc.FolderGatedTokenObjects{
		User: []extsvc.User{"u-both"},
		Caveats: extsvc.FolderGatedTokenCaveats{
			User: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
		Expirations: extsvc.FolderGatedTokenExpirations{
			User: &expiresAt,
		},
	}))

	rels, err := f.ReadGatedTokenUserRelations(ctx)
	require.NoError(t, err)
	require.Len(t, rels, 1)
	assert.Equal(t, "extsvc/tenant_match", rels[0].CaveatName, "combined trait — caveat name populates")
	assert.Equal(t, "acme", rels[0].CaveatContext["tenant"], "combined trait — caveat context populates")
	require.NotNil(t, rels[0].ExpiresAt, "combined trait — ExpiresAt populates")
	assert.WithinDuration(t, expiresAt, *rels[0].ExpiresAt, 2*time.Second)
}

func TestFolder_ReadGuardedViewerUserWildcard_PopulatesMetadata(t *testing.T) {
	ctx := context.Background()

	// Wildcard + caveat — pre-bind tenant=acme at write, then read the wildcard
	// tuple's metadata.
	f := extsvc.Folder("read-meta-wild")
	require.NoError(t, f.CreateGuardedViewerRelations(ctx, extsvc.FolderGuardedViewerObjects{
		Wildcards: extsvc.FolderGuardedViewerWildcards{User: true},
		Caveats: extsvc.FolderGuardedViewerCaveats{
			User: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
	}))

	meta, present, err := f.ReadGuardedViewerUserWildcard(ctx)
	require.NoError(t, err)
	require.True(t, present, "wildcard tuple is present")
	assert.Equal(t, extsvc.User(authz.WildcardID), meta.ID, "wildcard ID surfaces as the WildcardID sentinel")
	assert.Equal(t, "extsvc/tenant_match", meta.CaveatName, "wildcard tuple carries caveat metadata")
	require.NotNil(t, meta.CaveatContext)
	assert.Equal(t, "acme", meta.CaveatContext["tenant"])
}

func TestFolder_IDsOf_RoundTripEquivalent(t *testing.T) {
	ctx := context.Background()

	f := extsvc.Folder("read-meta-ids")
	require.NoError(t, f.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"u-ids-a", "u-ids-b"},
	}))

	rels, err := f.ReadViewerUserRelations(ctx)
	require.NoError(t, err)
	ids := authz.IDsOf(rels)
	assert.ElementsMatch(t, []extsvc.User{"u-ids-a", "u-ids-b"}, ids,
		"IDsOf projects the same IDs the pre-AUZ-010 API would have returned")
}

// AUZ-014 — consistency mode opt-in.
//
// SpiceDB's CheckPermission honors a Consistency field on the wire. The
// engine hardcodes a time-based policy (AtExactSnapshot post-write, nil
// otherwise). For security-sensitive checks where stale reads are
// unacceptable, callers can opt into ConsistencyFullyConsistent via
// authz.WithConsistency(ctx, mode).
//
// Note: AUZ-011 Discoveries hypothesized that AtExactSnapshot masks
// wall-clock expiration on userset tuples. Empirical re-verification
// during AUZ-014 (same fixture, same sleep timing) shows expired
// userset tuples are filtered under both default and FullyConsistent
// modes — SpiceDB enforces wall-clock expiration regardless of the
// snapshot revision pin. The AUZ-011 Discovery may have been a transient
// observation. AUZ-014's value is independent: caller-controlled per-call
// consistency override for security-sensitive workloads where the engine's
// time-based default policy isn't strong enough.

func TestFolder_TempCollab_ExpiredUserset_FullConsistencyDenies(t *testing.T) {
	// Demonstrates the override path: caller opts into FullyConsistent.
	// SpiceDB evaluates against most-up-to-date data + wall-clock;
	// expired userset tuple is filtered, Check denies.
	ctx := context.Background()

	team := extsvc.Team("t-cm-full")
	user := extsvc.User("u-cm-full")
	folder := extsvc.Folder("f-cm-full")

	require.NoError(t, team.CreateOwnerRelations(ctx, extsvc.TeamOwnerObjects{
		User: []extsvc.User{user},
	}))

	expiresAt := time.Now().Add(150 * time.Millisecond)
	require.NoError(t, folder.CreateTempCollabRelations(ctx, extsvc.FolderTempCollabObjects{
		TeamAdmin: []extsvc.Team{team},
		Expirations: extsvc.FolderTempCollabExpirations{
			TeamAdmin: &expiresAt,
		},
	}))

	time.Sleep(250 * time.Millisecond)

	// Override — security-sensitive caller opts out of cached snapshot.
	freshCtx := authz.WithConsistency(ctx, authz.ConsistencyFullyConsistent)
	_, err := folder.CheckTempCollabView(freshCtx, extsvc.CheckFolderTempCollabViewInputs{
		TeamAdmin: []extsvc.Team{team},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied,
		"full consistency: wall-clock evaluation filters expired userset tuple → ErrPermissionDenied (closes AUZ-011 artifact)")
}

func TestFolder_ExpiringBrowse_FullConsistencyDeniesAfterExpiry(t *testing.T) {
	// Direct-subject expiration — sanity check that the override path
	// works for the simpler case too. AUZ-009 already enforces this
	// under default consistency for direct subjects, but exercise the
	// override branch explicitly to verify it composes.
	ctx := context.Background()

	folder := extsvc.Folder("f-cm-direct")
	expiresAt := time.Now().Add(100 * time.Millisecond)
	require.NoError(t, folder.CreateExpiringViewerRelations(ctx, extsvc.FolderExpiringViewerObjects{
		User: []extsvc.User{"u-cm-direct"},
		Expirations: extsvc.FolderExpiringViewerExpirations{
			User: &expiresAt,
		},
	}))

	time.Sleep(200 * time.Millisecond)

	freshCtx := authz.WithConsistency(ctx, authz.ConsistencyFullyConsistent)
	_, err := folder.CheckExpiringBrowse(freshCtx, extsvc.CheckFolderExpiringBrowseInputs{
		User: []extsvc.User{"u-cm-direct"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied,
		"full consistency on direct-subject expiring tuple denies after wall-clock expiry")
}

func TestFolder_FullConsistency_NonExpiringTuple_StillGrants(t *testing.T) {
	// Override doesn't break the happy path — a regular non-expiring
	// tuple still grants when reached via FullyConsistent evaluation.
	ctx := context.Background()

	folder := extsvc.Folder("f-cm-happy")
	require.NoError(t, folder.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"u-cm-happy"},
	}))

	freshCtx := authz.WithConsistency(ctx, authz.ConsistencyFullyConsistent)
	ok, err := folder.CheckBrowse(freshCtx, extsvc.CheckFolderBrowseInputs{
		User: []extsvc.User{"u-cm-happy"},
	})
	require.NoError(t, err)
	assert.True(t, ok, "FullyConsistent on a fresh non-expiring tuple grants normally")
}

func TestArticle_LookupAuthorOnlyUserSubjects(t *testing.T) {
	ctx := context.Background()

	art := extsvc.Article("art8")
	require.NoError(t, art.CreateAuthorRelations(ctx, extsvc.ArticleAuthorObjects{
		User: []extsvc.User{"au8"},
	}))

	ids, err := art.LookupAuthorOnlyUserSubjects(ctx)
	require.NoError(t, err)
	assert.Contains(t, ids.Definite, extsvc.User("au8"))
}

// AUZ-011 — sub-relation references (`team#admin`).
//
// Schema:
//   relation collab: extsvc/team#admin
//   permission collab_view = collab
//   relation mixed_view: extsvc/user | extsvc/team#admin
//   relation gated_collab: extsvc/team#admin with extsvc/tenant_match
//   relation temp_collab: extsvc/team#admin with expiration

func TestFolder_Collab_WriteUsersetThenRead_SubRelationPopulates(t *testing.T) {
	ctx := context.Background()

	team := extsvc.Team("t-collab-rd")
	folder := extsvc.Folder("f-collab-rd")

	require.NoError(t, folder.CreateCollabRelations(ctx, extsvc.FolderCollabObjects{
		TeamAdmin: []extsvc.Team{team},
	}))

	rels, err := folder.ReadCollabTeamRelations(ctx)
	require.NoError(t, err)
	require.Len(t, rels, 1)
	assert.Equal(t, team, rels[0].ID)
	assert.Equal(t, "admin", rels[0].SubRelation, "userset row carries the sub-relation tag")
}

func TestFolder_CollabView_LiteralUsersetCheck(t *testing.T) {
	ctx := context.Background()

	team := extsvc.Team("t-lit")
	folder := extsvc.Folder("f-lit")

	require.NoError(t, folder.CreateCollabRelations(ctx, extsvc.FolderCollabObjects{
		TeamAdmin: []extsvc.Team{team},
	}))

	// Rare case — userset-as-subject Check; SpiceDB matches the literal
	// userset reference, no recursive expansion (per SPEC-006 A2).
	ok, err := folder.CheckCollabView(ctx, extsvc.CheckFolderCollabViewInputs{
		TeamAdmin: []extsvc.Team{team},
	})
	require.NoError(t, err)
	assert.True(t, ok, "literal userset reference matches the granted tuple")
}

func TestFolder_CollabView_MismatchedTeamUsersetDenies(t *testing.T) {
	ctx := context.Background()

	teamGranted := extsvc.Team("t-granted")
	teamOther := extsvc.Team("t-other")
	folder := extsvc.Folder("f-mis")

	require.NoError(t, folder.CreateCollabRelations(ctx, extsvc.FolderCollabObjects{
		TeamAdmin: []extsvc.Team{teamGranted},
	}))

	// Different team's admin set is NOT granted — literal userset mismatch.
	_, err := folder.CheckCollabView(ctx, extsvc.CheckFolderCollabViewInputs{
		TeamAdmin: []extsvc.Team{teamOther},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied,
		"literal userset reference does not match a different team's admin set")
}

func TestFolder_MixedView_DirectAndUsersetReadDisjoint(t *testing.T) {
	ctx := context.Background()

	user := extsvc.User("u-mix")
	team := extsvc.Team("t-mix")
	folder := extsvc.Folder("f-mix")

	require.NoError(t, folder.CreateMixedViewRelations(ctx, extsvc.FolderMixedViewObjects{
		User:      []extsvc.User{user},
		TeamAdmin: []extsvc.Team{team},
	}))

	userRels, err := folder.ReadMixedViewUserRelations(ctx)
	require.NoError(t, err)
	require.Len(t, userRels, 1)
	assert.Equal(t, user, userRels[0].ID)
	assert.Equal(t, "", userRels[0].SubRelation, "direct user row has no sub-relation")

	teamRels, err := folder.ReadMixedViewTeamRelations(ctx)
	require.NoError(t, err)
	require.Len(t, teamRels, 1)
	assert.Equal(t, team, teamRels[0].ID)
	assert.Equal(t, "admin", teamRels[0].SubRelation)
}

func TestFolder_GatedCollab_UsersetWithCaveat(t *testing.T) {
	ctx := context.Background()

	team := extsvc.Team("t-gated")
	user := extsvc.User("u-gated")
	folder := extsvc.Folder("f-gated")

	require.NoError(t, team.CreateOwnerRelations(ctx, extsvc.TeamOwnerObjects{
		User: []extsvc.User{user},
	}))
	require.NoError(t, folder.CreateGatedCollabRelations(ctx, extsvc.FolderGatedCollabObjects{
		TeamAdmin: []extsvc.Team{team},
		Caveats: extsvc.FolderGatedCollabCaveats{
			TeamAdmin: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
	}))

	ok, err := folder.CheckGatedCollabView(ctx, extsvc.CheckFolderGatedCollabViewInputs{
		TeamAdmin: []extsvc.Team{team},
	})
	require.NoError(t, err)
	assert.True(t, ok, "userset + matching caveat pre-bound at write grants")
}

func TestFolder_TempCollab_UsersetWithExpiration(t *testing.T) {
	ctx := context.Background()

	team := extsvc.Team("t-temp")
	user := extsvc.User("u-temp")
	folder := extsvc.Folder("f-temp")

	require.NoError(t, team.CreateOwnerRelations(ctx, extsvc.TeamOwnerObjects{
		User: []extsvc.User{user},
	}))

	// Within-TTL grant. The deny-after-expiry case is exercised by AUZ-009's
	// existing direct-subject expiration test (TestFolder_ExpiringBrowse_DeniesAfterExpiry).
	// For userset-as-subject Check, AtExactSnapshot consistency pins expiration
	// evaluation to the snapshot revision, so a tuple unexpired at write time
	// stays "live" for the literal userset match — see AUZ-011 Discoveries.
	expiresAt := time.Now().Add(1 * time.Hour)
	require.NoError(t, folder.CreateTempCollabRelations(ctx, extsvc.FolderTempCollabObjects{
		TeamAdmin: []extsvc.Team{team},
		Expirations: extsvc.FolderTempCollabExpirations{
			TeamAdmin: &expiresAt,
		},
	}))

	ok, err := folder.CheckTempCollabView(ctx, extsvc.CheckFolderTempCollabViewInputs{
		TeamAdmin: []extsvc.Team{team},
	})
	require.NoError(t, err)
	assert.True(t, ok, "userset within TTL grants")

	// Verify the tuple's metadata round-trips with the expiration timestamp.
	rels, err := folder.ReadTempCollabTeamRelations(ctx)
	require.NoError(t, err)
	require.Len(t, rels, 1)
	assert.Equal(t, "admin", rels[0].SubRelation)
	require.NotNil(t, rels[0].ExpiresAt, "userset tuple carries OptionalExpiresAt on read")
	assert.WithinDuration(t, expiresAt, *rels[0].ExpiresAt, 2*time.Second,
		"stored expiry within ±2s of write timestamp")
}

func TestFolder_NonUsersetRead_SubRelationIsEmpty(t *testing.T) {
	ctx := context.Background()

	folder := extsvc.Folder("f-nonu")
	require.NoError(t, folder.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"u-nonu"},
	}))

	rels, err := folder.ReadViewerUserRelations(ctx)
	require.NoError(t, err)
	require.Len(t, rels, 1)
	assert.Equal(t, "", rels[0].SubRelation, "direct subject row has empty SubRelation field")
}

// AUZ-011 follow-up — arrow chain reaching a userset.
//
// Schema:
//   document.parent: folder
//   document.inherited_collab = parent->mixed_browse
//   folder.mixed_view: user | team#admin
//   folder.mixed_browse = mixed_view
//   team.admin = owner + manager
//
// The chain: Check(doc, inherited_collab, user=u1) walks
//   doc.inherited_collab → doc.parent (folder) → folder.mixed_browse → mixed_view
//   → matches both direct (user u1 directly) AND team#admin tuples
//   → for team#admin tuples, expands to team.owner ∪ team.manager → matches u1
// SpiceDB walks the entire chain server-side; the codegen exposes only
// User as a Check input (the arrow walker does not surface usersets
// reachable through right-side permissions, by design — they live on a
// different resource).

func TestDocument_InheritedCollab_DirectViewerOnParent(t *testing.T) {
	ctx := context.Background()

	user := extsvc.User("u-ic-direct")
	folder := extsvc.Folder("f-ic-direct")
	doc := extsvc.Document("d-ic-direct")

	// Grant user directly as mixed_view on the parent folder.
	require.NoError(t, folder.CreateMixedViewRelations(ctx, extsvc.FolderMixedViewObjects{
		User: []extsvc.User{user},
	}))
	require.NoError(t, doc.CreateParentRelations(ctx, extsvc.DocumentParentObjects{
		Folder: []extsvc.Folder{folder},
	}))

	// Check user → walks doc.parent → folder.mixed_browse → user direct match.
	ok, err := doc.CheckInheritedCollab(ctx, extsvc.CheckDocumentInheritedCollabInputs{
		User: []extsvc.User{user},
	})
	require.NoError(t, err)
	assert.True(t, ok, "direct user on parent folder grants inherited_collab via arrow")
}

func TestDocument_InheritedCollab_UserViaArrowChainThroughUserset(t *testing.T) {
	ctx := context.Background()

	user := extsvc.User("u-ic-userset")
	team := extsvc.Team("t-ic-userset")
	folder := extsvc.Folder("f-ic-userset")
	doc := extsvc.Document("d-ic-userset")

	// Wire: u1 is owner of t1; t1#admin is granted mixed_view on f1; d1's parent is f1.
	require.NoError(t, team.CreateOwnerRelations(ctx, extsvc.TeamOwnerObjects{
		User: []extsvc.User{user},
	}))
	require.NoError(t, folder.CreateMixedViewRelations(ctx, extsvc.FolderMixedViewObjects{
		TeamAdmin: []extsvc.Team{team},
	}))
	require.NoError(t, doc.CreateParentRelations(ctx, extsvc.DocumentParentObjects{
		Folder: []extsvc.Folder{folder},
	}))

	// Check user → SpiceDB walks: doc.parent (f1) → f1.mixed_browse → mixed_view
	// → finds team:t1#admin → expands t1#admin → team.owner → finds u1.
	ok, err := doc.CheckInheritedCollab(ctx, extsvc.CheckDocumentInheritedCollabInputs{
		User: []extsvc.User{user},
	})
	require.NoError(t, err)
	assert.True(t, ok, "user reachable via arrow → userset → owner chain grants inherited_collab")
}

// AUZ-012 — rich CONDITIONAL_PERMISSION signal.
//
// SpiceDB returns Permissionship == CONDITIONAL_PERMISSION when a caveat
// reaches the chain but the request didn't supply the parameter context.
// Today the codegen collapsed CONDITIONAL → ErrPermissionDenied silently,
// dropping PartialCaveatInfo.MissingRequiredContext. SPEC-007 routes
// CONDITIONAL into a typed *ConditionalPermissionError carrying MissingKeys;
// existing callers checking errors.Is(_, ErrPermissionDenied) keep working
// via the typed error's custom Is method.
//
// Three semantic cases distinguish:
//   1. Granted        — caveat satisfied
//   2. Conditional    — caveat reachable but context missing → MissingKeys = ["tenant"]
//   3. Hard-denied    — caveat eval returned false (wrong value) → ErrPermissionDenied
//                       (NOT conditional — CEL returned false, not indeterminate)

func TestFolder_TenantedBrowse_GrantedPath_NoErrorRegression(t *testing.T) {
	ctx := context.Background()

	folder := extsvc.Folder("cps-grant")
	require.NoError(t, folder.CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-grant"},
		Caveats: extsvc.FolderTenantedViewerCaveats{
			User: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
	}))

	ok, err := folder.CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User: []extsvc.User{"u-grant"},
	})
	require.NoError(t, err)
	assert.True(t, ok, "matching pre-bound caveat grants — no behavior change vs v1.5")
}

func TestFolder_TenantedBrowse_ConditionalPath_RichSignal(t *testing.T) {
	ctx := context.Background()

	folder := extsvc.Folder("cps-cond")

	// Defer the caveat at write — Caveats sub-struct empty. Check-time
	// caller also doesn't supply tenant. SpiceDB returns CONDITIONAL
	// because the caveat is reachable but its `tenant` parameter is
	// indeterminate.
	require.NoError(t, folder.CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-cond"},
	}))

	_, err := folder.CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User: []extsvc.User{"u-cond"},
		// Caveats omitted — no tenant key supplied
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, authz.ErrConditionalPermission),
		"missing-context case matches ErrConditionalPermission sentinel")

	var cpe *authz.ConditionalPermissionError
	require.True(t, errors.As(err, &cpe), "errors.As extracts the typed error")
	assert.Contains(t, cpe.MissingKeys, "tenant",
		"PartialCaveatInfo.MissingRequiredContext surfaces the caveat key the caller forgot")
}

func TestFolder_TenantedBrowse_ConditionalPath_BackwardCompatStillMatchesPermissionDenied(t *testing.T) {
	ctx := context.Background()

	folder := extsvc.Folder("cps-bc")
	require.NoError(t, folder.CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-bc"},
	}))

	_, err := folder.CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User: []extsvc.User{"u-bc"},
	})
	require.Error(t, err)

	// Backward-compat: existing v1.5 callers checking ErrPermissionDenied
	// still see the conditional case as "denied" via the typed error's
	// custom Is method matching both targets (per SPEC-007 C2).
	assert.True(t, errors.Is(err, authz.ErrPermissionDenied),
		"conditional permission still satisfies ErrPermissionDenied for backward compat")
	assert.True(t, errors.Is(err, authz.ErrConditionalPermission),
		"and also matches the new sentinel for callers that want the rich signal")
}

func TestFolder_TenantedBrowse_HardDenyPath_NotConditional(t *testing.T) {
	ctx := context.Background()

	folder := extsvc.Folder("cps-hard")

	require.NoError(t, folder.CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
		User: []extsvc.User{"u-hard"},
	}))

	// Wrong tenant — CEL evaluates `"not-acme" == "acme"` to false.
	// SpiceDB returns NO_PERMISSION (hard deny), not CONDITIONAL.
	_, err := folder.CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User: []extsvc.User{"u-hard"},
		Caveats: extsvc.CheckFolderTenantedBrowseCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("not-acme")},
		},
	})
	require.Error(t, err)

	assert.True(t, errors.Is(err, authz.ErrPermissionDenied),
		"caveat eval false → hard deny via existing ErrPermissionDenied")
	assert.False(t, errors.Is(err, authz.ErrConditionalPermission),
		"hard deny is NOT conditional — CEL returned false, not indeterminate")

	// And errors.As to *ConditionalPermissionError must NOT succeed.
	var cpe *authz.ConditionalPermissionError
	assert.False(t, errors.As(err, &cpe),
		"the plain sentinel error is not the rich type")
}

func TestDocument_InheritedCollab_UnrelatedUserDenied(t *testing.T) {
	ctx := context.Background()

	user := extsvc.User("u-ic-no")
	team := extsvc.Team("t-ic-no")
	otherUser := extsvc.User("u-ic-other")
	folder := extsvc.Folder("f-ic-no")
	doc := extsvc.Document("d-ic-no")

	require.NoError(t, team.CreateOwnerRelations(ctx, extsvc.TeamOwnerObjects{
		User: []extsvc.User{user},
	}))
	require.NoError(t, folder.CreateMixedViewRelations(ctx, extsvc.FolderMixedViewObjects{
		TeamAdmin: []extsvc.Team{team},
	}))
	require.NoError(t, doc.CreateParentRelations(ctx, extsvc.DocumentParentObjects{
		Folder: []extsvc.Folder{folder},
	}))

	// otherUser is not owner of the team; not direct viewer of the folder.
	_, err := doc.CheckInheritedCollab(ctx, extsvc.CheckDocumentInheritedCollabInputs{
		User: []extsvc.User{otherUser},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied,
		"user outside the userset's expansion is denied through the arrow chain")
}

// AUZ-016 — Functioned tuple-to-userset (`.any()` / `.all()`).
//
// Schema:
//   relation any_parent: extsvc/folder
//   permission any_via = any_parent.any(browse)
//   relation all_parent: extsvc/folder
//   permission all_via = all_parent.all(browse)
//   relation gated_parent: extsvc/folder with extsvc/tenant_match
//   permission gated_all_via = gated_parent.all(browse)
//   relation direct_member: extsvc/user
//   permission mixed_all = direct_member + all_parent.all(browse)
//
// `.any()` is semantically equivalent to a regular arrow — verified for
// regression. `.all()` enforces strict-intersection: subject must reach
// `browse` via EVERY parent row. Combinations: caveated LeftRel,
// mixed-expression with regular identifier.

func TestFolder_AnyVia_SingleParentGrantsBrowse_Granted(t *testing.T) {
	ctx := context.Background()

	parent := extsvc.Folder("ftu-any-parent")
	folder := extsvc.Folder("ftu-any")
	user := extsvc.User("u-ftu-any")

	// Grant browse on the parent via viewer relation.
	require.NoError(t, parent.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{user},
	}))
	// Link folder → parent via any_parent.
	require.NoError(t, folder.CreateAnyParentRelations(ctx, extsvc.FolderAnyParentObjects{
		Folder: []extsvc.Folder{parent},
	}))

	ok, err := folder.CheckAnyVia(ctx, extsvc.CheckFolderAnyViaInputs{
		User: []extsvc.User{user},
	})
	require.NoError(t, err)
	assert.True(t, ok, ".any() on a parent that grants browse → granted (regular-arrow equivalent)")
}

func TestFolder_AllVia_TwoParentsBothGrant_Granted(t *testing.T) {
	ctx := context.Background()

	parent1 := extsvc.Folder("ftu-all-p1")
	parent2 := extsvc.Folder("ftu-all-p2")
	folder := extsvc.Folder("ftu-all-both")
	user := extsvc.User("u-ftu-all-both")

	// Both parents grant browse to user.
	require.NoError(t, parent1.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{user},
	}))
	require.NoError(t, parent2.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{user},
	}))
	// Link folder → both parents.
	require.NoError(t, folder.CreateAllParentRelations(ctx, extsvc.FolderAllParentObjects{
		Folder: []extsvc.Folder{parent1, parent2},
	}))

	ok, err := folder.CheckAllVia(ctx, extsvc.CheckFolderAllViaInputs{
		User: []extsvc.User{user},
	})
	require.NoError(t, err)
	assert.True(t, ok, ".all() with both parents granting browse → granted (strict intersection holds)")
}

func TestFolder_AllVia_TwoParentsOnlyOneGrants_Denied(t *testing.T) {
	ctx := context.Background()

	parent1 := extsvc.Folder("ftu-all-onep1")
	parent2 := extsvc.Folder("ftu-all-onep2")
	folder := extsvc.Folder("ftu-all-one")
	user := extsvc.User("u-ftu-all-one")

	// Only parent1 grants browse; parent2 has no viewer for this user.
	require.NoError(t, parent1.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{user},
	}))
	require.NoError(t, folder.CreateAllParentRelations(ctx, extsvc.FolderAllParentObjects{
		Folder: []extsvc.Folder{parent1, parent2},
	}))

	_, err := folder.CheckAllVia(ctx, extsvc.CheckFolderAllViaInputs{
		User: []extsvc.User{user},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied,
		".all() with one parent missing the grant → denied (proves strict intersection vs union)")
}

func TestFolder_AllVia_ZeroParents_Denied(t *testing.T) {
	ctx := context.Background()

	folder := extsvc.Folder("ftu-all-empty")
	user := extsvc.User("u-ftu-all-empty")

	// No all_parent tuples written for this folder.
	_, err := folder.CheckAllVia(ctx, extsvc.CheckFolderAllViaInputs{
		User: []extsvc.User{user},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied,
		".all() with zero parents → denied (vacuous case; no parents to traverse)")
}

func TestFolder_GatedAllVia_Combination_CaveatPlusAll_Granted(t *testing.T) {
	ctx := context.Background()

	parent1 := extsvc.Folder("ftu-gated-p1")
	parent2 := extsvc.Folder("ftu-gated-p2")
	folder := extsvc.Folder("ftu-gated-ok")
	user := extsvc.User("u-ftu-gated-ok")

	// Both parents grant browse to user.
	require.NoError(t, parent1.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{user},
	}))
	require.NoError(t, parent2.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{user},
	}))
	// Link folder → both parents via the CAVEATED gated_parent relation.
	// Pre-bind tenant=acme at write time on each row.
	require.NoError(t, folder.CreateGatedParentRelations(ctx, extsvc.FolderGatedParentObjects{
		Folder: []extsvc.Folder{parent1, parent2},
		Caveats: extsvc.FolderGatedParentCaveats{
			Folder: &extsvc.TenantMatchArgs{Tenant: new("acme")},
		},
	}))

	// Caveat reachable through the arrow → CheckGatedAllViaInputs gains
	// the Caveats sub-struct. Per AUZ-007 SPEC-003 walkPermCaveats handling,
	// caveats on the LeftRel of an arrow are collected; this test verifies
	// the same collection path runs through FunctionedTupleToUserset.
	ok, err := folder.CheckGatedAllVia(ctx, extsvc.CheckFolderGatedAllViaInputs{
		User: []extsvc.User{user},
	})
	require.NoError(t, err)
	assert.True(t, ok, ".all() + matching caveat + every parent grants → granted")
}

func TestFolder_GatedAllVia_Combination_WrongCaveat_Denied(t *testing.T) {
	ctx := context.Background()

	parent1 := extsvc.Folder("ftu-gated-no-p1")
	folder := extsvc.Folder("ftu-gated-no")
	user := extsvc.User("u-ftu-gated-no")

	require.NoError(t, parent1.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{user},
	}))
	// Defer the caveat at write time.
	require.NoError(t, folder.CreateGatedParentRelations(ctx, extsvc.FolderGatedParentObjects{
		Folder: []extsvc.Folder{parent1},
	}))

	// Wrong tenant supplied at check → CEL eval false → denied,
	// regardless of the .all() semantic.
	_, err := folder.CheckGatedAllVia(ctx, extsvc.CheckFolderGatedAllViaInputs{
		User: []extsvc.User{user},
		Caveats: extsvc.CheckFolderGatedAllViaCaveats{
			TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("not-acme")},
		},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied,
		".all() reaching a caveat that evaluates false → denied (caveat + .all() compose)")
}

func TestFolder_MixedAll_Combination_DirectPathGrants(t *testing.T) {
	ctx := context.Background()

	folder := extsvc.Folder("ftu-mixed-direct")
	user := extsvc.User("u-ftu-mixed-direct")

	// Grant via direct_member only — no all_parent rows.
	require.NoError(t, folder.CreateDirectMemberRelations(ctx, extsvc.FolderDirectMemberObjects{
		User: []extsvc.User{user},
	}))

	// Permission expression: direct_member + all_parent.all(browse)
	// Direct path satisfies → granted, even though .all() side has zero parents.
	ok, err := folder.CheckMixedAll(ctx, extsvc.CheckFolderMixedAllInputs{
		User: []extsvc.User{user},
	})
	require.NoError(t, err)
	assert.True(t, ok, "mixed expression: direct path grants regardless of .all() side")
}

func TestFolder_MixedAll_Combination_AllPathGrants(t *testing.T) {
	ctx := context.Background()

	parent1 := extsvc.Folder("ftu-mixed-all-p1")
	parent2 := extsvc.Folder("ftu-mixed-all-p2")
	folder := extsvc.Folder("ftu-mixed-all")
	user := extsvc.User("u-ftu-mixed-all")

	// User is NOT a direct_member of the folder; only reachable via
	// the .all() arrow side.
	require.NoError(t, parent1.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{user},
	}))
	require.NoError(t, parent2.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{user},
	}))
	require.NoError(t, folder.CreateAllParentRelations(ctx, extsvc.FolderAllParentObjects{
		Folder: []extsvc.Folder{parent1, parent2},
	}))

	ok, err := folder.CheckMixedAll(ctx, extsvc.CheckFolderMixedAllInputs{
		User: []extsvc.User{user},
	})
	require.NoError(t, err)
	assert.True(t, ok, "mixed expression: .all() side grants when every parent contributes browse, even with no direct_member")
}
