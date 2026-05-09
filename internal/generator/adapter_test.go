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

// AUZ-011 — sub-relation references (`team#admin`).

func TestFlattenAllowedTypes_PlainUserset_AcceptedWithSubRelation(t *testing.T) {
	// `svc/team#admin` — single userset reference. Adapter captures
	// SubRelation; IDFieldName composes namespace + pascalized
	// sub-relation: TeamAdmin.
	ti := &core.TypeInformation{
		AllowedDirectRelations: []*core.AllowedRelation{
			{
				Namespace:          "svc/team",
				RelationOrWildcard: &core.AllowedRelation_Relation{Relation: "admin"},
			},
		},
	}
	got, err := flattenAllowedTypes(ti)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "svc/team", got[0].Namespace)
	assert.Equal(t, "admin", got[0].SubRelation)
	assert.Equal(t, "TeamAdmin", got[0].IDFieldName)
	assert.False(t, got[0].IsWildcard)
}

func TestFlattenAllowedTypes_MixedDirectAndUserset_DistinctFieldNames(t *testing.T) {
	// `svc/user | svc/team#admin` — direct subject + userset reference
	// to a different namespace. No collision; IDFieldNames are
	// User and TeamAdmin.
	ti := &core.TypeInformation{
		AllowedDirectRelations: []*core.AllowedRelation{
			{Namespace: "svc/user"},
			{
				Namespace:          "svc/team",
				RelationOrWildcard: &core.AllowedRelation_Relation{Relation: "admin"},
			},
		},
	}
	got, err := flattenAllowedTypes(ti)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "User", got[0].IDFieldName)
	assert.Equal(t, "", got[0].SubRelation)
	assert.Equal(t, "TeamAdmin", got[1].IDFieldName)
	assert.Equal(t, "admin", got[1].SubRelation)
}

func TestFlattenAllowedTypes_TwoUsersetsSameNamespace_DistinctSubRelations_NoCollision(t *testing.T) {
	// `svc/team#admin | svc/team#owner` — same namespace, distinct
	// sub-relations. The 3-key disambiguation (Namespace, IsWildcard,
	// SubRelation) treats these as separate entries; IDFieldNames are
	// TeamAdmin and TeamOwner.
	ti := &core.TypeInformation{
		AllowedDirectRelations: []*core.AllowedRelation{
			{
				Namespace:          "svc/team",
				RelationOrWildcard: &core.AllowedRelation_Relation{Relation: "admin"},
			},
			{
				Namespace:          "svc/team",
				RelationOrWildcard: &core.AllowedRelation_Relation{Relation: "owner"},
			},
		},
	}
	got, err := flattenAllowedTypes(ti)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "TeamAdmin", got[0].IDFieldName)
	assert.Equal(t, "admin", got[0].SubRelation)
	assert.Equal(t, "TeamOwner", got[1].IDFieldName)
	assert.Equal(t, "owner", got[1].SubRelation)
}

func TestFlattenAllowedTypes_DirectAndUsersetSameNamespace_NoCollision(t *testing.T) {
	// `svc/team | svc/team#admin` — same namespace, distinct
	// SubRelation values (one empty, one non-empty). 3-key
	// disambiguation produces Team and TeamAdmin field names.
	ti := &core.TypeInformation{
		AllowedDirectRelations: []*core.AllowedRelation{
			{Namespace: "svc/team"},
			{
				Namespace:          "svc/team",
				RelationOrWildcard: &core.AllowedRelation_Relation{Relation: "admin"},
			},
		},
	}
	got, err := flattenAllowedTypes(ti)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "Team", got[0].IDFieldName)
	assert.Equal(t, "", got[0].SubRelation)
	assert.Equal(t, "TeamAdmin", got[1].IDFieldName)
	assert.Equal(t, "admin", got[1].SubRelation)
}

