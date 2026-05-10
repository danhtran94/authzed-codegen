package extsvc_test

import (
	"context"
	"testing"

	extsvc "github.com/danhtran94/authzed-codegen/example/authzed/extsvc"
	"github.com/danhtran94/authzed-codegen/pkg/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFolder_PurgeViewerRelations — Purge<Rel>Relations clears one relation
// entirely (all subjects), and leaves other relations on the same resource
// untouched. (AUZ-023)
func TestFolder_PurgeViewerRelations(t *testing.T) {
	ctx := context.Background()
	folder := extsvc.Folder("pg-fv1")

	require.NoError(t, folder.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User:  []extsvc.User{"pg-u-a"},
		Group: []extsvc.Group{"pg-g-a"},
	}))
	require.NoError(t, folder.CreateAnyParentRelations(ctx, extsvc.FolderAnyParentObjects{
		Folder: []extsvc.Folder{"pg-fp1"},
	}))

	// Sanity: viewer + any_parent both present.
	vu, err := folder.ReadViewerUserRelations(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, vu)
	pf, err := folder.ReadAnyParentFolderRelations(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, pf)

	require.NoError(t, folder.PurgeViewerRelations(ctx))

	vu, err = folder.ReadViewerUserRelations(ctx)
	require.NoError(t, err)
	assert.Empty(t, vu, "viewer user tuples purged")
	vg, err := folder.ReadViewerGroupRelations(ctx)
	require.NoError(t, err)
	assert.Empty(t, vg, "viewer group tuples purged")
	pf, err = folder.ReadAnyParentFolderRelations(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, pf, "any_parent untouched by PurgeViewerRelations")
}

// TestFolder_PurgeRelations — PurgeRelations clears every relation on the
// resource in one transaction. (AUZ-023)
func TestFolder_PurgeRelations(t *testing.T) {
	ctx := context.Background()
	folder := extsvc.Folder("pg-fr1")

	require.NoError(t, folder.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{User: []extsvc.User{"pg-u-r"}}))
	require.NoError(t, folder.CreateAnyParentRelations(ctx, extsvc.FolderAnyParentObjects{Folder: []extsvc.Folder{"pg-fr-parent"}}))

	require.NoError(t, folder.PurgeRelations(ctx))

	vu, err := folder.ReadViewerUserRelations(ctx)
	require.NoError(t, err)
	assert.Empty(t, vu, "viewer purged by PurgeRelations")
	pf, err := folder.ReadAnyParentFolderRelations(ctx)
	require.NoError(t, err)
	assert.Empty(t, pf, "any_parent purged by PurgeRelations")
}

// TestUser_PurgeRelationsAsSubject — removes the object from every definition
// that references it as a subject (here: folder.viewer AND document.owner —
// extsvc/user is a subject of multiple definitions, so the method fans out one
// filter-delete per referencing definition). (AUZ-023)
func TestUser_PurgeRelationsAsSubject(t *testing.T) {
	ctx := context.Background()
	user := extsvc.User("pg-u-subj")
	folder := extsvc.Folder("pg-f-subj")
	doc := extsvc.Document("pg-d-subj")
	other := extsvc.User("pg-u-keep") // bystander subject — must survive

	require.NoError(t, folder.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{User: []extsvc.User{user, other}}))
	require.NoError(t, doc.CreateOwnerRelations(ctx, extsvc.DocumentOwnerObjects{User: []extsvc.User{user}}))

	vu, err := folder.ReadViewerUserRelations(ctx)
	require.NoError(t, err)
	require.Contains(t, authz.IDsOf(vu), user, "seeded: user is a viewer of the folder")
	ou, err := doc.ReadOwnerUserRelations(ctx)
	require.NoError(t, err)
	require.Contains(t, authz.IDsOf(ou), user, "seeded: user is an owner of the document")

	require.NoError(t, user.PurgeRelationsAsSubject(ctx))

	vu, err = folder.ReadViewerUserRelations(ctx)
	require.NoError(t, err)
	assert.NotContains(t, authz.IDsOf(vu), user, "user removed from folder.viewer")
	assert.Contains(t, authz.IDsOf(vu), other, "bystander subject untouched")
	ou, err = doc.ReadOwnerUserRelations(ctx)
	require.NoError(t, err)
	assert.NotContains(t, authz.IDsOf(ou), user, "user removed from document.owner")
}

