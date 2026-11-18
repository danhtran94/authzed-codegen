package generator

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// neverGrantedSets returns, per "<type>/<perm>", the input types that get
// a "never granted" comment: permissionInputTypes minus grantable.
func neverGrantedSets(t *testing.T, defs []*DefinitionView) map[string][]string {
	t.Helper()
	tree, err := ParseDefinitions(defs).GetPermissionTree()
	require.NoError(t, err)
	grantable := grantableInputTypes(defs)

	out := map[string][]string{}
	for key, fields := range tree {
		ok := map[string]bool{}
		for _, g := range grantable[key] {
			ok[g] = true
		}
		var never []string
		for _, f := range fields {
			if !ok[f] {
				never = append(never, f)
			}
		}
		if len(never) > 0 {
			sort.Strings(never)
			out[key] = never
		}
	}
	return out
}

// AUZ-028 — on the fixture, exactly the article permissions' group/role
// fields are never granted: they reach `author_only` only through the
// excluded `- parent->browse`, and `editor` misses the `author` operand of
// `author & parent->browse`. Every other input field stays uncommented.
func TestNeverGranted_Fixture(t *testing.T) {
	defs, _ := adaptFixtureSchema(t)
	assert.Equal(t, map[string][]string{
		"extsvc/article/author_only": {"extsvc/group", "extsvc/role"},
		"extsvc/article/editor":      {"extsvc/group", "extsvc/role"},
	}, neverGrantedSets(t, defs))

	grantable := grantableInputTypes(defs)
	// Recursion resolves (least fixpoint): `self + parent_for_self->ancestor_or_self`.
	assert.Contains(t, grantable["extsvc/folder/ancestor_or_self"], "extsvc/folder")
	// .all() grants the same types as .any() at the type level.
	assert.ElementsMatch(t, grantable["extsvc/folder/any_via"], grantable["extsvc/folder/all_via"])
}

// --- synthetic schemas -----------------------------------------------------

func leafIdent(rel string) *PermissionNode {
	return &PermissionNode{Leaf: &PermissionExpr{Kind: PermExprIdentifier, Ident: rel}}
}

func leafArrow(left, right string) *PermissionNode {
	return &PermissionNode{Leaf: &PermissionExpr{Kind: PermExprArrow, LeftRel: left, RightPerm: right}}
}

func op(o PermOp, children ...*PermissionNode) *PermissionNode {
	return &PermissionNode{Op: o, Children: children}
}

func relView(name string, allowed ...AllowedType) *RelationView {
	return &RelationView{Name: name, AllowedTypes: allowed}
}

func direct(ns string) AllowedType       { return AllowedType{Namespace: ns} }
func userset(ns, rel string) AllowedType { return AllowedType{Namespace: ns, SubRelation: rel} }
func permView(name string, n *PermissionNode) *PermissionView {
	return &PermissionView{Name: name, Rewrite: n, Expressions: flattenPermissionNode(n)}
}

func def(prefixName string, rels []*RelationView, perms ...*PermissionView) *DefinitionView {
	prefix, name := prefixName[:len("t")], prefixName[len("t/"):]
	return &DefinitionView{ObjectType: ObjectType{Prefix: prefix, Name: name}, Relations: rels, Permissions: perms}
}

func TestGrantable_OperatorRules(t *testing.T) {
	defs := []*DefinitionView{def("t/doc",
		[]*RelationView{relView("a", direct("t/user"), direct("t/bot")), relView("b", direct("t/user"), direct("t/group"))},
		permView("both", op(PermOpIntersection, leafIdent("a"), leafIdent("b"))),
		permView("minus", op(PermOpExclusion, leafIdent("a"), leafIdent("b"))),
		permView("either", op(PermOpUnion, leafIdent("a"), leafIdent("b"))),
	)}
	got := grantableInputTypes(defs)
	assert.ElementsMatch(t, []string{"t/user"}, got["t/doc/both"])
	assert.ElementsMatch(t, []string{"t/user", "t/bot"}, got["t/doc/minus"])
	assert.ElementsMatch(t, []string{"t/user", "t/bot", "t/group"}, got["t/doc/either"])
}

// Review of #18: leaf rules must over-approximate, or an intersection
// operand that grants through a userset or a relation-targeting arrow
// would empty the result and falsely mark `User` never granted.
func TestGrantable_NoFalseNeverGranted(t *testing.T) {
	team := def("t/team",
		[]*RelationView{relView("member", direct("t/user")), relView("viewer", direct("t/user"))},
		permView("browse", leafIdent("member")),
	)
	cases := map[string]*DefinitionView{
		// view = allowed & shared, shared: team#member
		"userset operand": def("t/doc",
			[]*RelationView{relView("allowed", direct("t/user")), relView("shared", userset("t/team", "member"))},
			permView("view", op(PermOpIntersection, leafIdent("allowed"), leafIdent("shared"))),
		),
		// view = allowed & parent->browse, parent: team#member
		"arrow over userset-typed relation": def("t/doc",
			[]*RelationView{relView("allowed", direct("t/user")), relView("parent", userset("t/team", "member"))},
			permView("view", op(PermOpIntersection, leafIdent("allowed"), leafArrow("parent", "browse"))),
		),
		// view = allowed & parent->viewer, viewer is a relation on team
		"arrow targeting a relation": def("t/doc",
			[]*RelationView{relView("allowed", direct("t/user")), relView("parent", direct("t/team"))},
			permView("view", op(PermOpIntersection, leafIdent("allowed"), leafArrow("parent", "viewer"))),
		),
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			defs := []*DefinitionView{doc, team}
			assert.Contains(t, grantableInputTypes(defs)["t/doc/view"], "t/user")
			assert.NotContains(t, neverGrantedSets(t, defs), "t/doc/view")
		})
	}
}
