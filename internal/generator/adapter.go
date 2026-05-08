package generator

import (
	"fmt"
	"sort"
	"strings"

	core "github.com/authzed/spicedb/pkg/proto/core/v1"
)

const (
	PermExprIdentifier   = "identifier" // existing
	PermExprArrow        = "arrow"      // existing
	PermExprIntersection = "&"          // NEW
	PermExprExclusion    = "-"          // NEW
)

// ellipsisRelation is the SpiceDB compiler's marker for a plain reference
// (e.g. bookingsvc/employee with no `#sub-relation` suffix). See
// pkg/schemadsl/compiler/translator.go:61.
const ellipsisRelation = "..."

type ObjectType struct {
	Prefix string
	Name   string
}

func (ot ObjectType) String() string {
	return ot.Prefix + "/" + ot.Name
}

type DefinitionView struct {
	ObjectType  ObjectType
	Relations   []*RelationView
	Permissions []*PermissionView
}

type RelationView struct {
	Name         string
	AllowedTypes []AllowedType
}

type AllowedType struct {
	Namespace  string
	IsWildcard bool
	CaveatName string
}

// ParamSpec is a single caveat parameter as the generator sees it: the
// declared name and the inferred Go type. The Go type is best-effort —
// SpiceDB caveat types that don't have a clean Go analogue (any,
// duration, timestamp, map, ipaddress, unknown) fall back to `any` and
// the caller is responsible for passing a structpb-compatible value.
type ParamSpec struct {
	Name   string
	GoType string
}

type PermissionView struct {
	Name        string
	Expressions []PermissionExpr
}

type PermissionExpr struct {
	Kind      string
	Ident     string
	LeftRel   string
	RightPerm string
}

func AdaptDefinitions(caveatDefs []*core.CaveatDefinition, defs []*core.NamespaceDefinition) ([]*DefinitionView, map[string][]ParamSpec, error) {
	caveatMap := buildCaveatMap(caveatDefs)
	out := make([]*DefinitionView, 0, len(defs))
	for _, ns := range defs {
		prefix, name, ok := strings.Cut(ns.GetName(), "/")
		if !ok {
			return nil, nil, fmt.Errorf("definition %q: missing prefix/name separator", ns.GetName())
		}
		d := &DefinitionView{ObjectType: ObjectType{Prefix: prefix, Name: name}}

		for _, r := range ns.GetRelation() {
			switch {
			case r.GetTypeInformation() != nil:
				rv, err := adaptRelation(r)
				if err != nil {
					return nil, nil, fmt.Errorf("definition %q: %w", ns.GetName(), err)
				}
				for _, at := range rv.AllowedTypes {
					if at.CaveatName == "" {
						continue
					}
					if _, ok := caveatMap[at.CaveatName]; !ok {
						return nil, nil, fmt.Errorf("definition %q: relation %q references unknown caveat %q", ns.GetName(), rv.Name, at.CaveatName)
					}
				}
				d.Relations = append(d.Relations, rv)
			case r.GetUsersetRewrite() != nil:
				pv, err := adaptPermission(r)
				if err != nil {
					return nil, nil, fmt.Errorf("definition %q: %w", ns.GetName(), err)
				}
				d.Permissions = append(d.Permissions, pv)
			default:
				return nil, nil, fmt.Errorf("definition %q: relation %q has neither TypeInformation nor UsersetRewrite", ns.GetName(), r.GetName())
			}
		}

		out = append(out, d)
	}
	return out, caveatMap, nil
}

// buildCaveatMap turns SpiceDB's []*CaveatDefinition into a name-keyed
// map of typed parameter specs. Map iteration on ParameterTypes is
// non-deterministic, so the per-caveat slice is sorted by parameter name
// to keep generated output stable across runs.
func buildCaveatMap(caveatDefs []*core.CaveatDefinition) map[string][]ParamSpec {
	out := make(map[string][]ParamSpec, len(caveatDefs))
	for _, cd := range caveatDefs {
		params := make([]ParamSpec, 0, len(cd.GetParameterTypes()))
		for pname, ptype := range cd.GetParameterTypes() {
			params = append(params, ParamSpec{Name: pname, GoType: caveatTypeToGo(ptype)})
		}
		sort.Slice(params, func(i, j int) bool { return params[i].Name < params[j].Name })
		out[cd.GetName()] = params
	}
	return out
}

