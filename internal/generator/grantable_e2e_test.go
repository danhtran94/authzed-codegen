package generator

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/danhtran94/authzed-codegen/v2/pkg/authz"
	"github.com/danhtran94/authzed-codegen/v2/pkg/authz/spicedbtest"
)

// grantableSchema covers the shapes where an under-approximating analysis
// would emit a false "never granted" comment (review of #18), plus one
// genuinely never-granted intersection. Each permission intersects with
// `allowed` so a missing leaf type would empty the result.
const grantableSchema = `
definition tst/user {}
definition tst/group {}
definition tst/team {
  relation member: tst/user
  relation viewer: tst/user
  permission browse = member
}
definition tst/doc {
  relation allowed: tst/user
  relation shared: tst/team#member
  relation parent_us: tst/team#member
  relation parent: tst/team
  relation groups: tst/group
  relation banned: tst/user

  permission via_userset = allowed & shared
  permission via_arrow_userset = allowed & parent_us->browse
  permission via_arrow_relation = allowed & parent->viewer
  permission via_exclusion = allowed - banned
  permission disjoint = allowed & groups
}
`

// TestGrantable_AgreesWithSpiceDB is the cross-check behind the generated
// "never granted" comments (AUZ-028): every subject SpiceDB actually
// grants must have its type in grantableInputTypes, and no type that
// carries a comment may ever be granted. Each case also pins SpiceDB's
// answer, so a semantic change upstream fails here first.
func TestGrantable_AgreesWithSpiceDB(t *testing.T) {
	ctx := context.Background()
	defs, _ := adaptSchema(t, "grantable_e2e.zed", grantableSchema)
	grantable := grantableInputTypes(defs)
	never := neverGrantedSets(t, defs)

	sb, err := spicedbtest.Start(ctx, grantableSchema)
	if errors.Is(err, spicedbtest.ErrDockerUnavailable) {
		t.Skip(err)
	}
	require.NoError(t, err)
	defer func() { _ = sb.Close(ctx) }()
	e := sb.Engine

	doc := authz.Resource{Type: "tst/doc", ID: "d1"}
	team := authz.Resource{Type: "tst/team", ID: "t1"}
	ids := func(s ...string) []authz.ID {
		out := make([]authz.ID, len(s))
		for i, v := range s {
			out[i] = authz.ID(v)
		}
		return out
	}
	// alice: allowed + team member/viewer; bob: allowed only; carol: allowed + banned.
	require.NoError(t, e.CreateRelations(ctx, doc, "allowed", "tst/user", ids("alice", "bob", "carol")))
	require.NoError(t, e.CreateRelations(ctx, doc, "banned", "tst/user", ids("carol")))
	require.NoError(t, e.CreateRelations(ctx, team, "member", "tst/user", ids("alice")))
	require.NoError(t, e.CreateRelations(ctx, team, "viewer", "tst/user", ids("alice")))
	require.NoError(t, e.CreateRelationsToUserset(ctx, doc, "shared", "tst/team", ids("t1"), "member", "", nil, time.Time{}))
	require.NoError(t, e.CreateRelationsToUserset(ctx, doc, "parent_us", "tst/team", ids("t1"), "member", "", nil, time.Time{}))
	require.NoError(t, e.CreateRelations(ctx, doc, "parent", "tst/team", ids("t1")))
	require.NoError(t, e.CreateRelations(ctx, doc, "groups", "tst/group", ids("g1")))

	cases := []struct {
		perm, subjectType, id string
		granted               bool
	}{
		{"via_userset", "tst/user", "alice", true},
		{"via_userset", "tst/user", "bob", false},
		{"via_arrow_userset", "tst/user", "alice", true},
		{"via_arrow_userset", "tst/user", "bob", false},
		{"via_arrow_relation", "tst/user", "alice", true},
		{"via_arrow_relation", "tst/user", "bob", false},
		{"via_exclusion", "tst/user", "bob", true},
		{"via_exclusion", "tst/user", "carol", false},
		// Genuinely never granted: user misses `groups`, group misses `allowed`.
		{"disjoint", "tst/user", "alice", false},
		{"disjoint", "tst/group", "g1", false},
	}
	for _, c := range cases {
		t.Run(c.perm+"/"+c.id, func(t *testing.T) {
			key := "tst/doc/" + c.perm
			err := e.CheckPermission(ctx, doc, authz.Permission(c.perm), authz.Type(c.subjectType), ids(c.id))
			if err != nil && !errors.Is(err, authz.ErrPermissionDenied) {
				require.NoError(t, err)
			}
			granted := err == nil
			assert.Equal(t, c.granted, granted, "SpiceDB verdict")

			if granted {
				assert.Contains(t, grantable[key], c.subjectType, "a type SpiceDB grants must be grantable")
				assert.False(t, slices.Contains(never[key], c.subjectType), "a type SpiceDB grants must not be commented never granted")
			}
		})
	}

	// The comments emitted for this schema are exactly the disjoint ones.
	assert.Equal(t, map[string][]string{
		"tst/doc/disjoint": {"tst/group", "tst/user"},
	}, never)
}
