# [ADR-005] Engine Relationship-Filter Deletes: A Filter Struct over Named Methods

| Field      | Value                                                |
|------------|------------------------------------------------------|
| Status     | Accepted                                                |
| Date       | 2026-05-10                                            |
| Deciders   | Danh Tran                                             |
| Scope      | pkg/authz Engine interface + spicedb engine impl      |
| Depends on | docs/scope-relationship-cleanup-verbs.md              |

---

## Context

`docs/scope-relationship-cleanup-verbs.md` adds three generated cleanup verbs — `<Resource>.Purge<Rel>Relations(ctx)`, `<Resource>.PurgeRelations(ctx)`, `<Type>.PurgeRelationsAsSubject(ctx)` — for the "object deleted → clean up its tuples" pattern. None of them can be implemented through the current `authz.Engine` interface: the only delete it exposes is `DeleteRelations(ctx, from Resource, relation Relation, subject Type, ids []ID) error`, which requires the relation **and** the subject type **and** the specific subject IDs — a *targeted* revoke, not a filter-driven purge.

SpiceDB's `DeleteRelationships` RPC takes a single `RelationshipFilter` (`ResourceType`, `OptionalResourceId`, `OptionalResourceIdPrefix`, `OptionalRelation`, `OptionalSubjectFilter{SubjectType, OptionalSubjectId, OptionalRelation}`) and deletes all matching tuples — transactionally with `OptionalLimit: 0` (per A1). Any of the three cleanup verbs is one of these filters; `pkg/authz/` is a thin abstraction over SpiceDB, so the question is whether `Engine` exposes the filter as a struct (mirroring `RelationshipFilter`) or as a fixed set of pre-shaped named methods. This decision affects the `pkg/authz/` contract — under the v1.10 semver commitment, it's a MINOR bump (additive surface), so the shape needs pinning before implementation.

## Options

### Option A — Three named methods

```go
DeleteRelationsByResource(ctx context.Context, from Resource) error
DeleteRelationsByResourceRelation(ctx context.Context, from Resource, relation Relation) error
DeleteRelationsBySubject(ctx context.Context, resourceType Type, subject Type, subjectID ID) error
```

Each method maps to exactly one `RelationshipFilter` shape; its signature constrains the blast radius. Hard to misuse. But: every new filter shape the cleanup story grows into — resource-ID prefix purge (`folder:proj-*`), sub-relation-scoped subject filters (`team:t1#admin` only) — is a *new method* on the interface; the interface widens method-by-method. Three methods today, more later.

### Option B — One filter-struct method

```go
// In pkg/authz/ — mirrors SpiceDB's RelationshipFilter (subset the codegen needs today):
type RelationFilter struct {
    ResourceType Type     // optional, but recommended — a no-resource-type filter is index-suboptimal in SpiceDB
    ResourceID   ID       // optional
    Relation     Relation // optional
    SubjectType  Type     // optional
    SubjectID    ID       // optional
}
DeleteRelationsMatching(ctx context.Context, f RelationFilter) error
```

