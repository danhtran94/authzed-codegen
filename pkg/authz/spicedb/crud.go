package spicedb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/danhtran94/authzed-codegen/pkg/authz"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	"github.com/authzed/authzed-go/v1"
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
	e.debugLog("Looking up resources: from=%v, match=%v, subject=%v, byIDs=%v", from, match, subject, byIDs)
	consistency := e.getConsistencySnapshot()
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
		})

		if err != nil {
			return nil, err
		}

		data, err := res.Recv()
		for ; err == nil && data != nil; data, err = res.Recv() {
			e.debugLog("Received data: %+v", data) // Added debug log
			ids = append(ids, authz.ID(data.ResourceObjectId))
		}

		if !errors.Is(err, io.EOF) {
			return nil, err
		}
	}

	return ids, nil
}

func (e *Engine) LookupSubjects(ctx context.Context, on authz.Resource, permission authz.Permission, subject authz.Type) ([]authz.ID, error) {
	e.debugLog("Looking up subjects: on=%v, permission=%v, subject=%v", on, permission, subject)
	consistency := e.getConsistencySnapshot()
	ids := []authz.ID{}

	res, err := e.client.LookupSubjects(ctx, &v1.LookupSubjectsRequest{
		Consistency: consistency,
		Resource: &v1.ObjectReference{
			ObjectType: string(on.Type),
			ObjectId:   string(on.ID),
		},
		Permission:        string(permission),
		SubjectObjectType: string(subject),
	})
	if err != nil {
		return nil, err
	}

	data, err := res.Recv()
	for ; err == nil && data != nil; data, err = res.Recv() {
		e.debugLog("Received data: %+v", data) // Added debug log
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