// TestEngine_DeleteRelationsMatching_EmptyFilter — an all-empty RelationFilter
// is rejected with ErrEmptyRelationFilter (it would match everything). (AUZ-023)
func TestEngine_DeleteRelationsMatching_EmptyFilter(t *testing.T) {
	ctx := context.Background()
	err := sb.Engine.DeleteRelationsMatching(ctx, authz.RelationFilter{})
	assert.ErrorIs(t, err, authz.ErrEmptyRelationFilter)
}

// ---------------------------------------------------------------------------
// Edge cases (AUZ-023 — added under "test carefully" follow-up)
// ---------------------------------------------------------------------------

// TestFolder_Purge_IdempotentAndEmptyTarget — a filter-delete that matches
// nothing is not an error, and re-running a purge is a no-op. SpiceDB's
// DeleteRelationships is unconditional; zero matches → DeletedAt token, no err.
func TestFolder_Purge_IdempotentAndEmptyTarget(t *testing.T) {
	ctx := context.Background()
	folder := extsvc.Folder("pg-idem")

	// Nothing seeded yet — purge an empty relation.
	require.NoError(t, folder.PurgeViewerRelations(ctx), "purge on empty relation is a no-op, not an error")
	require.NoError(t, folder.PurgeRelations(ctx), "purge-all on empty resource is a no-op")

	require.NoError(t, folder.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{User: []extsvc.User{"pg-idem-u"}}))
	require.NoError(t, folder.PurgeViewerRelations(ctx))
	vu, err := folder.ReadViewerUserRelations(ctx)
	require.NoError(t, err)
	require.Empty(t, vu)

	// Second purge — already empty.
	require.NoError(t, folder.PurgeViewerRelations(ctx), "re-purge is idempotent")
	require.NoError(t, folder.PurgeRelations(ctx), "re-purge-all is idempotent")
}

// TestFolder_Purge_ResourceIsolation — Purge<Rel>Relations and PurgeRelations
// are scoped to one resource ID; a sibling resource of the same type is
// untouched.
func TestFolder_Purge_ResourceIsolation(t *testing.T) {
	ctx := context.Background()
	a := extsvc.Folder("pg-iso-a")
	b := extsvc.Folder("pg-iso-b")

	require.NoError(t, a.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{User: []extsvc.User{"pg-iso-ua"}}))
	require.NoError(t, b.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{User: []extsvc.User{"pg-iso-ub"}}))

	require.NoError(t, a.PurgeViewerRelations(ctx))
	va, err := a.ReadViewerUserRelations(ctx)
	require.NoError(t, err)
	assert.Empty(t, va, "folder a's viewer purged")
	vb, err := b.ReadViewerUserRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []extsvc.User{"pg-iso-ub"}, authz.IDsOf(vb), "folder b untouched by folder a's purge")

	// Re-seed a, then PurgeRelations a — b still untouched.
	require.NoError(t, a.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{User: []extsvc.User{"pg-iso-ua2"}}))
	require.NoError(t, a.PurgeRelations(ctx))
	vb, err = b.ReadViewerUserRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []extsvc.User{"pg-iso-ub"}, authz.IDsOf(vb), "folder b untouched by folder a's PurgeRelations")
}

// TestFolder_PurgeGuestRelations_DeletesWildcardTuple — the filter is by
// (resource, relation); it ignores tuple "shape". A wildcard grant
// (folder:f, guest, user:*) is deleted by PurgeGuestRelations just like a
// plain tuple would be.
func TestFolder_PurgeGuestRelations_DeletesWildcardTuple(t *testing.T) {
	ctx := context.Background()
	folder := extsvc.Folder("pg-wc")

	require.NoError(t, folder.CreateGuestRelations(ctx, extsvc.FolderGuestObjects{
		Wildcards: extsvc.FolderGuestWildcards{User: true},
	}))
	_, isWildcard, err := folder.ReadGuestUserWildcard(ctx)
	require.NoError(t, err)
	require.True(t, isWildcard, "seeded: guest is wildcard-granted")

	require.NoError(t, folder.PurgeGuestRelations(ctx))

	_, isWildcard, err = folder.ReadGuestUserWildcard(ctx)
	require.NoError(t, err)
	assert.False(t, isWildcard, "wildcard guest tuple deleted by PurgeGuestRelations")
}

