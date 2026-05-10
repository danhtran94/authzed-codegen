# [SPEC-014] Relationship Cleanup Codegen

| Field      | Value                                            |
|------------|--------------------------------------------------|
| Status     | Accepted                                            |
| Created    | 2026-05-10                                       |
| Author     | Danh Tran                                        |
| Implements | docs/scope-relationship-cleanup-verbs.md         |

---

## Overview

This SPEC operationalises the relationship-cleanup verbs from the scope note: a new `authz.Engine` method `DeleteRelationsMatching(ctx, RelationFilter) error` (shape decided by ADR-005), its `*spicedb.Engine` implementation, and three generated methods per the codegen — `<Resource>.Purge<Rel>Relations(ctx) error` (one per relation), `<Resource>.PurgeRelations(ctx) error` (one per definition), and `<Type>.PurgeRelationsAsSubject(ctx) error` (one per object type that appears as a subject of some relation in the schema). The generated verbs are thin wrappers that fill a `RelationFilter` for their fixed shapes and call `DeleteRelationsMatching`. `OptionalLimit: 0` — one transactional delete per filter (per A1). It does **not** add a `Delete<Type>()` cascade workflow, re-parenting of orphaned children, the bounded/non-transactional batch loop, optimistic-concurrency preconditions, or entity-CRUD scaffolding — those are out of scope per the scope note.

