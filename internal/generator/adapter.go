package generator

import (
	"fmt"
	"sort"
	"strings"

	core "github.com/authzed/spicedb/pkg/proto/core/v1"

	"github.com/danhtran94/authzed-codegen/internal/utilstr"
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

	// IDFieldName is the Go field name used in the generated <Rel>Objects
	// struct for the ID slice of this allowed type, and in the Wildcards
	// sub-struct for the wildcard bool. For non-colliding allowed types
	// it equals utilstr.TypeName(Namespace) — the existing convention.
	// When two AllowedDirectRelations share (Namespace, IsWildcard) but
	// declare different caveats, the codegen disambiguates by appending
	// the caveat's PascalCase name so each branch has its own field
	// (e.g. UserCavA, UserCavB) — see flattenAllowedTypes.
	IDFieldName string

	// CaveatFieldName is the Go field name in the <Rel>Caveats sub-struct
	// for this allowed type's caveat pointer. Same disambiguation rules
	// as IDFieldName. Empty when the allowed type has no caveat.
	CaveatFieldName string
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
// Returns a map keyed by "<defType>/<perm>" → sorted slice of unique
// caveat names reachable from the permission. An entry is omitted when
// the permission has no caveat path. Multiple distinct caveats are
// allowed (the codegen emits a Caveats sub-struct on Check<Perm>Inputs
// with one field per reachable caveat); the wire-level Context is a
// merged key bag where SpiceDB matches keys to whichever tuple's
// caveat needs them. Cross-caveat parameter-name collisions are
// detected separately by detectPermCaveatCollisions.
func collectPermCaveats(defs []*DefinitionView) map[string][]string {
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

	out := make(map[string][]string)
	for _, d := range defs {
		ot := d.ObjectType.String()
		for _, p := range d.Permissions {
			seen := make(map[string]bool)
			walkPermCaveats(ot, p.Name, relIdx, permIdx, seen, map[string]bool{})
			if len(seen) == 0 {
				continue
			}
			distinct := make([]string, 0, len(seen))
			for c := range seen {
				distinct = append(distinct, c)
			}
			sort.Strings(distinct)
			out[fmt.Sprintf("%s/%s", ot, p.Name)] = distinct
		}
	}
	return out
}

// detectPermCaveatCollisions walks every permission that reaches 2+
// distinct caveats and errors if any two of those caveats declare the
// same parameter name. Reason: the wire Context on
// CheckPermissionRequest is a single shared key-bag — when two caveats
// claim the same key, the codegen has no way to disambiguate at call
// time. The schema author resolves by renaming in one of the caveats.
func detectPermCaveatCollisions(permCaveats map[string][]string, caveatMap map[string][]ParamSpec) error {
	for permKey, caveatNames := range permCaveats {
		if len(caveatNames) < 2 {
			continue
		}
		paramOrigin := make(map[string]string)
		for _, cn := range caveatNames {
			for _, p := range caveatMap[cn] {
				if owner, ok := paramOrigin[p.Name]; ok && owner != cn {
					return fmt.Errorf(
						"permission %s reaches caveats %q and %q which both declare parameter %q — rename one in the schema to disambiguate",
						permKey, owner, cn, p.Name,
					)
				}
				paramOrigin[p.Name] = cn
			}
		}
	}
	return nil
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

// caveatTypeToGo maps a SpiceDB caveat type to the Go type literal
// emitted on a generated caveat Args struct field. Scalars are wrapped
// in a pointer so callers can express "defer this parameter to check
// time" via nil; container types (slices, maps, bytes) are left as-is
// because they are naturally nilable in Go. The element types inside
// list<T> are unwrapped (no pointer) — those are values inside the
// slice, not optional fields.
//
// The SpiceDB type set is registered in
// github.com/authzed/spicedb/pkg/caveats/types/basic.go: any, bool,
// string, int, uint, double, bytes, duration, timestamp, list<T>,
// map<T>, ipaddress. Types without a clean Go analogue surface as
// `any` — caller passes a structpb-compatible value at the call site.
func caveatTypeToGo(t *core.CaveatTypeReference) string {
	if t == nil {
		return "any"
	}
	switch t.GetTypeName() {
	case "bool":
		return "*bool"
	case "string":
		return "*string"
	case "int":
		return "*int"
	case "uint":
		return "*uint"
	case "double":
		return "*float64"
	case "bytes":
		return "[]byte"
	case "list":
		children := t.GetChildTypes()
		if len(children) != 1 {
			return "any"
		}
		return "[]" + caveatTypeToGoElem(children[0])
	default:
		return "any"
	}
}

// caveatTypeToGoElem returns the Go element type for use inside a
// slice — never wrapped in pointer. Used by caveatTypeToGo when
// recursing into list<T> children: the inner type is a value sitting
// inside the slice, not an optional field that needs nullability.
func caveatTypeToGoElem(t *core.CaveatTypeReference) string {
	if t == nil {
		return "any"
	}
	switch t.GetTypeName() {
	case "bool":
		return "bool"
	case "string":
		return "string"
	case "int":
		return "int"
	case "uint":
		return "uint"
	case "double":
		return "float64"
	case "bytes":
		return "[]byte"
	case "list":
		children := t.GetChildTypes()
		if len(children) != 1 {
			return "any"
		}
		return "[]" + caveatTypeToGoElem(children[0])
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

	// Compute IDFieldName and CaveatFieldName for each allowed type.
	// Singletons keep the existing typeName-based convention; entries
	// that collide on (Namespace, IsWildcard) get the caveat suffix
	// appended so each branch has its own struct field. SpiceDB's
	// tuple identity is (resource, relation, subject) — caveat is
	// metadata, not part of the key — so `relation foo: user with
	// cav_a | user with cav_b` means "a user-tuple can be written with
	// EITHER cav_a or cav_b." The disambiguated field names let the
	// caller pick per-batch which caveat applies.
	//
	// Pure duplicates (same Namespace + IsWildcard + identical caveat,
	// or both un-caveated) are still rejected — no semantic
	// disambiguation is possible.
	groupIdxs := make(map[string][]int, len(types))
	for i, t := range types {
		key := fmt.Sprintf("%s|wildcard=%v", t.Namespace, t.IsWildcard)
		groupIdxs[key] = append(groupIdxs[key], i)
	}
	for _, idxs := range groupIdxs {
		if len(idxs) == 1 {
			t := &types[idxs[0]]
			t.IDFieldName = utilstr.TypeName(t.Namespace)
			if t.CaveatName != "" {
				t.CaveatFieldName = t.IDFieldName
			}
			continue
		}
		caveats := make(map[string]bool, len(idxs))
		anyEmpty := false
		for _, i := range idxs {
			caveats[types[i].CaveatName] = true
			if types[i].CaveatName == "" {
				anyEmpty = true
			}
		}
		if anyEmpty || len(caveats) != len(idxs) {
			return nil, fmt.Errorf(
				"duplicate allowed type %q — multiple AllowedDirectRelations with same namespace must each declare a distinct caveat to be disambiguated",
				types[idxs[0]].Namespace,
			)
		}
		for _, i := range idxs {
			t := &types[i]
			disamb := utilstr.TypeName(t.Namespace) + pascalCaveatName(t.CaveatName)
			t.IDFieldName = disamb
			t.CaveatFieldName = disamb
		}
	}

	return types, nil
}

// pascalCaveatName mirrors the template's pascalCaveat helper —
// strips the schema prefix from the caveat name and PascalCases the
// local part. Lives here so the adapter can pre-compute disambiguated
// field names without taking a dependency on the template's FuncMap.
func pascalCaveatName(name string) string {
	local := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		local = name[i+1:]
	}
	return utilstr.SnakeToPascal(local)
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