`*spicedb.Engine` translates `RelationFilter` to a `v1.RelationshipFilter` with `OptionalLimit: 0`, rejecting the all-empty filter (matching SpiceDB's server-side `checkIfFilterIsEmpty`, but failing faster). Faithful abstraction — one method, mirroring SpiceDB's one delete-by-filter op. Extensible: prefix matching, sub-relation-scoped subject filters etc. are new *struct fields*, not new methods. But: an under-specified filter deletes broadly — `RelationFilter{ResourceType: TypeFolder}` (nothing else) means "delete *every* folder relationship in the system"; a zero-value field silently widens the delete. The all-empty case is rejected; the dangerously-broad-but-non-empty cases are not (SpiceDB doesn't reject them either). A doc comment carries the warning.

### Option C — Overload the existing `DeleteRelations`

Make `DeleteRelations(ctx, from, relation, subject, ids)` treat a zero-value `relation` as "all relations on `from`", a zero `subject` as "any subject type", a `nil`/empty `ids` as "any subject ID". No new method. But: it conflates targeted revoke with bulk purge behind one signature; a caller who forgets to set `relation` silently issues a "delete everything on this resource" call. The semantics of an existing method shift invisibly to current callers.

### Option D — No Engine change; generated code calls the SpiceDB client directly

The generated `Purge*` methods reach through to `engine.(*spicedb.Engine).client.DeleteRelationships(...)` (or a new exported accessor). Zero interface change. But: generated code today touches only the `authz.Engine` interface — never the underlying SpiceDB client (per A3). Breaking that boundary couples generated code to the spicedb package, defeating the abstraction `pkg/authz/` exists for, and violating the `.golangci.yml` boundary rules.

## Options Comparison

| Driver | A: Named methods | B: Filter struct | C: Overload DeleteRelations | D: Call client directly |
|--------|------------------|------------------|-----------------------------|-------------------------|
| Faithful to SpiceDB's API | Re-sliced | Mirrors `RelationshipFilter` | Re-sliced | Direct (too direct) |
| Extends to new filter shapes | New method each | New struct field | N/A | N/A |
| Interface additions | Three methods | One method + one type | Zero | Zero (+ client accessor) |
| Misuse resistance | Signature bounds it | Zero-value widens silently | Zero relation = purge-all | None |
| Respects pkg/authz boundary | Yes | Yes | Yes | No — couples to spicedb pkg |
| Conflates targeted vs bulk delete | No | No | Yes | No |
| Consistent with existing arg style | Positional (matches) | Struct (first such method) | Positional (matches) | N/A |

## Decision

The `authz.Engine` interface gains one method, `DeleteRelationsMatching(ctx context.Context, f RelationFilter) error`, where `RelationFilter` (a new type in `pkg/authz/`) mirrors the subset of SpiceDB's `RelationshipFilter` the codegen needs today; `*spicedb.Engine` implements it as a `client.DeleteRelationships` call with `OptionalLimit: 0`, and the generated `PurgeRelations` / `Purge<Rel>Relations` / `PurgeRelationsAsSubject` verbs are thin wrappers that fill the filter for their narrow shapes.

## Consequences

**Consequences Positive**

- `RelationFilter` mirrors SpiceDB's `RelationshipFilter`, so `pkg/authz/` stays a thin abstraction over the underlying API — one delete-by-filter op in SpiceDB, one `DeleteRelationsMatching` method in the abstraction. There is no opinionated re-slicing for a future reader to reverse-map.
- New filter shapes — resource-ID prefix purge, sub-relation-scoped subject filters — are additive struct fields on `RelationFilter`, not new interface methods. The cleanup story (which the scope already extends with sub-relation subjects) grows by widening one struct rather than the interface.
- The interface addition is one method plus one type. Out-of-tree `Engine` implementations (test doubles, alternate backends) add one method, not three; the `pkg/authz/` contract change is small.
- Generated code reads at the right level and stays inside the boundary: `engine.DeleteRelationsMatching(ctx, authz.RelationFilter{ResourceType: TypeFolder, ResourceID: ..., Relation: Relation(FolderViewer)})` — explicit about which filter is applied, no `v1.RelationshipFilter` literal, no reach into the spicedb client. `.golangci.yml`'s boundary rules stay satisfied.
- The targeted `DeleteRelations(ctx, from, relation, subject, ids)` keeps its exact semantics — a precise revoke. Bulk purge is a separate, named operation taking a filter. The two are not conflated behind one signature.

**Consequences Negative**

- An under-specified `RelationFilter` deletes broadly. `RelationFilter{ResourceType: TypeFolder}` — only the type set — deletes *every* folder relationship in the datastore; a forgotten or zero-value field silently widens the blast radius. The all-empty filter is rejected (matching SpiceDB), but the dangerously-broad-but-non-empty filters are not — SpiceDB's own `RelationshipFilter` carries the same hazard, and `pkg/authz/`, being a thin abstraction over it, inherits rather than childproofs it. The only guard is a doc comment on `RelationFilter` ("set `ResourceType` + `ResourceID`, or `ResourceType` + `SubjectID`; a filter with just `ResourceType` deletes every relationship of that type") — a warning, not a check.
- `DeleteRelationsMatching(ctx, RelationFilter)` is the first struct-argument method on the `Engine` interface; every other method (`CheckPermission`, `LookupResources`, `DeleteRelations`, …) is positional. The interface's arg style is no longer uniform — a small but real inconsistency a reader has to absorb.
- `RelationFilter` is a new exported type in `pkg/authz/`, alongside `Resource` / `Relation` / `Type` / `ID`. More surface to document and keep stable; if its field set later diverges from SpiceDB's `RelationshipFilter` (SpiceDB adds a filter dimension we don't mirror, or vice versa), the "faithful mirror" framing weakens and the gap needs explaining.
- The bounded / non-transactional batch-delete loop (`OptionalLimit: N, OptionalAllowPartialDeletions: true`) is not exposed — `DeleteRelationsMatching` is `OptionalLimit: 0`, one transaction, like the existing `DeleteRelations`. An object with millions of tuples hits the same single-transaction timeout (per A1 / spicedb#1224). Adding the loop later means an `opts` parameter on `DeleteRelationsMatching` (or a sibling method), and `OptionalLimit: 0` callers must keep working unchanged — so it can't be a default change.

## Assumptions

- **A1 [VERIFIED]:** SpiceDB's `DeleteRelationships` RPC accepts a `RelationshipFilter` where `ResourceType` is optional (`checkIfFilterIsEmpty` requires only one field set); `OptionalLimit: 0` deletes all matching tuples within one `ReadWriteTx` (transactional); `OptionalLimit: N` with `OptionalAllowPartialDeletions` controls bounded/non-transactional batching; a no-`resource_type` filter is index-suboptimal; SpiceDB does not cascade. Evidence: `github.com/authzed/spicedb@v1.52.0` `internal/services/v1/relationships.go` (the `DeleteRelationships` handler — `validateRelationshipsFilter` comment "ResourceType is optional", the `OptionalLimit` branches, the "unlimited deletion" path); `github.com/authzed/spicedb` PR #1739 (made `resource_type` optional; "two calls"; the index caveat); issue #1224 (large-deletion / non-transactional motivation); issue #315 (closed → two-call answer, not a `DeleteObject` API).

- **A2 [VERIFIED]:** The existing `authz.Engine.DeleteRelations(ctx, from Resource, relation Relation, subject Type, ids []ID) error` is a targeted revoke — it requires the relation, subject type, and specific subject IDs; `*spicedb.Engine` implements it as a `DeleteRelationships` call with `OptionalResourceId` + `OptionalRelation` + a subject filter built from the args, `OptionalLimit: 0`. Evidence: `pkg/authz/authz.go:189`; `pkg/authz/spicedb/crud.go` (~line 640).

- **A3 [VERIFIED]:** Generated code touches only the `authz.Engine` interface — it never imports `pkg/authz/spicedb` or the `authzed-go` client. Evidence: every existing `<entity>.gen.go` calls `authz.GetEngine(ctx).CheckPermission(...)` / `...CreateRelations(...)` etc. through the interface; `.golangci.yml`'s boundary rules forbid generated code (and `pkg/authz/`) from depending on the codegen internals and vice-versa.

- **A4 [VERIFIED]:** The codegen's design stance is verbs-not-workflows — the generated `Purge*` verbs are mechanical primitives over `DeleteRelationsMatching`; "delete an object" (which relations to purge, what to do about orphaned children) stays in the caller's domain model. Evidence: confirmed with the user during this feature's design discussion; SpiceDB's own choice not to add a `DeleteObject` API (#315 → #1739) reflects the same reasoning.

## History

<!-- managed by `harness history-update` — do not hand-edit -->
