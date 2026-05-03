package authz

import (
	"context"
	"fmt"
)

var ErrNoInput = fmt.Errorf("no input")
var ErrPermissionDenied = fmt.Errorf("permission denied")

type Engine interface {
	CreateRelations(ctx context.Context, to Resource, relation Relation, subject Type, ids []ID) error
	CheckPermission(ctx context.Context, dest Resource, has Permission, subject Type, audIDs []ID) error
	LookupResources(ctx context.Context, from Type, match Permission, subject Type, byIDs []ID) ([]ID, error)
	LookupSubjects(ctx context.Context, on Resource, permission Permission, subject Type) ([]ID, error)
	ReadRelations(ctx context.Context, from Resource, relation Relation, subject Type) ([]ID, error)
	DeleteRelations(ctx context.Context, from Resource, relation Relation, subject Type, ids []ID) error
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