**What this component does:** Add `RelationFilter` struct + `DeleteRelationsMatching` method to `pkg/authz/authz.go`. Implement `DeleteRelationsMatching` in `pkg/authz/spicedb/crud.go` — translate `RelationFilter` → `*v1.RelationshipFilter` (with `SubjectFilter.OptionalRelation` left nil, per A2), set `OptionalLimit: 0`, call `e.client.DeleteRelationships`, reject the all-empty filter before the call. Add to `internal/templates/object.go.tmpl`: `Purge<Rel>Relations` per relation, `PurgeRelations` per definition, `PurgeRelationsAsSubject` per subject-type. Add a generator helper that computes, per object type, the sorted list of `(referencing-definition-namespace)` pairs from `RelationView.AllowedTypes` across all definitions — `PurgeRelationsAsSubject` issues one `DeleteRelationsMatching` call per referencing definition (each scoped to a `ResourceType`, never a no-`resource_type` filter — per A1's index note); failures are accumulated with `errors.Join`. Regenerate `example/authzed/**.gen.go` (committed); add a "Relationship Cleanup" section to `README.md`; add e2e tests in `extsvc`.

**What this component does not do:** A `Delete<Type>()` workflow that decides which relations to purge — verbs-not-workflows; the caller's domain model composes the verbs. Re-parent orphaned children — domain decision. Expose the bounded batch-delete loop — `OptionalLimit: 0` only (huge-object support is a future `opts` parameter). Add preconditions to the purge — unconditional. Generate entity CRUD (`Create<Type>` / `Get<Type>` / `Update` / `Exists` / `List<Type>s`). Change the targeted `Delete<Rel>Relations(ctx, <Rel>Objects)` — it stays a precise revoke. Modify `internal/generator/adapter.go` — `RelationView.AllowedTypes` already carries the data the helper needs.

---

## Interface Contracts

### Runtime — `pkg/authz/authz.go`

```go
// RelationFilter selects relationships for DeleteRelationsMatching. It
// mirrors the subset of SpiceDB's v1.RelationshipFilter the codegen uses.
//
// Set ResourceType + ResourceID for "all of one resource's relationships",
// add Relation to scope to one relation, or set ResourceType + SubjectType
// + SubjectID for "all relationships where this subject appears within one
// resource type". A filter with ONLY ResourceType set deletes *every*
// relationship of that type in the datastore — the empty-everything case
// (no field set) is rejected, but dangerously-broad-but-non-empty filters
// are not. Match the underlying SpiceDB RelationshipFilter's blast radius
// accordingly.
type RelationFilter struct {
	ResourceType Type     // optional; recommended — a filter with no resource type is index-suboptimal in SpiceDB
	ResourceID   ID       // optional
	Relation     Relation // optional
	SubjectType  Type     // optional
	SubjectID    ID       // optional
}

// IsEmpty reports whether no field is set. DeleteRelationsMatching rejects
// such a filter (it would match every relationship).
func (f RelationFilter) IsEmpty() bool

// On the Engine interface, added after DeleteRelations:
//
//   // DeleteRelationsMatching deletes every relationship matching f, in a
//   // single transaction. Returns ErrEmptyRelationFilter if f.IsEmpty().
//   // For large result sets (millions of tuples) the underlying single
//   // transaction may time out — bounded/non-transactional deletion is
//   // not exposed.
DeleteRelationsMatching(ctx context.Context, f RelationFilter) error
```

New sentinel:

```go
// ErrEmptyRelationFilter is returned by DeleteRelationsMatching when the
// supplied RelationFilter has no field set (which would match — and delete
// — every relationship in the datastore).
var ErrEmptyRelationFilter = errors.New("empty relation filter")
```

### Engine impl — `pkg/authz/spicedb/crud.go`

```go
func (e *Engine) DeleteRelationsMatching(ctx context.Context, f authz.RelationFilter) error {
	if f.IsEmpty() {
		return authz.ErrEmptyRelationFilter
	}
	rf := &v1.RelationshipFilter{
		ResourceType:       string(f.ResourceType),
		OptionalResourceId: string(f.ResourceID),
		OptionalRelation:   string(f.Relation),
	}
	if f.SubjectType != "" || f.SubjectID != "" {
		rf.OptionalSubjectFilter = &v1.SubjectFilter{
			SubjectType:       string(f.SubjectType),
			OptionalSubjectId: string(f.SubjectID),
			// OptionalRelation left nil — matches the subject across all
			// sub-relations (team:t1, team:t1#admin, …). Setting it to
			// {Relation: ""} would match only the ellipsis. Per A2.
		}
	}
	_, err := e.client.DeleteRelationships(ctx, &v1.DeleteRelationshipsRequest{
		RelationshipFilter: rf,
		// OptionalLimit: 0 → one transactional, unlimited delete.
	})
	return err
}
```

### Generated methods — `<package>/<entity>.gen.go`

```go
// Purge<Rel>Relations deletes every <rel> relationship on this <resource>,
// regardless of subject. Unlike Delete<Rel>Relations (which revokes the
// specific subjects you pass), this clears the relation entirely — use it
// when <rel> as a whole no longer applies to this <resource>.
func (folder Folder) PurgeViewerRelations(ctx context.Context) error {
	return authz.GetEngine(ctx).DeleteRelationsMatching(ctx, authz.RelationFilter{
		ResourceType: TypeFolder,
		ResourceID:   authz.ID(folder),
		Relation:     authz.Relation(FolderViewer),
	})
}

// PurgeRelations deletes every relationship on this Folder — all relations,
// any subject — in one transaction. Use it when the Folder is deleted from
// your store: it removes the Folder's resource-side tuples. It does NOT
// remove tuples where the Folder appears as a *subject* of another
// resource (e.g. (folder:f2, parent, folder:f1)) — for that, the subject
// (folder:f2's parent edge) is on f2's side; if the Folder type appears as
// a subject anywhere in the schema, see PurgeRelationsAsSubject.
func (folder Folder) PurgeRelations(ctx context.Context) error {
	return authz.GetEngine(ctx).DeleteRelationsMatching(ctx, authz.RelationFilter{
		ResourceType: TypeFolder,
		ResourceID:   authz.ID(folder),
	})
}

// PurgeRelationsAsSubject deletes every relationship where this User is the
// subject, across the resource types whose schema allows User as a subject.
// One transactional delete per referencing resource type; failures are
// accumulated (errors.Join) and the rest still run — re-run on error
// (idempotent). Use it when the User is deleted from your store, alongside
// the User's own PurgeRelations if User is also a resource type with
// relations. Emitted only for object types that appear as a subject of some
// relation in the schema.
func (user User) PurgeRelationsAsSubject(ctx context.Context) error {
	eng := authz.GetEngine(ctx)
	var errs []error
	// One call per referencing resource type — sorted for deterministic output.
	if err := eng.DeleteRelationsMatching(ctx, authz.RelationFilter{
		ResourceType: TypeFolder, // extsvc/folder allows extsvc/user as a subject (viewer, owner, …)
		SubjectType:  TypeUser,
		SubjectID:    authz.ID(user),
	}); err != nil {
		errs = append(errs, fmt.Errorf("purge user as subject of extsvc/folder: %w", err))
	}
	// … one more block per referencing definition …
	return errors.Join(errs...)
}
```

`PurgeRelationsAsSubject` is **not** emitted for object types that never appear as a subject (e.g. a leaf resource type referenced by nothing). For a type referenced by exactly one definition, the method makes one call and returns its error directly (no `errors.Join` needed — but using `errors.Join(errs...)` uniformly is fine; it returns the single error unchanged).

### Generator helper — `internal/generator/generator.go`

```go
// subjectReferences returns, for each object type that appears as a subject
// of some relation across all definitions, the sorted list of definition
// namespaces that reference it. Drives PurgeRelationsAsSubject emission and
// its per-referencing-resource-type call list. Built from
// RelationView.AllowedTypes (each allowed type's Namespace is the subject
// type; SubRelation is irrelevant — a userset reference team#admin still
// has Namespace == "extsvc/team").
//
//   subjectReferences(defs) == map[string][]string{
//       "extsvc/user":  {"extsvc/folder", "extsvc/team"},   // sorted
//       "extsvc/team":  {"extsvc/folder"},
//       ...
//   }
func subjectReferences(defs []*DefinitionView) map[string][]string
```

A template func exposes this so `object.go.tmpl` can, per definition, ask "is this definition's object type a subject anywhere?" (→ emit `PurgeRelationsAsSubject`) and "which definitions reference it?" (→ the per-type call list).

---

## Data Shapes

### `RelationFilter` → `v1.RelationshipFilter` mapping

| `RelationFilter` field | `v1.RelationshipFilter` field | Notes |
|---|---|---|
| `ResourceType` | `ResourceType` | string-cast; empty allowed by SpiceDB but index-suboptimal — codegen always sets it |
| `ResourceID` | `OptionalResourceId` | string-cast; empty → not set |
| `Relation` | `OptionalRelation` | string-cast; empty → not set |
| `SubjectType`, `SubjectID` | `OptionalSubjectFilter.{SubjectType, OptionalSubjectId}` | the `SubjectFilter` is created only if either is non-empty |
| (none) | `OptionalSubjectFilter.OptionalRelation` | always nil — matches any sub-relation (per A2) |
| (none) | `OptionalResourceIdPrefix` | not mirrored (no current use) |
| (none) | `DeleteRelationshipsRequest.OptionalLimit` | always 0 — one transactional delete |
| (none) | `DeleteRelationshipsRequest.OptionalAllowPartialDeletions` | always false (irrelevant when limit is 0) |
| (none) | `DeleteRelationshipsRequest.OptionalPreconditions` | not used — unconditional |

### Generated verb → `RelationFilter` shape

```
Folder.PurgeViewerRelations(ctx)        → {ResourceType: TypeFolder, ResourceID: <f>, Relation: FolderViewer}
Folder.PurgeRelations(ctx)              → {ResourceType: TypeFolder, ResourceID: <f>}
User.PurgeRelationsAsSubject(ctx)       → for each D in subjectReferences["<pkg>/user"]:
                                            {ResourceType: TypeD, SubjectType: TypeUser, SubjectID: <u>}
```

---

## Sequence

`Folder.PurgeRelations(ctx)`:

```
caller: folder.PurgeRelations(ctx)        ← folder:f1 was deleted from the caller's store
   │
   ▼
authz.GetEngine(ctx).DeleteRelationsMatching(ctx, RelationFilter{ResourceType: TypeFolder, ResourceID: "f1"})
   │
   ▼  IsEmpty()? no → proceed
   ▼
*spicedb.Engine: build v1.RelationshipFilter{ResourceType: "extsvc/folder", OptionalResourceId: "f1"}
   ▼
client.DeleteRelationships(&v1.DeleteRelationshipsRequest{RelationshipFilter: ..., OptionalLimit: 0})
   │
   ▼  SpiceDB: one ReadWriteTx — deletes ALL tuples where resource == folder:f1 (every relation, any subject)
   ▼
return nil (or the gRPC error)
```

`User.PurgeRelationsAsSubject(ctx)` (User referenced by extsvc/folder and extsvc/team):

```
caller: user.PurgeRelationsAsSubject(ctx)   ← user:u1 was deleted
   │
   ├─► DeleteRelationsMatching(ctx, {ResourceType: TypeFolder, SubjectType: TypeUser, SubjectID: "u1"})
   │     └─► deletes (folder:*, *, user:u1) and (folder:*, *, user:u1#*) — any folder, any relation, any sub-relation
   │     └─► err? → errs = append(errs, wrap("…of extsvc/folder", err)) ; continue
   │
   ├─► DeleteRelationsMatching(ctx, {ResourceType: TypeTeam, SubjectType: TypeUser, SubjectID: "u1"})
   │     └─► deletes (team:*, *, user:u1) and (team:*, *, user:u1#*)
   │     └─► err? → errs = append(errs, wrap("…of extsvc/team", err)) ; continue
   │
   ▼
return errors.Join(errs...)   ← nil if all succeeded
```

Lifecycle pattern (documented in README, not enforced): when `folder:f1` is deleted from the caller's store, in the same logical operation call `folder.PurgeRelations(ctx)` (resource-side); if `Folder` also appears as a subject (e.g. `(folder:f2, parent, folder:f1)`), also call `folder.PurgeRelationsAsSubject(ctx)` (subject-side). The two are separate calls — idempotent, but not jointly atomic; re-run on partial failure.

---

## Errors

| Error | From | Trigger | Caller recovery |
|---|---|---|---|
| `authz.ErrEmptyRelationFilter` | `DeleteRelationsMatching` | `f.IsEmpty()` — no field set | Caller fixes the filter (a generated `Purge*` method can't produce this — it always sets `ResourceType` + at least one more field) |
| wrapped gRPC error | `DeleteRelationsMatching` | SpiceDB rejects the request (unknown resource type / relation, transaction timeout on a huge delete, connection error) | Retry (idempotent — re-deleting already-deleted tuples is a no-op); for timeout-on-huge-object, the bounded loop is out of scope — drop to the raw client with `OptionalLimit`/`OptionalAllowPartialDeletions` |
| `errors.Join` of per-type wrapped errors | `PurgeRelationsAsSubject` | one or more of the per-referencing-resource-type deletes failed | Re-run `PurgeRelationsAsSubject` (idempotent); the joined error names which resource types failed (`"purge … as subject of extsvc/folder: …"`) |

`DeleteRelationsMatching` and `Purge<Rel>Relations` / `PurgeRelations` make exactly one `DeleteRelationships` call — atomic, all-or-nothing. `PurgeRelationsAsSubject` makes N calls — each atomic individually, but the N together are not transactional; a mid-sequence failure leaves the earlier deletes committed. It is best-effort: a failing call does not abort the remaining calls.

---

## Constraints

- **C1 — `OptionalLimit: 0` always.** `DeleteRelationsMatching` issues one transactional, unlimited delete per filter. The bounded/non-transactional loop (`OptionalLimit: N, OptionalAllowPartialDeletions: true`) is not exposed; an object with millions of tuples will hit SpiceDB's single-transaction timeout. Adding the loop later means an `opts` parameter on `DeleteRelationsMatching` (or a sibling method) — `OptionalLimit: 0` callers must keep working.

- **C2 — No no-`resource_type` filter from generated code.** Every generated `Purge*` call sets `RelationFilter.ResourceType`. `PurgeRelationsAsSubject` issues one call per referencing resource type rather than one no-`resource_type` filter (which is index-suboptimal per A1). A hand-written caller *may* pass a `RelationFilter` with `ResourceType == ""` to `DeleteRelationsMatching` — SpiceDB accepts it, with the index cost; the `RelationFilter` doc comment notes this.

- **C3 — `SubjectFilter.OptionalRelation` left nil.** When `RelationFilter.SubjectType`/`SubjectID` are set, the `*spicedb.Engine` builds `v1.SubjectFilter` with `OptionalRelation == nil` — matching the subject across all sub-relations (`team:t1`, `team:t1#admin`, …). This is intentional: `PurgeRelationsAsSubject` removes the object from everywhere it appears as a subject, including userset references. Setting `OptionalRelation` to `{Relation: ""}` would match only the ellipsis (per A2) — not what cleanup wants.

- **C4 — Deterministic output ordering.** `PurgeRelationsAsSubject`'s per-referencing-resource-type call blocks are emitted in sorted-namespace order (`subjectReferences` returns sorted slices). `Purge<Rel>Relations` methods are emitted in the same order as the relations on the definition (the template already iterates them in declaration order, which is stable from the compiler). Round-trip regeneration (scope SC9) requires byte-identical output.

- **C5 — `PurgeRelationsAsSubject` emitted only for subject types.** A definition's object type gets a `PurgeRelationsAsSubject` method iff that type appears in some `RelationView.AllowedTypes[*].Namespace` across all definitions. A leaf resource type referenced as a subject by nothing has no such method (it would make zero calls — emitting it would be misleading).

- **C6 — `RelationFilter` mirrors `RelationshipFilter` as a subset, not a re-design.** If SpiceDB adds a filter dimension the codegen later needs, it becomes a new `RelationFilter` field (additive, MINOR). The two should not diverge into different shapes.

## Unresolved Questions

(none)

Two design choices were considered and resolved here:

- **`PurgeRelationsAsSubject` error policy** — resolved as **best-effort + `errors.Join`** (continue past a failed per-type delete; return the joined error). Rationale: a cleanup operation should remove as much as possible even if one resource type's delete fails; the operation is idempotent, so the caller re-runs the whole thing on a non-nil return. The alternative — abort on first error — would leave more orphans behind on a transient failure.
- **`RelationFilter` field set** — resolved as the **five fields the codegen uses today** (`ResourceType`, `ResourceID`, `Relation`, `SubjectType`, `SubjectID`). `OptionalResourceIdPrefix` and a sub-relation-scoped `SubjectRelation` are not included — no current use, and adding a struct field later is backwards-compatible (per C6). The codegen never needs prefix matching, and `PurgeRelationsAsSubject` deliberately wants the all-sub-relations behavior (`OptionalRelation == nil`), which is the only behavior without a `SubjectRelation` field.

## Assumptions

- **A1 [VERIFIED]:** SpiceDB's `DeleteRelationships` RPC accepts a `RelationshipFilter` where `ResourceType` is optional (`checkIfFilterIsEmpty` requires only one field set); `OptionalLimit: 0` deletes all matching tuples within one `ReadWriteTx` (transactional); a no-`resource_type` filter is index-suboptimal; `OptionalLimit: N` with `OptionalAllowPartialDeletions` controls bounded/non-transactional batching; SpiceDB does not cascade. Evidence: `github.com/authzed/spicedb@v1.52.0` `internal/services/v1/relationships.go` (the `DeleteRelationships` handler — `validateRelationshipsFilter` comment "ResourceType is optional", the `OptionalLimit` branches, the "kick off an unlimited deletion" path); `github.com/authzed/spicedb` PR #1739 (made `resource_type` optional; "two calls"; the index caveat); issue #1224; issue #315.

- **A2 [VERIFIED]:** A `v1.SubjectFilter` with `OptionalSubjectId` set and `OptionalRelation == nil` matches the subject across all sub-relations; setting `OptionalRelation` to `&SubjectFilter_RelationFilter{Relation: ""}` matches only the ellipsis (no sub-relation). Evidence: `github.com/authzed/authzed-go@v1.9.0` `proto/authzed/api/v1/permission_service.pb.go` `SubjectFilter` definition + `00_handwritten_validation.go` `SubjectFilter.HandwrittenValidate`; SpiceDB `internal/services/v1/relationships.go` `validateRelationshipsFilter` (treats `subjectFilter.OptionalRelation` as optional, distinct from absent).

- **A3 [VERIFIED]:** `RelationView.AllowedTypes` carries, per relation, the list of allowed subject types — each with a `Namespace` (the subject object type, e.g. `extsvc/user`) and a `SubRelation` (empty for direct subjects, non-empty for userset references like `team#admin`). The "which definitions reference type T as a subject" computation reads `AllowedTypes[*].Namespace` across all definitions, ignoring `SubRelation`. Evidence: `internal/generator/adapter.go` `AllowedType` struct (`Namespace`, `SubRelation` fields per AUZ-005/006); `RelationView.AllowedTypes` populated by `flattenAllowedTypes`.

- **A4 [VERIFIED]:** ADR-005 decided the `Engine` exposes one filter-struct method `DeleteRelationsMatching(ctx, RelationFilter) error` (not three named methods, not an overload, not a direct client call). Evidence: `docs/ADR-005-engine-filter-delete.md`, Status: Accepted.

- **A5 [VERIFIED]:** Generated code touches only the `authz.Engine` interface — it never imports `pkg/authz/spicedb` or the `authzed-go` client. The generated `Purge*` methods call `authz.GetEngine(ctx).DeleteRelationsMatching(...)`, consistent with the existing `Check<Perm>` / `Create<Rel>Relations` etc. Evidence: every existing `<entity>.gen.go`; `.golangci.yml` boundary rules.

## History

<!-- managed by `harness history-update` — do not hand-edit -->
