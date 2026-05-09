package spicedb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/danhtran94/authzed-codegen/pkg/authz"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	"github.com/authzed/authzed-go/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Engine struct {
	client *authzed.Client

	production bool

	durationExpire time.Duration
	token          string
	setTokenTime   int64
}

var _ authz.Engine = &Engine{}

func NewEngine(client *authzed.Client, durationExpireToken time.Duration) *Engine {
	if durationExpireToken == 0 {
		durationExpireToken = 3 * time.Second
	}

	return &Engine{
		client:         client,
		durationExpire: durationExpireToken,
	}
}

func (e *Engine) debugLog(format string, args ...interface{}) {
	if !e.production {
		fmt.Printf("[DEBUG] "+format+"\n", args...)
	}
}

func (e *Engine) SetProduction(yes bool) {
	e.production = yes
}

func (e *Engine) setToken(token string) {
	e.debugLog("Setting token: %s", token)
	e.token = token
	e.setTokenTime = time.Now().UnixNano()
}

// SetSnapshotToken advances the engine's stored ZedToken so subsequent
// reads pin to that snapshot. Use this when a write happens outside
// the engine's own write methods (e.g. tests that write a caveated
// relationship via the underlying authzed-go client because the
// codegen does not yet emit write-time caveat attachment, AUZ-006).
// Without this, snapshot reads would see a pre-write view and mask
// the new tuple.
func (e *Engine) SetSnapshotToken(token string) {
	e.setToken(token)
}

func (e *Engine) getConsistencySnapshot() *v1.Consistency {
	now := time.Now().UnixNano()
	if now-e.setTokenTime > e.durationExpire.Nanoseconds() {
		e.debugLog("Using default consistency")
		return nil
	}

	e.debugLog("Using consistency snapshot with token: %s", e.token)
	return &v1.Consistency{
		Requirement: &v1.Consistency_AtExactSnapshot{
			AtExactSnapshot: &v1.ZedToken{
				Token: e.token,
			},
		},
	}
}

// CreateRelationsWithExpiration writes per-tuple OptionalExpiresAt
// timestamps via OPERATION_TOUCH. Optionally also attaches a caveat
// when caveatName != "" (covers `with cav and expiration` schemas).
// TOUCH is required because un-garbage-collected expired tuples may
// collide on tuple identity, and OPERATION_CREATE errors on existing
// tuples (per SPEC-004 A2 — Authzed docs).
func (e *Engine) CreateRelationsWithExpiration(ctx context.Context, to authz.Resource, relation authz.Relation, subject authz.Type, ids []authz.ID, caveatName string, caveatParams map[string]any, expiresAt time.Time) error {
	e.debugLog("Creating expiring relations: to=%v, relation=%v, subject=%v, ids=%v, caveat=%s, params=%v, expiresAt=%v", to, relation, subject, ids, caveatName, caveatParams, expiresAt)

	var caveatCtx *structpb.Struct
	if caveatName != "" {
		var err error
		caveatCtx, err = serializeCaveatMap(caveatParams)
		if err != nil {
			return fmt.Errorf("serialize caveat params: %w", err)
		}
	}

	expiresPb := timestamppb.New(expiresAt)

	updates := make([]*v1.RelationshipUpdate, 0, len(ids))
	for _, id := range ids {
		rel := &v1.Relationship{
			Resource: &v1.ObjectReference{
				ObjectType: string(to.Type),
				ObjectId:   string(to.ID),
			},
			Relation: string(relation),
			Subject: &v1.SubjectReference{
				Object: &v1.ObjectReference{
					ObjectType: string(subject),
					ObjectId:   string(id),
				},
			},
			OptionalExpiresAt: expiresPb,
		}
		if caveatName != "" {
			rel.OptionalCaveat = &v1.ContextualizedCaveat{
				CaveatName: caveatName,
				Context:    caveatCtx,
			}
		}
		updates = append(updates, &v1.RelationshipUpdate{
			Operation:    v1.RelationshipUpdate_OPERATION_TOUCH,
			Relationship: rel,
		})
	}

	res, err := e.client.WriteRelationships(ctx, &v1.WriteRelationshipsRequest{Updates: updates})
	if err != nil {
		return err
	}
	e.setToken(res.WrittenAt.Token)
	return nil
}

