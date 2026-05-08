package spicedb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"time"

	"github.com/danhtran94/authzed-codegen/pkg/authz"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	"github.com/authzed/authzed-go/v1"
	"google.golang.org/protobuf/types/known/structpb"
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

func (e *Engine) LookupResources(ctx context.Context, from authz.Type, match authz.Permission, subject authz.Type, byIDs []authz.ID) ([]authz.ID, error) {
	return e.LookupResourcesWithCaveat(ctx, from, match, subject, byIDs, nil)
}

func (e *Engine) LookupResourcesWithCaveat(ctx context.Context, from authz.Type, match authz.Permission, subject authz.Type, byIDs []authz.ID, caveatParams map[string]any) ([]authz.ID, error) {
	e.debugLog("Looking up resources: from=%v, match=%v, subject=%v, byIDs=%v, caveatParams=%v", from, match, subject, byIDs, caveatParams)
	consistency := e.getConsistencySnapshot()

	caveatCtx, err := serializeCaveatMap(caveatParams)
	if err != nil {
		return nil, fmt.Errorf("serialize caveat params: %w", err)
	}

	ids := []authz.ID{}
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
			return nil, err
		}

		data, err := res.Recv()
		for ; err == nil && data != nil; data, err = res.Recv() {
			e.debugLog("Received data: %+v", data)
			// Skip CONDITIONAL_PERMISSION — caller treats those as deny,
			// matching errorIfDenied's collapse on Check. Definite grants
			// only.
			if data.Permissionship != v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION {
				continue
			}
			ids = append(ids, authz.ID(data.ResourceObjectId))
		}

		if !errors.Is(err, io.EOF) {
			return nil, err
		}
	}

	return ids, nil
}

func (e *Engine) LookupSubjects(ctx context.Context, on authz.Resource, permission authz.Permission, subject authz.Type) ([]authz.ID, error) {
	return e.LookupSubjectsWithCaveat(ctx, on, permission, subject, nil)
}

func (e *Engine) LookupSubjectsWithCaveat(ctx context.Context, on authz.Resource, permission authz.Permission, subject authz.Type, caveatParams map[string]any) ([]authz.ID, error) {
	e.debugLog("Looking up subjects: on=%v, permission=%v, subject=%v, caveatParams=%v", on, permission, subject, caveatParams)
	consistency := e.getConsistencySnapshot()

	caveatCtx, err := serializeCaveatMap(caveatParams)
	if err != nil {
		return nil, fmt.Errorf("serialize caveat params: %w", err)
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
		return nil, err
	}

	ids := []authz.ID{}
	data, err := res.Recv()
	for ; err == nil && data != nil; data, err = res.Recv() {
		e.debugLog("Received data: %+v", data)
		// Filter on Subject.Permissionship — `Permissionship` (top-level
		// of LookupSubjectsResponse) is deprecated per the proto in
		// favor of `Subject.Permissionship`. Definite grants only.
		if data.Subject == nil || data.Subject.Permissionship != v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION {
			continue
		}
		ids = append(ids, authz.ID(data.Subject.SubjectObjectId))
	}
	if !errors.Is(err, io.EOF) {
		return nil, err
	}

	return ids, nil
}

func (e *Engine) ReadRelations(ctx context.Context, from authz.Resource, relation authz.Relation, subject authz.Type) ([]authz.ID, error) {
	e.debugLog("Reading relations: from=%v, relation=%v, subject=%v", from, relation, subject)
	consistency := e.getConsistencySnapshot()
	ids := []authz.ID{}

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
		e.debugLog("Received data: %+v", data) // Added debug log
		ids = append(ids, authz.ID(data.Relationship.Subject.Object.ObjectId))
	}
	if !errors.Is(err, io.EOF) {
		return nil, err
	}

	return ids, nil
}

func (e *Engine) HasPublicRelation(ctx context.Context, on authz.Resource, relation authz.Relation, subject authz.Type) (bool, error) {
	e.debugLog("Checking public relation: on=%v, relation=%v, subject=%v", on, relation, subject)
	ids, err := e.ReadRelations(ctx, on, relation, subject)
	if err != nil {
		return false, err
	}
	return slices.Contains(ids, authz.WildcardID), nil
}

func (e *Engine) HasPublicSubject(ctx context.Context, on authz.Resource, permission authz.Permission, subject authz.Type) (bool, error) {
	e.debugLog("Checking public subject: on=%v, permission=%v, subject=%v", on, permission, subject)
	ids, err := e.LookupSubjects(ctx, on, permission, subject)
	if err != nil {
		return false, err
	}
	return slices.Contains(ids, authz.WildcardID), nil
}

func errorIfDenied(res *v1.CheckPermissionResponse, err error) error {
	if err != nil {
		return err
	}

	if res.Permissionship == v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION {
		return nil
	}

	return authz.ErrPermissionDenied
}