// collectPermCaveats indexes def/relation structures and walks each
// permission's expressions to find caveats reachable via the relations
// it directly references (or via sibling permission references). Arrows
// (e.g. `parent->browse`) collect the LeftRel's caveats only — caveats
// on the right side resolve through a different object's permission and
// are not part of this Check call's wire Context.
//
// Returns a map keyed by "<defType>/<perm>" → unique caveat name. An
// empty value means the permission has no caveat path. Multi-caveat
// reach errors out — multi-caveat per permission is deferred (AUZ-006
// out-of-scope).
func collectPermCaveats(defs []*DefinitionView) (map[string]string, error) {
	relIdx := make(map[string]map[string][]AllowedType, len(defs))
	permIdx := make(map[string]map[string][]PermissionExpr, len(defs))
	for _, d := range defs {
		ot := d.ObjectType.String()
		relIdx[ot] = make(map[string][]AllowedType, len(d.Relations))
		for _, r := range d.Relations {
			relIdx[ot][r.Name] = r.AllowedTypes
		}
		permIdx[ot] = make(map[string][]PermissionExpr, len(d.Permissions))
		for _, p := range d.Permissions {
			permIdx[ot][p.Name] = p.Expressions
		}
	}

	out := make(map[string]string)
	for _, d := range defs {
		ot := d.ObjectType.String()
		for _, p := range d.Permissions {
			seen := make(map[string]bool)
			walkPermCaveats(ot, p.Name, relIdx, permIdx, seen, map[string]bool{})
			distinct := make([]string, 0, len(seen))
			for c := range seen {
				distinct = append(distinct, c)
			}
			sort.Strings(distinct)
			switch len(distinct) {
			case 0:
			case 1:
				out[fmt.Sprintf("%s/%s", ot, p.Name)] = distinct[0]
			default:
				return nil, fmt.Errorf(
					"permission %s/%s reaches multiple caveats %v — multi-caveat per permission is out of scope (AUZ-006)",
					ot, p.Name, distinct,
				)
			}
		}
	}
	return out, nil
}

func walkPermCaveats(
	ot, permName string,
	relIdx map[string]map[string][]AllowedType,
	permIdx map[string]map[string][]PermissionExpr,
	out map[string]bool,
	visited map[string]bool,
) {
	key := fmt.Sprintf("%s/%s", ot, permName)
	if visited[key] {
		return
	}
	visited[key] = true
	defer delete(visited, key)

	exprs, ok := permIdx[ot][permName]
	if !ok {
		return
	}
	rels := relIdx[ot]
	for _, e := range exprs {
		switch e.Kind {
		case PermExprIdentifier:
			if at, ok := rels[e.Ident]; ok {
				for _, t := range at {
					if t.CaveatName != "" {
						out[t.CaveatName] = true
					}
				}
			} else if _, ok := permIdx[ot][e.Ident]; ok {
				walkPermCaveats(ot, e.Ident, relIdx, permIdx, out, visited)
			}
		case PermExprArrow:
			if at, ok := rels[e.LeftRel]; ok {
				for _, t := range at {
					if t.CaveatName != "" {
						out[t.CaveatName] = true
					}
				}
			}
		}
	}
}

// caveatTypeToGo maps a SpiceDB caveat type to a Go type literal. The
// SpiceDB type set is registered in
// github.com/authzed/spicedb/pkg/caveats/types/basic.go: any, bool,
// string, int, uint, double, bytes, duration, timestamp, list<T>,
// map<T>, ipaddress. Types without a clean Go analogue surface as `any`
// — caller passes a structpb-compatible value at the call site.
func caveatTypeToGo(t *core.CaveatTypeReference) string {
	if t == nil {
		return "any"
	}
	switch t.GetTypeName() {
	case "bool":
		return "bool"
	case "string":
		return "string"
	case "int":
		return "int64"
	case "uint":
		return "uint64"
	case "double":
		return "float64"
	case "bytes":
		return "[]byte"
	case "list":
		children := t.GetChildTypes()
		if len(children) != 1 {
			return "any"
		}
		return "[]" + caveatTypeToGo(children[0])
	default:
		return "any"
	}
}