func (e *Engine) CreateRelationsWithCaveat(ctx context.Context, to authz.Resource, relation authz.Relation, subject authz.Type, ids []authz.ID, caveatName string, caveatParams map[string]any) error {
	e.debugLog("Creating caveated relations: to=%v, relation=%v, subject=%v, ids=%v, caveat=%s, params=%v", to, relation, subject, ids, caveatName, caveatParams)

	caveatCtx, err := serializeCaveatMap(caveatParams)
	if err != nil {
		return fmt.Errorf("serialize caveat params: %w", err)
	}

	updates := make([]*v1.RelationshipUpdate, 0, len(ids))
	for _, id := range ids {
		updates = append(updates, &v1.RelationshipUpdate{
			Operation: v1.RelationshipUpdate_OPERATION_CREATE,
			Relationship: &v1.Relationship{
				Resource: &v1.ObjectReference{
					ObjectType: string(to.Type),
					ObjectId:   string(to.ID),
				},
				Relation: string(relation),
				Subject: &v1.SubjectReference{
					Object: &v1.ObjectReference{
						ObjectType: string(subject),
						ObjectId:   string(id),
					},
				},
				OptionalCaveat: &v1.ContextualizedCaveat{
					CaveatName: caveatName,
					Context:    caveatCtx,
				},
			},
		})
	}

	res, err := e.client.WriteRelationships(ctx, &v1.WriteRelationshipsRequest{Updates: updates})
	if err != nil {
		return err
	}
	e.setToken(res.WrittenAt.Token)
	return nil
}

func (e *Engine) CreateRelations(ctx context.Context, to authz.Resource, relation authz.Relation, subject authz.Type, ids []authz.ID) error {
	e.debugLog("Creating relations: to=%v, relation=%v, subject=%v, ids=%v", to, relation, subject, ids)
	updates := make([]*v1.RelationshipUpdate, 0, len(ids))
	for _, id := range ids {
		e.debugLog("Processing id: %v", id) // Added debug log
		updates = append(updates, &v1.RelationshipUpdate{
			Operation: v1.RelationshipUpdate_OPERATION_CREATE,
			Relationship: &v1.Relationship{
				Resource: &v1.ObjectReference{
					ObjectType: string(to.Type),
					ObjectId:   string(to.ID),
				},
				Relation: string(relation),
				Subject: &v1.SubjectReference{
					Object: &v1.ObjectReference{
						ObjectType: string(subject),
						ObjectId:   string(id),
					},
				},
			},
		})
	}

	res, err := e.client.WriteRelationships(ctx, &v1.WriteRelationshipsRequest{
		Updates: updates,
	})
	if err != nil {
		return err
	}

	e.setToken(res.WrittenAt.Token)

	return nil
}

// CreateRelationsToUserset writes userset references — `Subject.OptionalRelation`
// is set to subRelation, identifying the row as a reference to another resource's
// relation/permission rather than a direct subject. Always issues OPERATION_TOUCH:
// userset writes share the expired-collision concern from AUZ-009 expiration writes,
// and TOUCH is idempotent so the cost is zero for non-expiring writes (per SPEC-006 C2/A3).
//
// Sentinel parameters compress 4 userset combinations into one method:
//   - Plain userset: caveatName == "", caveatParams == nil, expiresAt zero
//   - + caveat: caveatName != "" (caveatParams optional — nil defers to check time)
//   - + expiration: expiresAt non-zero
//   - + caveat + expiration: both
func (e *Engine) CreateRelationsToUserset(ctx context.Context, to authz.Resource, relation authz.Relation, subject authz.Type, ids []authz.ID, subRelation string, caveatName string, caveatParams map[string]any, expiresAt time.Time) error {
	e.debugLog("Creating userset relations: to=%v, relation=%v, subject=%v#%v, ids=%v, caveat=%s, expiresAt=%v", to, relation, subject, subRelation, ids, caveatName, expiresAt)

	var caveatCtx *structpb.Struct
	if caveatName != "" {
		var err error
		caveatCtx, err = serializeCaveatMap(caveatParams)
		if err != nil {
			return fmt.Errorf("serialize caveat params: %w", err)
		}
	}

	var expiresPb *timestamppb.Timestamp
	if !expiresAt.IsZero() {
		expiresPb = timestamppb.New(expiresAt)
	}

	updates := make([]*v1.RelationshipUpdate, 0, len(ids))
	for _, id := range ids {
		rel := &v1.Relationship{
			Resource: &v1.ObjectReference{
				ObjectType: string(to.Type),
				ObjectId:   string(to.ID),
			},
			Relation: string(relation),
			Subject: &v1.SubjectReference{
				Object: &v1.ObjectReference{
					ObjectType: string(subject),
					ObjectId:   string(id),
				},
				OptionalRelation: subRelation,
			},
		}
		if caveatName != "" {
			rel.OptionalCaveat = &v1.ContextualizedCaveat{CaveatName: caveatName, Context: caveatCtx}
		}
		if expiresPb != nil {
			rel.OptionalExpiresAt = expiresPb
		}
		updates = append(updates, &v1.RelationshipUpdate{
			Operation:    v1.RelationshipUpdate_OPERATION_TOUCH,
			Relationship: rel,
		})
	}

	res, err := e.client.WriteRelationships(ctx, &v1.WriteRelationshipsRequest{Updates: updates})
	if err != nil {
		return err
	}
	e.setToken(res.WrittenAt.Token)
	return nil
}

