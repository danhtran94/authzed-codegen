package generator

import (
	"testing"

	core "github.com/authzed/spicedb/pkg/proto/core/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flattenAllowedTypes computes IDFieldName and CaveatFieldName for
// each allowed type, disambiguating only when (Namespace, IsWildcard)
// collides AND the colliding entries carry distinct caveats. SpiceDB's
// schema compiler accepts `user with cav_a | user with cav_b`; the
// codegen supports it by appending the caveat's PascalCase name to
// disambiguate the Go field names.

func TestFlattenAllowedTypes_DuplicateNamespace_DifferentCaveats_Disambiguates(t *testing.T) {
	// `svc/user with svc/cav_a | svc/user with svc/cav_b` — same
	// (Namespace, IsWildcard), distinct caveats. Codegen succeeds
	// with disambiguated field names so the caller can pick which
	// caveat applies per batch.
	ti := &core.TypeInformation{
		AllowedDirectRelations: []*core.AllowedRelation{
			{
				Namespace:      "svc/user",
				RequiredCaveat: &core.AllowedCaveat{CaveatName: "svc/cav_a"},
			},
			{
				Namespace:      "svc/user",
				RequiredCaveat: &core.AllowedCaveat{CaveatName: "svc/cav_b"},
			},
		},
	}
	got, err := flattenAllowedTypes(ti)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "UserCavA", got[0].IDFieldName)
	assert.Equal(t, "UserCavA", got[0].CaveatFieldName)
	assert.Equal(t, "UserCavB", got[1].IDFieldName)
	assert.Equal(t, "UserCavB", got[1].CaveatFieldName)
}

func TestFlattenAllowedTypes_DuplicateNamespace_SameCaveat_Errors(t *testing.T) {
	// Same (Namespace, IsWildcard) AND same caveat — no semantic
	// disambiguation is possible (both branches would write
	// indistinguishable tuples). Schema author must refactor.
	ti := &core.TypeInformation{
		AllowedDirectRelations: []*core.AllowedRelation{
			{
				Namespace:      "svc/user",
				RequiredCaveat: &core.AllowedCaveat{CaveatName: "svc/cav"},
			},
			{
				Namespace:      "svc/user",
				RequiredCaveat: &core.AllowedCaveat{CaveatName: "svc/cav"},
			},
		},
	}
	_, err := flattenAllowedTypes(ti)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate allowed type "svc/user"`)
}

func TestFlattenAllowedTypes_DuplicateNamespace_MixedCaveatPresence_Errors(t *testing.T) {
	// `svc/user with cav | svc/user` — one caveated, one not. The
	// non-caveated branch has no caveat name to disambiguate against.
	// Reject — schema author should split into two relations.
	ti := &core.TypeInformation{
		AllowedDirectRelations: []*core.AllowedRelation{
			{
				Namespace:      "svc/user",
				RequiredCaveat: &core.AllowedCaveat{CaveatName: "svc/cav"},
			},
			{Namespace: "svc/user"},
		},
	}
	_, err := flattenAllowedTypes(ti)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate allowed type "svc/user"`)
}

func TestFlattenAllowedTypes_DistinctNamespaces_SameCaveat_KeepsCleanNames(t *testing.T) {
	// Legal Case 2: different allowed types gated by the same caveat.
	// No collision on (Namespace, IsWildcard), so field names stay
	// clean (User, Customer) — no caveat suffix.
	ti := &core.TypeInformation{
		AllowedDirectRelations: []*core.AllowedRelation{
			{
				Namespace:      "svc/user",
				RequiredCaveat: &core.AllowedCaveat{CaveatName: "svc/cav"},
			},
			{
				Namespace:      "svc/customer",
				RequiredCaveat: &core.AllowedCaveat{CaveatName: "svc/cav"},
			},
		},
	}
	got, err := flattenAllowedTypes(ti)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "User", got[0].IDFieldName)
	assert.Equal(t, "User", got[0].CaveatFieldName)
	assert.Equal(t, "Customer", got[1].IDFieldName)
	assert.Equal(t, "Customer", got[1].CaveatFieldName)
}

func TestFlattenAllowedTypes_SameNamespace_DifferentWildcardBit_OK(t *testing.T) {
	// `svc/user | svc/user:*` — same Namespace, different IsWildcard.
	// The (Namespace, IsWildcard) key disambiguates: the regular
	// branch generates `User []User`, the wildcard branch generates
	// `Wildcards.User bool`. No collision.
	ti := &core.TypeInformation{
		AllowedDirectRelations: []*core.AllowedRelation{
			{Namespace: "svc/user"},
			{
				Namespace: "svc/user",
				RelationOrWildcard: &core.AllowedRelation_PublicWildcard_{
					PublicWildcard: &core.AllowedRelation_PublicWildcard{},
				},
			},
		},
	}
	got, err := flattenAllowedTypes(ti)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.False(t, got[0].IsWildcard)
	assert.True(t, got[1].IsWildcard)
}
