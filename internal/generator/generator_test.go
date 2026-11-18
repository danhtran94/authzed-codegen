package generator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AUZ-028 — operator-aware grantability over the fixture schema.
func TestGrantableInputTypes_Fixture(t *testing.T) {
	defs, _ := adaptFixtureSchema(t)
	byTypes := ParseDefinitions(defs)
	tree, err := byTypes.GetPermissionTree()
	require.NoError(t, err)
	grantable := grantableInputTypes(defs, byTypes)

	// Never invents a type: grantable ⊆ permissionInputTypes, for every permission.
	for key, types := range grantable {
		assert.Subset(t, tree[key], types, "grantable(%s) must be a subset of its input types", key)
	}

	// `author` allows only extsvc/user; parent->browse's group/role reach
	// only the excluded side (-) or the other arm of the intersection (&).
	assert.ElementsMatch(t, []string{"extsvc/user"}, grantable["extsvc/article/author_only"])
	assert.ElementsMatch(t, []string{"extsvc/user"}, grantable["extsvc/article/editor"])

	// Union keeps every branch; nested union under exclusion keeps all of its branches.
	assert.ElementsMatch(t, tree["extsvc/document/edit"], grantable["extsvc/document/edit"])
	assert.ElementsMatch(t, tree["extsvc/folder/nested_access"], grantable["extsvc/folder/nested_access"])

	// Recursion resolves (least fixpoint): `self + parent_for_self->ancestor_or_self`.
	assert.Contains(t, grantable["extsvc/folder/ancestor_or_self"], "extsvc/folder")

	// .all() grants the same types as .any() at the type level.
	assert.ElementsMatch(t, grantable["extsvc/folder/any_via"], grantable["extsvc/folder/all_via"])
}

func TestGrantableInputTypes_IntersectionAndExclusionRules(t *testing.T) {
	leaf := func(rel string) *PermissionNode {
		return &PermissionNode{Leaf: &PermissionExpr{Kind: PermExprIdentifier, Ident: rel}}
	}
	rel := func(name string, ns ...string) *RelationView {
		r := &RelationView{Name: name}
		for _, n := range ns {
			r.AllowedTypes = append(r.AllowedTypes, AllowedType{Namespace: n})
		}
		return r
	}
	perm := func(name string, n *PermissionNode) *PermissionView {
		return &PermissionView{Name: name, Rewrite: n, Expressions: flattenPermissionNode(n)}
	}
	defs := []*DefinitionView{{
		ObjectType: ObjectType{Prefix: "t", Name: "doc"},
		Relations: []*RelationView{
			rel("a", "t/user", "t/bot"),
			rel("b", "t/user", "t/group"),
		},
		Permissions: []*PermissionView{
			perm("both", &PermissionNode{Op: PermOpIntersection, Children: []*PermissionNode{leaf("a"), leaf("b")}}),
			perm("minus", &PermissionNode{Op: PermOpExclusion, Children: []*PermissionNode{leaf("a"), leaf("b")}}),
			perm("either", &PermissionNode{Op: PermOpUnion, Children: []*PermissionNode{leaf("a"), leaf("b")}}),
		},
	}}
	got := grantableInputTypes(defs, ParseDefinitions(defs))
	assert.ElementsMatch(t, []string{"t/user"}, got["t/doc/both"])
	assert.ElementsMatch(t, []string{"t/user", "t/bot"}, got["t/doc/minus"])
	assert.ElementsMatch(t, []string{"t/user", "t/bot", "t/group"}, got["t/doc/either"])
}