func TestFlattenAllowedTypes_UsersetWithDistinctCaveats_Disambiguates(t *testing.T) {
	// `svc/team#admin with svc/cav_a | svc/team#admin with svc/cav_b`
	// — same (Namespace, IsWildcard, SubRelation), distinct caveats.
	// Caveat suffix appends to disambiguate: TeamAdminCavA, TeamAdminCavB.
	ti := &core.TypeInformation{
		AllowedDirectRelations: []*core.AllowedRelation{
			{
				Namespace:          "svc/team",
				RelationOrWildcard: &core.AllowedRelation_Relation{Relation: "admin"},
				RequiredCaveat:     &core.AllowedCaveat{CaveatName: "svc/cav_a"},
			},
			{
				Namespace:          "svc/team",
				RelationOrWildcard: &core.AllowedRelation_Relation{Relation: "admin"},
				RequiredCaveat:     &core.AllowedCaveat{CaveatName: "svc/cav_b"},
			},
		},
	}
	got, err := flattenAllowedTypes(ti)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "TeamAdminCavA", got[0].IDFieldName)
	assert.Equal(t, "TeamAdminCavA", got[0].CaveatFieldName)
	assert.Equal(t, "TeamAdminCavB", got[1].IDFieldName)
	assert.Equal(t, "TeamAdminCavB", got[1].CaveatFieldName)
}

// AUZ-016 — Functioned tuple-to-userset (`parent.any(view)` / `parent.all(view)`).

func TestLowerSetOperationChild_FunctionedTUSeAny_MapsToPermExprArrow(t *testing.T) {
	// `permission p = parent.any(view)` — codegen treats it identically
	// to a regular TupleToUserset arrow (PermExprArrow with LeftRel +
	// RightPerm). Function value (FUNCTION_ANY) is read but not stored.
	child := &core.SetOperation_Child{
		ChildType: &core.SetOperation_Child_FunctionedTupleToUserset{
			FunctionedTupleToUserset: &core.FunctionedTupleToUserset{
				Function:        core.FunctionedTupleToUserset_FUNCTION_ANY,
				Tupleset:        &core.FunctionedTupleToUserset_Tupleset{Relation: "parent"},
				ComputedUserset: &core.ComputedUserset{Relation: "view"},
			},
		},
	}
	got, err := lowerSetOperationChild("p", child)
	require.NoError(t, err)
	assert.Equal(t, PermExprArrow, got.Kind)
	assert.Equal(t, "parent", got.LeftRel)
	assert.Equal(t, "view", got.RightPerm)
}

func TestLowerSetOperationChild_FunctionedTUSeAll_MapsToPermExprArrow(t *testing.T) {
	// `permission p = parent.all(view)` — strict-intersection semantic
	// is server-side at Check time; codegen output is identical to .any()
	// at the PermissionExpr level.
	child := &core.SetOperation_Child{
		ChildType: &core.SetOperation_Child_FunctionedTupleToUserset{
			FunctionedTupleToUserset: &core.FunctionedTupleToUserset{
				Function:        core.FunctionedTupleToUserset_FUNCTION_ALL,
				Tupleset:        &core.FunctionedTupleToUserset_Tupleset{Relation: "parent"},
				ComputedUserset: &core.ComputedUserset{Relation: "view"},
			},
		},
	}
	got, err := lowerSetOperationChild("p", child)
	require.NoError(t, err)
	assert.Equal(t, PermExprArrow, got.Kind)
	assert.Equal(t, "parent", got.LeftRel)
	assert.Equal(t, "view", got.RightPerm)
}

// AUZ-017 — `_self` schema construct (`use self`).

func TestLowerSetOperationChild_XSelf_MapsToPermExprSelf(t *testing.T) {
	// `permission p = self` — codegen accepts and maps to PermExprSelf
	// kind. No payload (LeftRel / RightPerm / Ident all empty).
	child := &core.SetOperation_Child{
		ChildType: &core.SetOperation_Child_XSelf{
			XSelf: &core.SetOperation_Child_Self{},
		},
	}
	got, err := lowerSetOperationChild("p", child)
	require.NoError(t, err)
	assert.Equal(t, PermExprSelf, got.Kind)
	assert.Empty(t, got.LeftRel)
	assert.Empty(t, got.RightPerm)
	assert.Empty(t, got.Ident)
}
