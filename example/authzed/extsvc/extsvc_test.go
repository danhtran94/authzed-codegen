package extsvc_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	extsvc "github.com/danhtran94/authzed-codegen/example/authzed/extsvc"
	"github.com/danhtran94/authzed-codegen/pkg/authz"
	"github.com/danhtran94/authzed-codegen/pkg/authz/spicedbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const schemaPath = "../../schema.zed"

type strID string

func (s strID) String() string { return string(s) }

// sb is the package-scoped Sandbox so tests can reach the underlying
// authzed.Client for write paths the codegen doesn't yet cover (e.g.
// attaching a caveat at write time — deferred per AUZ-006 scope).
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

// writeTenantedViewer attaches a tenant_match caveat (no pre-context)
// to a tenanted_viewer relationship via authzed-go directly. The
// codegen's CreateTenantedViewerRelations issues the write without a
// caveat — SpiceDB rejects that for `with caveat` relations, so
// caveat-aware tests bypass the codegen for the write half.
func writeTenantedViewer(ctx context.Context, t *testing.T, folderID, userID string) {
	t.Helper()
	res, err := sb.Client.WriteRelationships(ctx, &v1.WriteRelationshipsRequest{
		Updates: []*v1.RelationshipUpdate{{
			Operation: v1.RelationshipUpdate_OPERATION_CREATE,
			Relationship: &v1.Relationship{
				Resource: &v1.ObjectReference{ObjectType: "extsvc/folder", ObjectId: folderID},
				Relation: "tenanted_viewer",
				Subject: &v1.SubjectReference{
					Object: &v1.ObjectReference{ObjectType: "extsvc/user", ObjectId: userID},
				},
				OptionalCaveat: &v1.ContextualizedCaveat{CaveatName: "extsvc/tenant_match"},
			},
		}},
	})
	require.NoError(t, err)
	// Engine reads use snapshot consistency at the last token it wrote
	// itself; this direct write bypassed Engine.CreateRelations, so the
	// engine's snapshot would predate the new tuple. Advance it.
	sb.Engine.SetSnapshotToken(res.WrittenAt.Token)
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

	isWildcard, err := f.ReadGuestUserWildcard(ctx)
	require.NoError(t, err)
	assert.True(t, isWildcard)
}

func TestFolder_ReadGuestUserWildcard_Unset(t *testing.T) {
	ctx := context.Background()

	// guest is wildcard-only on the schema; an unset wildcard surfaces as false
	// without writing any tuple.
	f := extsvc.Folder("tf-w-no")

	isWildcard, err := f.ReadGuestUserWildcard(ctx)
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
	assert.Equal(t, []extsvc.User{"tvu3"}, users)
}

func TestFolder_LookupBrowseUserSubjects(t *testing.T) {
	ctx := context.Background()

	f := extsvc.Folder("tf-vu4")
	require.NoError(t, f.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"tvu4a", "tvu4b"},
	}))

	ids, err := f.LookupBrowseUserSubjects(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []extsvc.User{"tvu4a", "tvu4b"}, ids)
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
	assert.Equal(t, []extsvc.Folder{"fv4"}, folders)
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
	assert.Equal(t, []extsvc.User{"au5"}, users)
}

func TestArticle_ReadParent(t *testing.T) {
	ctx := context.Background()

	art := extsvc.Article("art6")
	require.NoError(t, art.CreateParentRelations(ctx, extsvc.ArticleParentObjects{
		Folder: []extsvc.Folder{"af4"},
	}))

	folders, err := art.ReadParentFolderRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []extsvc.Folder{"af4"}, folders)
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
	assert.Contains(t, ids, extsvc.User("au7"))
}

// --- Folder.tenanted_browse: caveat codegen path ---
//
// Schema: relation tenanted_viewer: extsvc/user with extsvc/tenant_match,
// caveat tenant_match(tenant string) { tenant == "acme" }.
// The write path attaches the caveat with no pre-context (writeTenantedViewer);
// the check provides the binding at request time via input.Caveat.

func TestFolder_CheckTenantedBrowse_MatchTenant(t *testing.T) {
	ctx := context.Background()

	writeTenantedViewer(ctx, t, "tcb-match", "u-match")

	ok, err := extsvc.Folder("tcb-match").CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User:   []extsvc.User{"u-match"},
		Caveat: &extsvc.TenantMatchArgs{Tenant: "acme"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestFolder_CheckTenantedBrowse_WrongTenant(t *testing.T) {
	ctx := context.Background()

	writeTenantedViewer(ctx, t, "tcb-wrong", "u-wrong")

	_, err := extsvc.Folder("tcb-wrong").CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User:   []extsvc.User{"u-wrong"},
		Caveat: &extsvc.TenantMatchArgs{Tenant: "not-acme"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

// Nil Caveat → SpiceDB returns CONDITIONAL_PERMISSION (caveat couldn't
// bind the `tenant` param). errorIfDenied maps anything other than
// HAS_PERMISSION to ErrPermissionDenied, so the conservative default is
// deny. A future job may surface CONDITIONAL as a distinct signal.
func TestFolder_CheckTenantedBrowse_NilCaveat(t *testing.T) {
	ctx := context.Background()

	writeTenantedViewer(ctx, t, "tcb-nil", "u-nil")

	_, err := extsvc.Folder("tcb-nil").CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
		User: []extsvc.User{"u-nil"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

func TestArticle_LookupAuthorOnlyUserSubjects(t *testing.T) {
	ctx := context.Background()

	art := extsvc.Article("art8")
	require.NoError(t, art.CreateAuthorRelations(ctx, extsvc.ArticleAuthorObjects{
		User: []extsvc.User{"au8"},
	}))

	ids, err := art.LookupAuthorOnlyUserSubjects(ctx)
	require.NoError(t, err)
	assert.Contains(t, ids, extsvc.User("au8"))
}