func adaptRelation(r *core.Relation) (*RelationView, error) {
	types, err := flattenAllowedTypes(r.GetTypeInformation())
	if err != nil {
		return nil, fmt.Errorf("relation %q: %w", r.GetName(), err)
	}
	return &RelationView{Name: r.GetName(), AllowedTypes: types}, nil
}

func flattenAllowedTypes(ti *core.TypeInformation) ([]AllowedType, error) {
	var types []AllowedType

	for _, ar := range ti.GetAllowedDirectRelations() {
		caveatName := ""
		if rc := ar.GetRequiredCaveat(); rc != nil {
			caveatName = rc.GetCaveatName()
		}
		if ar.GetRequiredExpiration() != nil {
			return nil, fmt.Errorf("expiration traits are not supported (allowed type %q)", ar.GetNamespace())
		}

		if rel := ar.GetRelation(); rel != "" && rel != ellipsisRelation {
			return nil, fmt.Errorf("sub-relation references are not supported (%s#%s)", ar.GetNamespace(), rel)
		}

		types = append(types, AllowedType{
			Namespace:  ar.GetNamespace(),
			IsWildcard: ar.GetPublicWildcard() != nil,
			CaveatName: caveatName,
		})
	}
	return types, nil
}

func adaptPermission(r *core.Relation) (*PermissionView, error) {
	exprs, err := lowerUsersetRewrite(r.GetName(), r.GetUsersetRewrite())
	if err != nil {
		return nil, err
	}
	return &PermissionView{Name: r.GetName(), Expressions: exprs}, nil
}

func lowerUsersetRewrite(permName string, rw *core.UsersetRewrite) ([]PermissionExpr, error) {
	// Intersection and exclusion are structurally identical to union for codegen:
	// all children contribute types to a flat set. Treat them identically.
	union := rw.GetUnion()
	if union == nil {
		union = rw.GetIntersection()
	}
	if union == nil {
		union = rw.GetExclusion()
	}
	if union == nil {
		return nil, fmt.Errorf("permission %q: rewrite has no union/intersection/exclusion operation", permName)
	}

	out := make([]PermissionExpr, 0, len(union.GetChild()))
	for _, child := range union.GetChild() {
		e, err := lowerSetOperationChild(permName, child)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func lowerSetOperationChild(permName string, c *core.SetOperation_Child) (PermissionExpr, error) {
	switch {
	case c.GetComputedUserset() != nil:
		return PermissionExpr{
			Kind:  PermExprIdentifier,
			Ident: c.GetComputedUserset().GetRelation(),
		}, nil
	case c.GetTupleToUserset() != nil:
		ttu := c.GetTupleToUserset()
		return PermissionExpr{
			Kind:      PermExprArrow,
			LeftRel:   ttu.GetTupleset().GetRelation(),
			RightPerm: ttu.GetComputedUserset().GetRelation(),
		}, nil
	case c.GetXThis() != nil:
		return PermissionExpr{}, fmt.Errorf("permission %q: legacy _this child is not supported", permName)
	case c.GetXNil() != nil:
		return PermissionExpr{}, fmt.Errorf("permission %q: nil child is not supported", permName)
	case c.GetXSelf() != nil:
		return PermissionExpr{}, fmt.Errorf("permission %q: self child is not supported", permName)
	case c.GetUsersetRewrite() != nil:
		rw := c.GetUsersetRewrite()
		exprs, err := lowerUsersetRewrite(permName, rw)
		if err != nil {
			return PermissionExpr{}, err
		}
		if len(exprs) > 0 {
			return exprs[0], nil
		}
		return PermissionExpr{}, fmt.Errorf("permission %q: userset rewrite child produced no expressions", permName)
	case c.GetFunctionedTupleToUserset() != nil:
		return PermissionExpr{}, fmt.Errorf("permission %q: functioned tuple-to-userset (with self/expiration) is not supported", permName)
	default:
		return PermissionExpr{}, fmt.Errorf("permission %q: unknown rewrite child type", permName)
	}
}