// TestFolder_TwoCallLifecycle — the documented orphan hazard + its fix.
// (folder:child, any_parent, folder:parent) is a tuple where folder:parent is
// the *subject*. parent.PurgeRelations() (resource-side) clears parent's own
// tuples but leaves that subject-side tuple behind — child still points at a
// dead parent. parent.PurgeRelationsAsSubject() (subject-side) finishes the
// job. Both calls are needed on object deletion.
func TestFolder_TwoCallLifecycle(t *testing.T) {
	ctx := context.Background()
	child := extsvc.Folder("pg-lc-child")
	parent := extsvc.Folder("pg-lc-parent")

	require.NoError(t, parent.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{User: []extsvc.User{"pg-lc-u"}}))
	require.NoError(t, child.CreateAnyParentRelations(ctx, extsvc.FolderAnyParentObjects{Folder: []extsvc.Folder{parent}}))

	// Sanity.
	pv, err := parent.ReadViewerUserRelations(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, pv)
	cp, err := child.ReadAnyParentFolderRelations(ctx)
	require.NoError(t, err)
	require.Contains(t, authz.IDsOf(cp), parent, "seeded: child.any_parent → parent")

	// Step 1 — resource-side purge. Clears parent's own tuples; does NOT touch
	// the (child, any_parent, parent) tuple where parent is the subject.
	require.NoError(t, parent.PurgeRelations(ctx))
	pv, err = parent.ReadViewerUserRelations(ctx)
	require.NoError(t, err)
	assert.Empty(t, pv, "parent's resource-side tuples cleared")
	cp, err = child.ReadAnyParentFolderRelations(ctx)
	require.NoError(t, err)
	assert.Contains(t, authz.IDsOf(cp), parent, "ORPHAN: child still points at the deleted parent after PurgeRelations alone")

	// Step 2 — subject-side purge. Removes the dangling reference.
	require.NoError(t, parent.PurgeRelationsAsSubject(ctx))
	cp, err = child.ReadAnyParentFolderRelations(ctx)
	require.NoError(t, err)
	assert.NotContains(t, authz.IDsOf(cp), parent, "subject-side tuple removed by PurgeRelationsAsSubject — lifecycle complete")
}

// TestTeam_PurgeRelationsAsSubject_RemovesUsersetTuple — a userset grant
// (folder:f, collab, team:t#admin) names team:t as the subject anchor with
// sub-relation "admin". PurgeRelationsAsSubject filters by {resourceType,
// subjectType=team, subjectId=t} with OptionalSubjectFilter.OptionalRelation
// left nil — nil = "any sub-relation", so the #admin tuple matches and is
// removed. (Verifies SPEC-014 C3: leave OptionalRelation nil.)
func TestTeam_PurgeRelationsAsSubject_RemovesUsersetTuple(t *testing.T) {
	ctx := context.Background()
	team := extsvc.Team("pg-us-t1")
	folder := extsvc.Folder("pg-us-f1")

	require.NoError(t, folder.CreateCollabRelations(ctx, extsvc.FolderCollabObjects{TeamAdmin: []extsvc.Team{team}}))
	ct, err := folder.ReadCollabTeamRelations(ctx)
	require.NoError(t, err)
	require.Contains(t, authz.IDsOf(ct), team, "seeded: folder.collab → team#admin")

	require.NoError(t, team.PurgeRelationsAsSubject(ctx))

	ct, err = folder.ReadCollabTeamRelations(ctx)
	require.NoError(t, err)
	assert.NotContains(t, authz.IDsOf(ct), team, "userset tuple removed despite the #admin sub-relation")
}

