package generator

import "fmt"

// grantableInputTypes returns, for every "<type>/<perm>" key, the direct
// input types SpiceDB can actually grant that permission to — the
// operator-aware counterpart of GetPermissionTree (AUZ-028).
//
// GetPermissionTree unions the types of every branch regardless of
// operator, so `author - parent->browse` exposes parent->browse's types
// as inputs even though they can only ever reach the excluded side. This
// analysis keeps the resolver's leaf rules exactly and changes only how
// an operator node combines its children:
//
//   - union:        union of the children
//   - intersection: intersection of the children
//   - exclusion:    the first (base) child only — later children subtract
//
// Leaf rules mirror resolvePermissionExpressionTypes / resolveTransitive:
//
//   - identifier naming a relation   → the relation's direct (non-userset) types
//   - identifier naming a permission → that permission on the same type
//   - arrow rel->perm (any / all)    → perm on each type rel allows, if defined there
//   - _self                          → the object's own type
//
// Recursive permissions are resolved as a least fixpoint: every key
// starts empty and is recomputed until nothing changes. Each rule is
// monotone over a finite set of types, so the iteration terminates.
func grantableInputTypes(defs []*DefinitionView, byTypes DefinitionsByTypes) map[string][]string {
	type permRef struct {
		objectType string
		node       *PermissionNode
	}
	perms := map[string]permRef{}
	var keys []string
	for _, d := range defs {
		ot := d.ObjectType.String()
		for _, p := range d.Permissions {
			k := permKey(ot, p.Name)
			perms[k] = permRef{objectType: ot, node: p.Rewrite}
			keys = append(keys, k)
		}
	}

	relationTypes := func(objectType, rel string) []string {
		def, ok := byTypes[objectType]
		if !ok {
			return nil
		}
		_, rels := def.Relations.Get(rel)
		return rels.Types()
	}

	cur := map[string]map[string]bool{}
	for _, k := range keys {
		cur[k] = map[string]bool{}
	}

	var eval func(objectType string, n *PermissionNode) map[string]bool
	eval = func(objectType string, n *PermissionNode) map[string]bool {
		out := map[string]bool{}
		if n == nil {
			return out
		}
		if n.Leaf != nil {
			e := n.Leaf
			switch e.Kind {
			case PermExprIdentifier:
				if def, ok := byTypes[objectType]; ok {
					if ok, rels := def.Relations.Get(e.Ident); ok {
						for _, t := range rels.Types() {
							out[t] = true
						}
						return out
					}
				}
				for t := range cur[permKey(objectType, e.Ident)] {
					out[t] = true
				}
			case PermExprArrow:
				for _, refType := range relationTypes(objectType, e.LeftRel) {
					for t := range cur[permKey(refType, e.RightPerm)] {
						out[t] = true
					}
				}
			case PermExprSelf:
				out[objectType] = true
			}
			return out
		}

		switch n.Op {
		case PermOpUnion:
			for _, c := range n.Children {
				for t := range eval(objectType, c) {
					out[t] = true
				}
			}
		case PermOpIntersection:
			for i, c := range n.Children {
				child := eval(objectType, c)
				if i == 0 {
					out = child
					continue
				}
				for t := range out {
					if !child[t] {
						delete(out, t)
					}
				}
			}
		case PermOpExclusion:
			if len(n.Children) > 0 {
				out = eval(objectType, n.Children[0])
			}
		}
		return out
	}

	for changed := true; changed; {
		changed = false
		for _, k := range keys {
			next := eval(perms[k].objectType, perms[k].node)
			if len(next) != len(cur[k]) {
				cur[k] = next
				changed = true
			}
		}
	}

	result := make(map[string][]string, len(keys))
	for _, k := range keys {
		types := make([]string, 0, len(cur[k]))
		for t := range cur[k] {
			types = append(types, t)
		}
		result[k] = types
	}
	return result
}

func permKey(objectType, perm string) string {
	return fmt.Sprintf("%s/%s", objectType, perm)
}
