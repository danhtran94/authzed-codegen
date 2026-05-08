package authz

import (
	"context"
	"fmt"
)

var ErrNoInput = fmt.Errorf("no input")
var ErrPermissionDenied = fmt.Errorf("permission denied")

// WildcardID is the SpiceDB wire-level marker for a wildcard subject
// (relation granted to all subjects of a given type). The codegen
// references this constant rather than hardcoding the literal "*".
const WildcardID ID = "*"

type Engine interface {
	CreateRelations(ctx context.Context, to Resource, relation Relation, subject Type, ids []ID) error
	CreateRelationsWithCaveat(ctx context.Context, to Resource, relation Relation, subject Type, ids []ID, caveatName string, caveatParams map[string]any) error
	CheckPermission(ctx context.Context, dest Resource, has Permission, subject Type, audIDs []ID) error
	CheckPermissionWithCaveat(ctx context.Context, dest Resource, has Permission, subject Type, audIDs []ID, caveatParams map[string]any) error
	LookupResources(ctx context.Context, from Type, match Permission, subject Type, byIDs []ID) ([]ID, error)
	LookupSubjects(ctx context.Context, on Resource, permission Permission, subject Type) ([]ID, error)
	ReadRelations(ctx context.Context, from Resource, relation Relation, subject Type) ([]ID, error)
	DeleteRelations(ctx context.Context, from Resource, relation Relation, subject Type, ids []ID) error
	HasPublicRelation(ctx context.Context, on Resource, relation Relation, subject Type) (bool, error)
	HasPublicSubject(ctx context.Context, on Resource, permission Permission, subject Type) (bool, error)
}

var DefaultEngine Engine = nil

func SetDefaultEngine(engine Engine) {
	DefaultEngine = engine
}

func GetEngine(ctx context.Context) Engine {
	return DefaultEngine
}

type Type string

type ID string

type Permission string

type Relation string

type Subject struct {
	Type Type
	IDs  []ID
}

type Resource struct {
	Type Type
	ID   ID
}

func IDs[T ~string](ids []T) []ID {
	result := []ID{}

	for _, id := range ids {
		result = append(result, ID(id))
	}

	return result
}

func FromIDs[T ~string](ids []ID) []T {
	result := []T{}

	for _, id := range ids {
		result = append(result, T(id))
	}

	return result
}

// FromIDsExcludingWildcard converts an authz.ID slice to typed []T,
// dropping the wildcard sentinel WildcardID. Generated code uses this
// for read paths that surface concrete IDs only — the wildcard state
// is exposed through the sibling Read<Rel><Type>Wildcard / Lookup<Perm>
// <Type>WildcardSubjects methods (per ADR-003).
func FromIDsExcludingWildcard[T ~string](ids []ID) []T {
	result := []T{}

	for _, id := range ids {
		if id == WildcardID {
			continue
		}
		result = append(result, T(id))
	}

	return result
}

type StringConvertable interface {
	String() string
}

func Stringer[T ~string](id StringConvertable) T {
	return T(id.String())
}

func Stringers[T ~string](ids []StringConvertable) []T {
	result := []T{}

	for _, id := range ids {
		result = append(result, T(id.String()))
	}

	return result
}
