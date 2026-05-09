package authz

import (
	"context"
	"errors"
	"fmt"
	"time"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
)

var ErrNoInput = fmt.Errorf("no input")
var ErrPermissionDenied = fmt.Errorf("permission denied")

// ErrConditionalPermission is the sentinel returned (via wrapping) when
// SpiceDB indicates PERMISSIONSHIP_CONDITIONAL_PERMISSION — the permission
// would be granted IF the caller supplied additional caveat context.
//
// Backward compat: errors satisfying this sentinel ALSO satisfy
// ErrPermissionDenied via *ConditionalPermissionError's custom Is method,
// so existing code matching `errors.Is(err, ErrPermissionDenied)` keeps
// working. New code can additionally check for ErrConditionalPermission
// and use errors.As to extract a *ConditionalPermissionError carrying the
// caveat parameter names the caller forgot to supply.
var ErrConditionalPermission = errors.New("conditional permission")

// ConditionalPermissionError wraps SpiceDB's PartialCaveatInfo signal.
// MissingKeys is the list of caveat parameter names the caller should
// fetch and retry with — directly from PartialCaveatInfo.MissingRequiredContext.
// May be empty when SpiceDB returns CONDITIONAL without a specific recovery
// hint (e.g. CEL evaluator returned indeterminate for an ambiguous expression).
type ConditionalPermissionError struct {
	MissingKeys []string
}

func (e *ConditionalPermissionError) Error() string {
	return fmt.Sprintf("conditional permission: missing %v", e.MissingKeys)
}

// Is matches both ErrConditionalPermission (rich-signal path) and
// ErrPermissionDenied (backward-compat path). See package docs.
func (e *ConditionalPermissionError) Is(target error) bool {
	return target == ErrConditionalPermission || target == ErrPermissionDenied
}

// WildcardID is the SpiceDB wire-level marker for a wildcard subject
// (relation granted to all subjects of a given type). The codegen
// references this constant rather than hardcoding the literal "*".
const WildcardID ID = "*"

// SchemaDrift is the result of comparing the codegen-baseline schema
// against the currently deployed schema in SpiceDB. Buckets the raw
// ReflectionSchemaDiff entries by severity:
//
//   - Added/Cosmetic are safe (deployed schema is ahead or just doc changes)
//   - Removed/Changed are breaking (deployed schema lacks something the
//     binary depends on, or evaluates differently)
//
// Use IsBreaking() at startup to decide fail-fast vs log-and-continue.
type SchemaDrift struct {
	Added    []DriftEntry
	Removed  []DriftEntry
	Changed  []DriftEntry
	Cosmetic []DriftEntry
}

// IsBreaking reports whether any breaking drift exists (Removed or Changed
// entries). Caller typically hard-fails at startup when this is true.
func (d SchemaDrift) IsBreaking() bool {
	return len(d.Removed) > 0 || len(d.Changed) > 0
}

// IsClean reports whether the deployed schema matches the codegen baseline
// exactly across all four buckets.
func (d SchemaDrift) IsClean() bool {
	return len(d.Added)+len(d.Removed)+len(d.Changed)+len(d.Cosmetic) == 0
}

// DriftEntry is one row of drift. Description is human-readable for logs;
// Raw exposes the typed wire-level diff for callers needing programmatic
// access to specific oneof variants (e.g. PermissionExprChanged.Old / .New).
type DriftEntry struct {
	Description string
	Raw         *v1.ReflectionSchemaDiff
}

// ConsistencyMode controls how strongly read-side methods (Check, Lookup,
// Read) observe writes when evaluating against SpiceDB. Set per-call via
// WithConsistency(ctx, mode); the *spicedb.Engine reads the override
// internally. Per-call override; engine's existing recent-token logic
// remains the default.
type ConsistencyMode int

const (
	// ConsistencyDefault preserves the engine's existing behavior: pin to
	// AtExactSnapshot when a recent write token is available (within the
	// engine's durationExpire window), otherwise fall through to SpiceDB's
	// MinimumLatency default. Optimised for read-your-own-writes.
	ConsistencyDefault ConsistencyMode = 0

	// ConsistencyFullyConsistent forces SpiceDB to evaluate against the
	// most up-to-date data, bypassing any cached snapshot. Slower than
	// default; required for security-sensitive checks where stale reads
	// are unacceptable AND for any check that depends on wall-clock
	// semantics like expiration filtering on userset tuples (per
	// AUZ-011 Discoveries).
	ConsistencyFullyConsistent ConsistencyMode = 1
)

// consistencyKey is the unexported context-value key for the
// ConsistencyMode override. The struct{} type prevents external packages
// from constructing a colliding key.
type consistencyKey struct{}

// WithConsistency returns a derived context carrying the consistency mode
// override. Engine read-side methods (Check, Lookup, Read) honor the
// override transparently — no codegen-method signature change. Caller
// scope it at the request boundary; downstream calls inherit.
func WithConsistency(ctx context.Context, mode ConsistencyMode) context.Context {
	return context.WithValue(ctx, consistencyKey{}, mode)
}

// GetConsistency returns the consistency mode set on the context, or
// ConsistencyDefault if not set. Engine impls call this from
// getConsistencySnapshot to drive the per-call wire selection.
func GetConsistency(ctx context.Context) ConsistencyMode {
	if mode, ok := ctx.Value(consistencyKey{}).(ConsistencyMode); ok {
		return mode
	}
	return ConsistencyDefault
}

// LookupResult is the return value of every Engine.Lookup* method.
// Definite holds resource/subject IDs the caller has confirmed access to.
// Conditional holds entries that would be granted IF the caller supplies
// the named missing keys; treating these as confirmed is unsafe — callers
// fetch the missing context and retry the Check, or filter Conditional out
// entirely when only definite grants matter.
//
// Both slices are explicitly initialised to empty (not nil) by the engine
// impl so callers can range over either field unconditionally.
type LookupResult struct {
	Definite    []ID
	Conditional []LookupConditionalEntry
}

// LookupConditionalEntry surfaces SpiceDB's PartialCaveatInfo for a single
// conditional Lookup row. MissingKeys is the caveat parameter names from
// PartialCaveatInfo.MissingRequiredContext — directly off the wire. May be
// empty when SpiceDB returns CONDITIONAL without a specific recovery hint
// (CEL evaluator returned indeterminate for an ambiguous expression).
type LookupConditionalEntry struct {
	ID          ID
	MissingKeys []string
}

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
	LookupResources(ctx context.Context, from Type, match Permission, subject Type, byIDs []ID) (LookupResult, error)
	LookupResourcesWithCaveat(ctx context.Context, from Type, match Permission, subject Type, byIDs []ID, caveatParams map[string]any) (LookupResult, error)
	LookupSubjects(ctx context.Context, on Resource, permission Permission, subject Type) (LookupResult, error)
	LookupSubjectsWithCaveat(ctx context.Context, on Resource, permission Permission, subject Type, caveatParams map[string]any) (LookupResult, error)
	ReadRelations(ctx context.Context, from Resource, relation Relation, subject Type) ([]RelationTuple, error)
	DeleteRelations(ctx context.Context, from Resource, relation Relation, subject Type, ids []ID) error
	HasPublicRelation(ctx context.Context, on Resource, relation Relation, subject Type) (bool, error)
	HasPublicSubject(ctx context.Context, on Resource, permission Permission, subject Type) (bool, error)
	DiffSchema(ctx context.Context, comparisonSchema string) ([]*v1.ReflectionSchemaDiff, error)
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