func (e *Engine) DeleteRelations(ctx context.Context, from authz.Resource, relation authz.Relation, subject authz.Type, ids []authz.ID) error {
	e.debugLog("Deleting relations: from=%v, relation=%v, subject=%v, ids=%v", from, relation, subject, ids)
	updates := make([]*v1.RelationshipUpdate, 0, len(ids))
	for _, id := range ids {
		e.debugLog("Processing id: %v", id) // Added debug log
		updates = append(updates, &v1.RelationshipUpdate{
			Operation: v1.RelationshipUpdate_OPERATION_DELETE,
			Relationship: &v1.Relationship{
				Resource: &v1.ObjectReference{
					ObjectType: string(from.Type),
					ObjectId:   string(from.ID),
				},
				Relation: string(relation),
				Subject: &v1.SubjectReference{
					Object: &v1.ObjectReference{
						ObjectType: string(subject),
						ObjectId:   string(id),
					},
				},
			},
		})
	}

	res, err := e.client.WriteRelationships(ctx, &v1.WriteRelationshipsRequest{
		Updates: updates,
	})
	if err != nil {
		return err
	}

	e.setToken(res.WrittenAt.Token)

	return nil
}

func (e *Engine) CheckPermissionWithCaveat(ctx context.Context, dest authz.Resource, has authz.Permission, subject authz.Type, audIDs []authz.ID, caveatParams map[string]any) error {
	e.debugLog("Checking permission with caveat: dest=%v, has=%v, subject=%v, audIDs=%v, params=%v", dest, has, subject, audIDs, caveatParams)
	consistency := e.getConsistencySnapshot()

	caveatCtx, err := serializeCaveatMap(caveatParams)
	if err != nil {
		return fmt.Errorf("serialize caveat params: %w", err)
	}

	for _, id := range audIDs {
		err := errorIfDenied(e.client.CheckPermission(ctx, &v1.CheckPermissionRequest{
			Consistency: consistency,
			Resource: &v1.ObjectReference{
				ObjectType: string(dest.Type),
				ObjectId:   string(dest.ID),
			},
			Permission: string(has),
			Subject: &v1.SubjectReference{
				Object: &v1.ObjectReference{
					ObjectType: string(subject),
					ObjectId:   string(id),
				},
			},
			Context: caveatCtx,
		}))
		if err != nil {
			return err
		}
	}

	return nil
}

// serializeCaveatMap converts the codegen wire-boundary map into a
// structpb.Struct for SpiceDB's CheckPermissionRequest.Context and the
// write-time OptionalCaveat.Context. Empty / nil input returns
// (nil, nil) so the wire field stays unset, matching non-caveat checks.
//
// structpb.NewStruct only accepts a narrow set of value types and in
// particular rejects typed slices like []string, []int64, etc. — only
// []any reaches its ListValue encoder. The codegen emits typed slices
// when caveat parameters are list<T>, so we coerce here at the wire
// boundary so call sites stay simple.
func serializeCaveatMap(m map[string]any) (*structpb.Struct, error) {
	if len(m) == 0 {
		return nil, nil
	}
	return structpb.NewStruct(coerceStructpbMap(m))
}

func coerceStructpbMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = coerceStructpbValue(v)
	}
	return out
}

func coerceStructpbValue(v any) any {
	switch x := v.(type) {
	case []byte:
		// structpb.NewValue natively encodes []byte as a base64-string
		// StringValue — exactly what SpiceDB CEL's bytes type expects on
		// the wire. Pass through unchanged; the reflection fallback below
		// would otherwise convert it to []any of int64, which SpiceDB
		// rejects with "bytes requires a base64 unicode string".
		return x
	case []any:
		coerced := make([]any, len(x))
		for i, e := range x {
			coerced[i] = coerceStructpbValue(e)
		}
		return coerced
	case []string:
		return toAnySlice(x)
	case []int64:
		return toAnySlice(x)
	case []int:
		return toAnySlice(x)
	case []uint64:
		return toAnySlice(x)
	case []float64:
		return toAnySlice(x)
	case []bool:
		return toAnySlice(x)
	case map[string]any:
		return coerceStructpbMap(x)
	default:
		// Reflection fallback for typed slices not enumerated above —
		// e.g. [][]string from a list<list<string>> caveat parameter, or
		// any other typed slice the codegen produces. Recurses through
		// each element so nested structures coerce all the way down.
		rv := reflect.ValueOf(v)
		if rv.IsValid() && rv.Kind() == reflect.Slice {
			out := make([]any, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				out[i] = coerceStructpbValue(rv.Index(i).Interface())
			}
			return out
		}
		return v
	}
}

func toAnySlice[T any](in []T) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

func (e *Engine) CheckPermission(ctx context.Context, dest authz.Resource, has authz.Permission, subject authz.Type, audIDs []authz.ID) error {
	e.debugLog("Checking permission: dest=%v, has=%v, subject=%v, audIDs=%v", dest, has, subject, audIDs)
	consistency := e.getConsistencySnapshot()

	for _, id := range audIDs {
		e.debugLog("Processing id: %v", id) // Added debug log
		err := errorIfDenied(e.client.CheckPermission(ctx, &v1.CheckPermissionRequest{
			Consistency: consistency,
			Resource: &v1.ObjectReference{
				ObjectType: string(dest.Type),
				ObjectId:   string(dest.ID),
			},
			Permission: string(has),
			Subject: &v1.SubjectReference{
				Object: &v1.ObjectReference{
					ObjectType: string(subject),
					ObjectId:   string(id),
				},
			},
		}))
		if err != nil {
			return err
		}
	}

	return nil
}

func (e *Engine) LookupResources(ctx context.Context, from authz.Type, match authz.Permission, subject authz.Type, byIDs []authz.ID) (authz.LookupResult, error) {
	return e.LookupResourcesWithCaveat(ctx, from, match, subject, byIDs, nil)
}

func (e *Engine) LookupResourcesWithCaveat(ctx context.Context, from authz.Type, match authz.Permission, subject authz.Type, byIDs []authz.ID, caveatParams map[string]any) (authz.LookupResult, error) {
	e.debugLog("Looking up resources: from=%v, match=%v, subject=%v, byIDs=%v, caveatParams=%v", from, match, subject, byIDs, caveatParams)
	consistency := e.getConsistencySnapshot()

	caveatCtx, err := serializeCaveatMap(caveatParams)
	if err != nil {
		return authz.LookupResult{}, fmt.Errorf("serialize caveat params: %w", err)
	}

	result := authz.LookupResult{
		Definite:    []authz.ID{},
		Conditional: []authz.LookupConditionalEntry{},
	}
	for _, id := range byIDs {
		res, err := e.client.LookupResources(ctx, &v1.LookupResourcesRequest{
			Consistency:        consistency,
			ResourceObjectType: string(from),
			Permission:         string(match),
			Subject: &v1.SubjectReference{
				Object: &v1.ObjectReference{
					ObjectType: string(subject),
					ObjectId:   string(id),
				},
			},
			Context: caveatCtx,
		})
		if err != nil {
			return authz.LookupResult{}, err
		}

		data, err := res.Recv()
		for ; err == nil && data != nil; data, err = res.Recv() {
			e.debugLog("Received data: %+v", data)
			switch data.Permissionship {
			case v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION:
				result.Definite = append(result.Definite, authz.ID(data.ResourceObjectId))

			case v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION:
				var missing []string
				if pci := data.PartialCaveatInfo; pci != nil {
					missing = pci.MissingRequiredContext
				}
				result.Conditional = append(result.Conditional, authz.LookupConditionalEntry{
					ID:          authz.ID(data.ResourceObjectId),
					MissingKeys: missing,
				})
				// LOOKUP_PERMISSIONSHIP_UNSPECIFIED is dropped — it's a wire
				// placeholder for protocol-version mismatches, not a valid grant.
			}
		}

		if !errors.Is(err, io.EOF) {
			return authz.LookupResult{}, err
		}
	}

	return result, nil
}

