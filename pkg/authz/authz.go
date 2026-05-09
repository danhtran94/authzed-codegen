package authz

import (
	"context"
	"fmt"
	"time"
)

var ErrNoInput = fmt.Errorf("no input")
var ErrPermissionDenied = fmt.Errorf("permission denied")

// WildcardID is the SpiceDB wire-level marker for a wildcard subject
// (relation granted to all subjects of a given type). The codegen
// references this constant rather than hardcoding the literal "*".
const WildcardID ID = "*"

// RelationTuple is the engine-surface representation of a single
// SpiceDB relationship row. Subject ID is untyped at this layer;
// generated code casts it to the typed subject (User, Group, …).
//
//   - SubRelation is empty for direct subjects, non-empty when the row
//     references a userset (e.g. team#admin) per SPEC-006.
//   - CaveatName is the empty string when no caveat is attached.
//   - CaveatContext is nil when no caveat is attached or pre-context is empty.
//   - ExpiresAt is nil when the tuple has no per-tuple TTL. A pointer is used
//     because the zero time.Time{} is a valid past timestamp (0001-01-01)
//     and would be ambiguous with "no expiration set".
type RelationTuple struct {
	ID            ID
	SubRelation   string
	CaveatName    string
	CaveatContext map[string]any
	ExpiresAt     *time.Time
}

type Engine interface {
	CreateRelations(ctx context.Context, to Resource, relation Relation, subject Type, ids []ID) error
	CreateRelationsWithCaveat(ctx context.Context, to Resource, relation Relation, subject Type, ids []ID, caveatName string, caveatParams map[string]any) error
	CreateRelationsWithExpiration(ctx context.Context, to Resource, relation Relation, subject Type, ids []ID, caveatName string, caveatParams map[string]any, expiresAt time.Time) error
	CreateRelationsToUserset(ctx context.Context, to Resource, relation Relation, subject Type, ids []ID, subRelation string, caveatName string, caveatParams map[string]any, expiresAt time.Time) error
	CheckPermission(ctx context.Context, dest Resource, has Permission, subject Type, audIDs []ID) error
	CheckPermissionWithCaveat(ctx context.Context, dest Resource, has Permission, subject Type, audIDs []ID, caveatParams map[string]any) error
	CheckPermissionUserset(ctx context.Context, dest Resource, has Permission, subject Type, audIDs []ID, subRelation string, caveatParams map[string]any) error
	LookupResources(ctx context.Context, from Type, match Permission, subject Type, byIDs []ID) ([]ID, error)
	LookupResourcesWithCaveat(ctx context.Context, from Type, match Permission, subject Type, byIDs []ID, caveatParams map[string]any) ([]ID, error)
	LookupSubjects(ctx context.Context, on Resource, permission Permission, subject Type) ([]ID, error)
	LookupSubjectsWithCaveat(ctx context.Context, on Resource, permission Permission, subject Type, caveatParams map[string]any) ([]ID, error)
	ReadRelations(ctx context.Context, from Resource, relation Relation, subject Type) ([]RelationTuple, error)
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

// IDsOf projects subject IDs from a slice of relation metadata structs.
// Each generated <Rel><Type>Relation struct exposes RelationID() T so the
// constraint is satisfied uniformly. Caller writes authz.IDsOf(rels) and
// type inference resolves T and R from the single positional argument.
func IDsOf[T ~string, R interface{ RelationID() T }](rels []R) []T {
	out := make([]T, len(rels))
	for i, r := range rels {
		out[i] = r.RelationID()
	}
	return out
}

// IDsOfExcludingWildcard is the read-side equivalent of FromIDsExcludingWildcard.
// Returns the input slice with any wildcard tuples removed. Generated Read
// methods filter wildcards before returning a non-wildcard slice — the
// wildcard tuple is surfaced via the sibling Read<Rel><Type>Wildcard method.
func IDsOfExcludingWildcard[T ~string, R interface{ RelationID() T }](rels []R) []R {
	out := make([]R, 0, len(rels))
	for _, r := range rels {
		if ID(r.RelationID()) == WildcardID {
			continue
		}
		out = append(out, r)
	}
	return out
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
