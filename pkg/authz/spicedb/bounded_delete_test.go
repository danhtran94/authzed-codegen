package spicedb_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/danhtran94/authzed-codegen/v2/pkg/authz"
	"github.com/stretchr/testify/require"
)

// TestEngine_DeleteRelationsMatching_Bounded — AUZ-024 / ADR-006. opts.Limit > 0
// bounds a single DeleteRelationsMatching call: it deletes at most Limit
// relationships and reports Complete == false when more remain, with no
// SpiceDB cursor involved (ADR-006 never sets OptionalCursor). Looping on the
// same filter and limit — caller-driven, no cursor argument — eventually
// clears everything and reports Complete == true.
func TestEngine_DeleteRelationsMatching_Bounded(t *testing.T) {
	ctx := context.Background()
	eng := authz.GetEngine(ctx)

	folder := authz.Resource{Type: authz.Type("extsvc/folder"), ID: authz.ID("bd-bounded-f1")}
	const total = 5
	ids := make([]authz.ID, 0, total)
	for i := 0; i < total; i++ {
		ids = append(ids, authz.ID(fmt.Sprintf("bd-bounded-u%d", i)))
	}
	require.NoError(t, eng.CreateRelations(ctx, folder, authz.Relation("viewer"), authz.Type("extsvc/user"), ids))

	filter := authz.RelationFilter{
		ResourceType: folder.Type,
		ResourceID:   folder.ID,
		Relation:     authz.Relation("viewer"),
	}

	var deleted uint64
	const limit = 2
	for i := 0; i < total; i++ { // bounded loop: at most `total` iterations, never relies on an infinite loop
		progress, err := eng.DeleteRelationsMatching(ctx, filter, authz.DeleteOptions{Limit: limit})
		require.NoError(t, err)
		deleted += progress.Deleted

		if progress.Complete {
			require.LessOrEqual(t, progress.Deleted, uint64(limit), "last call deletes at most the limit")
			break
		}
		require.Equal(t, uint64(limit), progress.Deleted, "a non-complete call deletes exactly the limit")
	}
	require.Equal(t, uint64(total), deleted, "the full set is removed across bounded calls")

	remaining, err := eng.ReadRelations(ctx, folder, authz.Relation("viewer"), authz.Type("extsvc/user"))
	require.NoError(t, err)
	require.Empty(t, remaining, "nothing left after the bounded loop completes")
}

// TestEngine_DeleteRelationsMatching_ZeroOptsUnbounded — DeleteOptions{} (the
// zero value) reproduces the pre-ADR-006 behavior: one unbounded,
// single-transaction delete, Complete always true.
func TestEngine_DeleteRelationsMatching_ZeroOptsUnbounded(t *testing.T) {
	ctx := context.Background()
	eng := authz.GetEngine(ctx)

	folder := authz.Resource{Type: authz.Type("extsvc/folder"), ID: authz.ID("bd-zero-f1")}
	ids := []authz.ID{"bd-zero-u1", "bd-zero-u2", "bd-zero-u3"}
	require.NoError(t, eng.CreateRelations(ctx, folder, authz.Relation("viewer"), authz.Type("extsvc/user"), ids))

	progress, err := eng.DeleteRelationsMatching(ctx, authz.RelationFilter{
		ResourceType: folder.Type,
		ResourceID:   folder.ID,
		Relation:     authz.Relation("viewer"),
	}, authz.DeleteOptions{})
	require.NoError(t, err)
	require.True(t, progress.Complete, "zero-value DeleteOptions is always complete")
	require.Equal(t, uint64(len(ids)), progress.Deleted)

	remaining, err := eng.ReadRelations(ctx, folder, authz.Relation("viewer"), authz.Type("extsvc/user"))
	require.NoError(t, err)
	require.Empty(t, remaining)
}