func (e *Engine) LookupSubjects(ctx context.Context, on authz.Resource, permission authz.Permission, subject authz.Type) (authz.LookupResult, error) {
	return e.LookupSubjectsWithCaveat(ctx, on, permission, subject, nil)
}

func (e *Engine) LookupSubjectsWithCaveat(ctx context.Context, on authz.Resource, permission authz.Permission, subject authz.Type, caveatParams map[string]any) (authz.LookupResult, error) {
	e.debugLog("Looking up subjects: on=%v, permission=%v, subject=%v, caveatParams=%v", on, permission, subject, caveatParams)
	consistency := e.getConsistencySnapshot()

	caveatCtx, err := serializeCaveatMap(caveatParams)
	if err != nil {
		return authz.LookupResult{}, fmt.Errorf("serialize caveat params: %w", err)
	}

	res, err := e.client.LookupSubjects(ctx, &v1.LookupSubjectsRequest{
		Consistency: consistency,
		Resource: &v1.ObjectReference{
			ObjectType: string(on.Type),
			ObjectId:   string(on.ID),
		},
		Permission:        string(permission),
		SubjectObjectType: string(subject),
		Context:           caveatCtx,
	})
	if err != nil {
		return authz.LookupResult{}, err
	}

	result := authz.LookupResult{
		Definite:    []authz.ID{},
		Conditional: []authz.LookupConditionalEntry{},
	}
	data, err := res.Recv()
	for ; err == nil && data != nil; data, err = res.Recv() {
		e.debugLog("Received data: %+v", data)
		// Read from data.Subject — top-level Permissionship is deprecated
		// per the proto in favor of Subject.Permissionship.
		if data.Subject == nil {
			continue
		}
		switch data.Subject.Permissionship {
		case v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION:
			result.Definite = append(result.Definite, authz.ID(data.Subject.SubjectObjectId))

		case v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION:
			var missing []string
			if pci := data.Subject.PartialCaveatInfo; pci != nil {
				missing = pci.MissingRequiredContext
			}
			result.Conditional = append(result.Conditional, authz.LookupConditionalEntry{
				ID:          authz.ID(data.Subject.SubjectObjectId),
				MissingKeys: missing,
			})
			// LOOKUP_PERMISSIONSHIP_UNSPECIFIED dropped silently.
		}
	}
	if !errors.Is(err, io.EOF) {
		return authz.LookupResult{}, err
	}

	return result, nil
}