// TestEngine_DeleteRelationsMatching_BySubjectOnly — the raw primitive with
// no resource type: {SubjectType, SubjectID} only. SpiceDB accepts a
// RelationshipFilter without a resource_type (index-suboptimal per authzed PR
// #1739, but valid) and deletes that subject everywhere in one call — the
// single-RPC alternative to PurgeRelationsAsSubject's per-definition fan-out.
func TestEngine_DeleteRelationsMatching_BySubjectOnly(t *testing.T) {
	ctx := context.Background()
	u := extsvc.User("pg-scr-u1")
	folder := extsvc.Folder("pg-scr-f1")
	doc := extsvc.Document("pg-scr-d1")

	require.NoError(t, folder.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{User: []extsvc.User{u}}))
	require.NoError(t, doc.CreateOwnerRelations(ctx, extsvc.DocumentOwnerObjects{User: []extsvc.User{u}}))

	require.NoError(t, sb.Engine.DeleteRelationsMatching(ctx, authz.RelationFilter{
		SubjectType: extsvc.TypeUser,
		SubjectID:   authz.ID(u),
	}))

	vu, err := folder.ReadViewerUserRelations(ctx)
	require.NoError(t, err)
	assert.NotContains(t, authz.IDsOf(vu), u, "removed from folder.viewer by one resource-type-less call")
	ou, err := doc.ReadOwnerUserRelations(ctx)
	require.NoError(t, err)
	assert.NotContains(t, authz.IDsOf(ou), u, "removed from document.owner by the same call")
}

// TestEngine_DeleteRelationsMatching_FilterShapes — the three field
// combinations the generated Purge* methods are built from, exercised directly
// on the Engine.
func TestEngine_DeleteRelationsMatching_FilterShapes(t *testing.T) {
	ctx := context.Background()

	t.Run("resource+relation", func(t *testing.T) {
		f := extsvc.Folder("pg-fs-rr")
		require.NoError(t, f.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{User: []extsvc.User{"a", "b"}}))
		require.NoError(t, f.CreateAnyParentRelations(ctx, extsvc.FolderAnyParentObjects{Folder: []extsvc.Folder{"pg-fs-rr-p"}}))
		require.NoError(t, sb.Engine.DeleteRelationsMatching(ctx, authz.RelationFilter{
			ResourceType: extsvc.TypeFolder, ResourceID: authz.ID(f), Relation: authz.Relation(extsvc.FolderViewer),
		}))
		vu, err := f.ReadViewerUserRelations(ctx)
		require.NoError(t, err)
		assert.Empty(t, vu, "viewer cleared")
		ap, err := f.ReadAnyParentFolderRelations(ctx)
		require.NoError(t, err)
		assert.NotEmpty(t, ap, "any_parent untouched (relation-scoped)")
	})

	t.Run("resource only", func(t *testing.T) {
		f := extsvc.Folder("pg-fs-r")
		require.NoError(t, f.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{User: []extsvc.User{"a"}}))
		require.NoError(t, f.CreateAnyParentRelations(ctx, extsvc.FolderAnyParentObjects{Folder: []extsvc.Folder{"pg-fs-r-p"}}))
		require.NoError(t, sb.Engine.DeleteRelationsMatching(ctx, authz.RelationFilter{
			ResourceType: extsvc.TypeFolder, ResourceID: authz.ID(f),
		}))
		vu, err := f.ReadViewerUserRelations(ctx)
		require.NoError(t, err)
		assert.Empty(t, vu)
		ap, err := f.ReadAnyParentFolderRelations(ctx)
		require.NoError(t, err)
		assert.Empty(t, ap, "all relations on the resource cleared")
	})

	t.Run("resource-type + subject", func(t *testing.T) {
		f := extsvc.Folder("pg-fs-rts")
		u := extsvc.User("pg-fs-rts-u")
		require.NoError(t, f.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{User: []extsvc.User{u, "pg-fs-rts-keep"}}))
		require.NoError(t, sb.Engine.DeleteRelationsMatching(ctx, authz.RelationFilter{
			ResourceType: extsvc.TypeFolder, SubjectType: extsvc.TypeUser, SubjectID: authz.ID(u),
		}))
		vu, err := f.ReadViewerUserRelations(ctx)
		require.NoError(t, err)
		assert.NotContains(t, authz.IDsOf(vu), u, "subject removed across all folders")
		assert.Contains(t, authz.IDsOf(vu), extsvc.User("pg-fs-rts-keep"), "other subject on the same relation untouched")
	})
}
