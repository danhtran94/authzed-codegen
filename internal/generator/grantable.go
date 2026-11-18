package generator

import "fmt"

// grantableInputTypes returns, for every "<type>/<perm>" key, an
// over-approximation of the direct subject types SpiceDB may grant that
// permission to (AUZ-028). A Check<Perm>Inputs field whose type is NOT
// in this set is documented as "never granted", so the set must never
// under-approximate: a type missing here produces a false comment.
//
// For that reason the leaf rules are deliberately more generous than the
// input-field resolver (resolvePermissionExpressionTypes), which only
// lists the fields to emit. Under union a missing leaf type is harmless;
// under intersection it would wrongly empty the result.
//
// Every relation and permission of every definition gets a set:
//
//   - relation:     each allowed type's namespace (wildcards included);
//     for a userset `T#R`, the set of T/R (the members a userset admits)
//   - identifier:   the set of the named relation or permission
//   - arrow rel->x: the set of T/x for every namespace T that rel allows,
//     userset entries included, whether x is a relation or a permission
//   - _self:        the object's own type
//   - union:        union of the children
//   - intersection: intersection of the children
//   - exclusion:    the first (base) child only — later children subtract
//
// Caveats and expiration only ever narrow a grant, so ignoring them keeps
// the set an over-approximation. Recursive definitions are resolved as a
// least fixpoint: every set starts empty and is recomputed until nothing
// changes. Each rule is monotone over a finite set of types, so the
// iteration terminates.
func grantableInputTypes(defs []*DefinitionView) map[string][]string {
	type entry struct {
		objectType string
		relation   *RelationView
		permission *PermissionNode
	}
	entries := map[string]entry{}
	relations := map[string]*RelationView{}
	var keys, permKeys []string
	for _, d := range defs {
		ot := d.ObjectType.String()
		for _, r := range d.Relations {
			k := permKey(ot, r.Name)
			entries[k] = entry{objectType: ot, relation: r}
			relations[k] = r
			keys = append(keys, k)
		}
		for _, p := range d.Permissions {
			k := permKey(ot, p.Name)
			entries[k] = entry{objectType: ot, permission: p.Rewrite}
			keys = append(keys, k)
			permKeys = append(permKeys, k)
		}
	}

	cur := map[string]map[string]bool{}
	for _, k := range keys {
		cur[k] = map[string]bool{}
	}
	union := func(dst, src map[string]bool) {
		for t := range src {
			dst[t] = true
		}
	}

	evalRelation := func(r *RelationView) map[string]bool {
		out := map[string]bool{}
		for _, at := range r.AllowedTypes {
			if at.SubRelation == "" {
				out[at.Namespace] = true
				continue
			}
			union(out, cur[permKey(at.Namespace, at.SubRelation)])
		}
		return out
	}

	var evalNode func(objectType string, n *PermissionNode) map[string]bool
	evalNode = func(objectType string, n *PermissionNode) map[string]bool {
		out := map[string]bool{}
		if n == nil {
			return out
		}
		if n.Leaf != nil {
			e := n.Leaf
			switch e.Kind {
			case PermExprIdentifier:
				union(out, cur[permKey(objectType, e.Ident)])
			case PermExprArrow:
				if left, ok := relations[permKey(objectType, e.LeftRel)]; ok {
					for _, at := range left.AllowedTypes {
						union(out, cur[permKey(at.Namespace, e.RightPerm)])
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
				union(out, evalNode(objectType, c))
			}
		case PermOpIntersection:
			for i, c := range n.Children {
				child := evalNode(objectType, c)
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
				out = evalNode(objectType, n.Children[0])
			}
		}
		return out
	}

	for changed := true; changed; {
		changed = false
		for _, k := range keys {
			e := entries[k]
			var next map[string]bool
			if e.relation != nil {
				next = evalRelation(e.relation)
			} else {
				next = evalNode(e.objectType, e.permission)
			}
			// Sets only grow (monotone rules), so a size change is a change.
			if len(next) != len(cur[k]) {
				cur[k] = next
				changed = true
			}
		}
	}

	result := make(map[string][]string, len(permKeys))
	for _, k := range permKeys {
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