// CheckPermissionUserset answers the rare userset-as-subject question — "does the
// userset reference {subjectType, id, subRelation} have permission `has` on `dest`?"
// SpiceDB matches the literal userset reference; it does NOT recursively walk the
// userset's membership (per SPEC-006 A2). The common case (does user u1 have view?)
// continues to use CheckPermission / CheckPermissionWithCaveat.
func (e *Engine) CheckPermissionUserset(ctx context.Context, dest authz.Resource, has authz.Permission, subject authz.Type, audIDs []authz.ID, subRelation string, caveatParams map[string]any) error {
	e.debugLog("Checking userset permission: dest=%v, has=%v, subject=%v#%v, audIDs=%v, params=%v", dest, has, subject, subRelation, audIDs, caveatParams)

	var caveatCtx *structpb.Struct
	if caveatParams != nil {
		var err error
		caveatCtx, err = serializeCaveatMap(caveatParams)
		if err != nil {
			return fmt.Errorf("serialize caveat params: %w", err)
		}
	}

	consistency := e.getConsistencySnapshot()
	for _, id := range audIDs {
		res, err := e.client.CheckPermission(ctx, &v1.CheckPermissionRequest{
			Consistency: consistency,
			Resource: &v1.ObjectReference{
				ObjectType: string(dest.Type),
				ObjectId:   string(dest.ID),
			},
			Permission: string(has),
			Subject: &v1.SubjectReference{
				Object: &v1.ObjectReference{
					ObjectType: string(subject),
					ObjectId:   string(id),
				},
				OptionalRelation: subRelation,
			},
			Context: caveatCtx,
		})
		if err := errorIfDenied(res, err); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) ReadRelations(ctx context.Context, from authz.Resource, relation authz.Relation, subject authz.Type) ([]authz.RelationTuple, error) {
	e.debugLog("Reading relations: from=%v, relation=%v, subject=%v", from, relation, subject)
	consistency := e.getConsistencySnapshot()
	tuples := []authz.RelationTuple{}

	res, err := e.client.ReadRelationships(ctx, &v1.ReadRelationshipsRequest{
		Consistency: consistency,
		RelationshipFilter: &v1.RelationshipFilter{
			ResourceType:       string(from.Type),
			OptionalResourceId: string(from.ID),
			OptionalRelation:   string(relation),
			OptionalSubjectFilter: &v1.SubjectFilter{
				SubjectType: string(subject),
			},
		},
	})
	if err != nil {
		return nil, err
	}

	data, err := res.Recv()
	for ; err == nil && data != nil; data, err = res.Recv() {
		e.debugLog("Received data: %+v", data)
		rel := data.Relationship
		t := authz.RelationTuple{
			ID:          authz.ID(rel.Subject.Object.ObjectId),
			SubRelation: rel.Subject.OptionalRelation,
		}
		if rel.OptionalCaveat != nil {
			t.CaveatName = rel.OptionalCaveat.CaveatName
			if rel.OptionalCaveat.Context != nil {
				t.CaveatContext = rel.OptionalCaveat.Context.AsMap()
			}
		}
		if rel.OptionalExpiresAt != nil {
			ts := rel.OptionalExpiresAt.AsTime()
			t.ExpiresAt = &ts
		}
		tuples = append(tuples, t)
	}
	if !errors.Is(err, io.EOF) {
		return nil, err
	}

	return tuples, nil
}

func (e *Engine) HasPublicRelation(ctx context.Context, on authz.Resource, relation authz.Relation, subject authz.Type) (bool, error) {
	e.debugLog("Checking public relation: on=%v, relation=%v, subject=%v", on, relation, subject)
	tuples, err := e.ReadRelations(ctx, on, relation, subject)
	if err != nil {
		return false, err
	}
	for _, t := range tuples {
		if t.ID == authz.WildcardID {
			return true, nil
		}
	}
	return false, nil
}

func (e *Engine) HasPublicSubject(ctx context.Context, on authz.Resource, permission authz.Permission, subject authz.Type) (bool, error) {
	e.debugLog("Checking public subject: on=%v, permission=%v, subject=%v", on, permission, subject)
	result, err := e.LookupSubjects(ctx, on, permission, subject)
	if err != nil {
		return false, err
	}
	for _, id := range result.Definite {
		if id == authz.WildcardID {
			return true, nil
		}
	}
	return false, nil
}

func errorIfDenied(res *v1.CheckPermissionResponse, err error) error {
	if err != nil {
		return err
	}

	switch res.Permissionship {
	case v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION:
		return nil

	case v1.CheckPermissionResponse_PERMISSIONSHIP_CONDITIONAL_PERMISSION:
		var missing []string
		if pci := res.PartialCaveatInfo; pci != nil {
			missing = pci.MissingRequiredContext
		}
		return &authz.ConditionalPermissionError{MissingKeys: missing}

	default:
		return authz.ErrPermissionDenied
	}
}
