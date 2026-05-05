package generator

import (
	"fmt"
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

func AdaptDefinitions(defs []*core.NamespaceDefinition) ([]*DefinitionView, error) {
	out := make([]*DefinitionView, 0, len(defs))
	for _, ns := range defs {
		prefix, name, ok := strings.Cut(ns.GetName(), "/")
		if !ok {
			return nil, fmt.Errorf("definition %q: missing prefix/name separator", ns.GetName())
		}
		d := &DefinitionView{ObjectType: ObjectType{Prefix: prefix, Name: name}}

		for _, r := range ns.GetRelation() {
			switch {
			case r.GetTypeInformation() != nil:
				rv, err := adaptRelation(r)
				if err != nil {
					return nil, fmt.Errorf("definition %q: %w", ns.GetName(), err)
				}
				d.Relations = append(d.Relations, rv)
			case r.GetUsersetRewrite() != nil:
				pv, err := adaptPermission(r)
				if err != nil {
					return nil, fmt.Errorf("definition %q: %w", ns.GetName(), err)
				}
				d.Permissions = append(d.Permissions, pv)
			default:
				return nil, fmt.Errorf("definition %q: relation %q has neither TypeInformation nor UsersetRewrite", ns.GetName(), r.GetName())
			}
		}

		out = append(out, d)
	}
	return out, nil
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
		if ar.GetRequiredCaveat() != nil {
			return nil, fmt.Errorf("caveats are not supported (allowed type %q)", ar.GetNamespace())
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
