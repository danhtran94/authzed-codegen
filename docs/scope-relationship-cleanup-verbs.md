# Scope: Relationship cleanup verbs (object lifecycle)

| Field   | Value      |
|---------|------------|
| Status  | Accepted   |
| Created | 2026-05-10 |
| Author  | Danh Tran  |

---

## Problem

When a SpiceDB object disappears from the caller's domain — a `folder:f1` deleted from their database — its relationships in SpiceDB are orphaned. SpiceDB does **not** cascade (per A1); the orphaned tuples linger and cause two concrete failures, not just untidiness:

- **`LookupResources` returns ghosts.** "List the folders alice can browse" still includes `folder:f1`, because its `(folder:f1, viewer, user:alice)` tuple still resolves — the relationship graph has no idea `folder:f1` "no longer exists."
- **Object-ID reuse is a correctness bug.** A recreated `folder:f1` (sequential IDs, slugs, soft-delete-then-recreate) inherits the dead object's `viewer`/`owner`/`collab` grants.

The codegen today offers only **targeted** delete: `<Resource>.Delete<Rel>Relations(ctx, <Rel>Objects{...})` — deletes specific tuples of one relation, requiring the caller to supply the relation, the subject type, **and the specific subject IDs**. There is no way to say "purge everything referencing `folder:f1`" — or even "purge all `viewer` tuples on `folder:f1`, whatever the subject" — through the generated code or the `authz.Engine` interface (the Engine's `DeleteRelations(ctx, from, relation, subject, ids)` requires all four). Callers must drop to the raw `v1.DeleteRelationshipsRequest` with a `RelationshipFilter`, bypassing both the codegen and the `pkg/authz/` abstraction.

This scope adds mechanical cleanup **primitives** — not a cascade. SpiceDB itself made this choice: [authzed/spicedb#315](https://github.com/authzed/spicedb/issues/315) ("remove an object and all its relations in one call") sat at *priority/4 / needs-discussion* for years and was closed by [PR #1739](https://github.com/authzed/spicedb/pull/1739), which did **not** add a `DeleteObject` API — it made `resource_type` optional on `RelationshipFilter` so *"all objects can now be removed from the datastore with **two** calls (once with the object ID as the resource ID and once with it as the subject ID)."* There's no "object" to delete — only tuples matching a filter — and "what should happen to `folder:f1`'s children" (`(folder:f2, parent, folder:f1)` — delete `f2`? re-parent it? leave it orphaned?) is a domain decision the codegen can't make. The codegen ships the verbs (resource-side purge, subject-side purge); the caller's domain model composes them. The codegen does **not** generate a `Delete<Type>()` workflow.

**The codegen's edge over hand-rolled cleanup:** the subject-side purge (delete every tuple where `team:t1` is the subject) is index-suboptimal when expressed as a single filter with no `resource_type` (per #1739: *"if a relationships filter is used without a resource type, there can be a performance impact due to the existing indexes all starting with the resource type"*). The codegen knows the schema — it knows *which definitions allow `team#X` as a subject* — so it emits the subject-side purge as **N per-referencing-resource-type filter deletes** (`{ResourceType: "extsvc/folder", OptionalSubjectFilter: {SubjectType: "extsvc/team", OptionalSubjectId: "t1"}}`, one per referencing definition), which are index-optimal. Hand-written code can't do that without re-reading the schema.

Concrete artifacts:

- `pkg/authz/authz.go` — `Engine` interface gains relationship-filter delete method(s) (exact shape decided by a follow-up ADR — candidates: `DeleteRelationsByResource(ctx, Resource)` + `DeleteRelationsByResourceRelation(ctx, Resource, Relation)` + `DeleteRelationsBySubject(ctx, resourceType Type, subject Type, id ID)`, vs one `DeleteRelationsMatching(ctx, RelationFilter) error` mirroring SpiceDB's `RelationshipFilter`). All `OptionalLimit: 0` — one transactional delete (per A1).
- `pkg/authz/spicedb/crud.go` — implementations wrapping `v1.DeleteRelationshipsRequest` with a `RelationshipFilter` and `OptionalLimit: 0` (matching the existing `DeleteRelations` pattern).
- `internal/templates/object.go.tmpl` — generated:
  - `<Resource>.Purge<Rel>Relations(ctx) error` per relation — delete all tuples of that relation on this resource, any subject.
  - `<Resource>.PurgeRelations(ctx) error` — delete all of this resource's relationships (all relations, any subject) — one filter `{ResourceType, ResourceId}`.
  - `<Type>.PurgeRelationsAsSubject(ctx) error` — delete every tuple where this object is the subject, across the definitions whose schema allows it as a subject — N per-referencing-resource-type filter deletes. Emitted only for types that appear as a subject somewhere in the schema.
- `internal/generator/generator.go` — a template helper to compute, per object type, the list of referencing definitions (for `PurgeRelationsAsSubject`).
- `example/authzed/**.gen.go` — regenerated (new `Purge*` methods); committed.
- `README.md` — a "Relationship Cleanup" section: the verbs; how `Purge*` differs from the targeted `Delete<Rel>Relations`; the two-call lifecycle pattern (resource-side + subject-side); that orphans make `LookupResources` return ghosts and are dangerous on object-ID reuse; the index caveat and how the generated subject-purge sidesteps it; the resilience nuance (write the SpiceDB delete in the same logical op that deletes the object from your store; `OPERATION_DELETE` on DB-transaction failure).
- e2e tests in `extsvc` — a multi-subject resource-side purge, a per-relation purge that leaves other relations intact, and (if a fixture allows) a subject-side purge.

## Success Criteria

1. The `authz.Engine` interface gains filter-delete method(s) (final names/shapes per the follow-up ADR) whose parameters do **not** require a subject type or subject IDs for the resource-side variant. Verifiable: `grep -n "DeleteRelations" pkg/authz/authz.go` shows new method(s) for resource-only (and resource+relation) deletes.

2. `*spicedb.Engine` implements them by calling `e.client.DeleteRelationships` with a `*v1.RelationshipFilter` (`ResourceType` + `OptionalResourceId` [+ `OptionalRelation`] for resource-side; `ResourceType` + `OptionalSubjectFilter{SubjectType, OptionalSubjectId}` for the per-resource-type subject-side variant) and `OptionalLimit: 0`. Verifiable: `grep -n "DeleteRelationships\|RelationshipFilter\|OptionalSubjectFilter" pkg/authz/spicedb/crud.go` shows the calls.

3. The generator emits `func (<resource> <Resource>) Purge<Rel>Relations(ctx context.Context) error` for every relation on every definition, plus `func (<resource> <Resource>) PurgeRelations(ctx context.Context) error` per definition. Verifiable: `grep -c "func (folder Folder) Purge.*Relations(ctx context.Context) error" example/authzed/extsvc/folder.gen.go` equals (number of `relation` lines on `extsvc/folder`) + 1.

4. The generator emits `func (<obj> <Type>) PurgeRelationsAsSubject(ctx context.Context) error` for every object type that appears as a subject of some relation in the schema (and **not** for types that never appear as a subject). Verifiable: `grep -c "func (user User) PurgeRelationsAsSubject" example/authzed/extsvc/user.gen.go` returns `1` (extsvc/user is a subject of folder.viewer etc.); a type that's never a subject has no such method.

5. `PurgeRelationsAsSubject` issues one filter delete per *referencing* resource type — not one no-`resource_type` filter. Verifiable: the generated body for `extsvc/user.PurgeRelationsAsSubject` contains one `engine.DeleteRelationsBySubject(...)` (or equivalent) call per definition that allows `extsvc/user` as a subject; none of those calls passes an empty resource type.

6. `Purge<Rel>Relations` deletes **all** tuples of that relation on the resource, any subject; `PurgeRelations` deletes all relations; neither touches other resources. Verifiable (e2e): write `(folder:p1, viewer, user:a)` + `(folder:p1, viewer, group:g)` + `(folder:p1, owner, user:b)`, call `extsvc.Folder("p1").PurgeViewerRelations(ctx)`, then `ReadViewerUserRelations`/`ReadViewerGroupRelations` are empty and `ReadOwnerUserRelations` is unchanged; separately, `PurgeRelations` clears all three.

7. `go build ./...` exits 0.

8. `go vet ./...` exits 0.

9. Round-trip: `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed && git diff --quiet example/authzed/` exits 0.

10. `go test ./pkg/authz/spicedb/... ./example/authzed/...` passes (or skips cleanly without Docker) — includes the new purge e2e tests.

11. `README.md` gains a section (heading containing `Relationship Cleanup` or `Cleanup`) that (a) shows a `Purge<Rel>Relations` / `PurgeRelations` / `PurgeRelationsAsSubject` call, (b) contrasts `Purge*` with the targeted `Delete<Rel>Relations`, (c) names the object-ID-reuse hazard and the `LookupResources`-ghosts hazard, and (d) describes the two-call lifecycle pattern. Verifiable: `grep -i "purge\|cleanup" README.md` matches; the section includes a `Purge` code example and the strings `reuse` and `LookupResources`.

12. CHANGELOG gains a `[1.15.0]` entry describing the `Engine` filter-delete method(s) and the generated `Purge*` methods. Verifiable: `grep -n "1.15.0\|Purge" CHANGELOG.md` matches.

## Out of Scope

- **A `Delete<Type>()` / cascade workflow** that decides which relations to purge when an object is deleted. Reason: a SpiceDB relation is a semantic edge, not a structural FK — "the right thing" depends on the caller's domain model. SpiceDB itself rejected this (#315 → #1739 = "two calls", not `DeleteObject`). The codegen ships verbs; the app composes them.
- **Re-parenting orphaned children** — when `folder:f1` is deleted, `(folder:f2, parent, folder:f1)` orphans `f2`. Whether `f2` is deleted, re-parented, or kept-as-orphan is a domain decision; the codegen does not implement it. (`f1`'s *own* resource-side `parent` edge — `(folder:f1, parent, folder:f0)` — *is* removed by `f1.PurgeRelations`, since `f1` no longer exists.)
- **The bounded / non-transactional batch-delete loop** (`OptionalLimit: N, OptionalAllowPartialDeletions: true`, loop while `DeletionProgress == PARTIAL`) for objects with millions of relationships (per [authzed/spicedb#1224](https://github.com/authzed/spicedb/issues/1224)). Reason: `OptionalLimit: 0` (one transactional delete) is correct and matches the existing `DeleteRelations` for normal-fan-out objects; the bounded loop is a robustness option to add only if a concrete huge-object need surfaces. If/when added, it'd be an `opts` parameter on the `Engine` methods, not a default change.
- **Optimistic-concurrency preconditions** on the purge (`OptionalPreconditions`). Deferred — the purge is unconditional.
- **Entity CRUD scaffolding** (`Create<Type>` / `Get<Type>` / `Update` / `Exists` / `List<Type>s` per object type). Reason: SpiceDB doesn't store objects — it stores relationships — so these would be leaky entity-store abstractions over the relationship graph (`List<Type>s` is O(relationships), misses zero-relationship objects; `Create<Type>` has nothing to create; `Update` has no fields). Object lifecycle belongs in the caller's real data store; this scope is purely relationship cleanup.

## Risks

- **`SubjectFilter.OptionalRelation` semantics** — a `SubjectFilter` with `OptionalSubjectId` set and `OptionalRelation` *unset* matches the subject across all sub-relations (`team:t1`, `team:t1#admin`, `team:t1#member`, …); setting `OptionalRelation: {Relation: ""}` matches only the ellipsis (no sub-relation). For "purge everything where `team:t1` is the subject" you want the broad form (unset). Mitigation: the SPEC pins this — `PurgeRelationsAsSubject` leaves `OptionalRelation` unset; a comment in the generated code and the SPEC's Constraints record why.

- **No-`resource_type` filter is index-suboptimal** — confirmed by #1739. Mitigation: the codegen never emits a no-`resource_type` subject-side filter; `PurgeRelationsAsSubject` is N per-referencing-resource-type filter deletes (each supplies `ResourceType`), which are index-optimal. SC5 checks this.

- **`Purge*` vs `Delete<Rel>Relations` confusion** — two delete-ish verbs with different semantics (`Purge*` = all subjects, no IDs needed; `Delete<Rel>Relations` = you supply the IDs). Mitigation: distinct verb (`Purge` not `Delete`); the generated methods' Go docs and the README section explicitly contrast them; the README's lifecycle pattern shows `Purge*` as the object-deletion hook and `Delete<Rel>Relations` as the targeted-revoke hook.

- **The ID-reuse / `LookupResources`-ghost hazards make "you should purge" feel mandatory, but the codegen can't enforce when callers call it.** Mitigation: the README documents both hazards prominently and the recommended pattern (purge in the same logical operation that deletes the object from your store; `OPERATION_DELETE` on DB-transaction failure for resilience). The codegen provides the verb, not the enforcement — consistent with the verb-not-workflow stance.

- **Adding to the `Engine` interface is a `pkg/authz/` contract change** — under the v1.10 semver commitment, additive interface methods are a MINOR bump (`v1.15.0`). Mitigation: purely additive (new methods, no signature change to existing ones); the follow-up ADR records the exact shapes; SC12 pins the CHANGELOG entry. The only `Engine` implementation is `*spicedb.Engine`.

- **`PurgeRelationsAsSubject` requires a "which definitions reference type T as a subject" computation** — the codegen must walk all definitions' relations' allowed-types to build this map (including sub-relation references like `team#admin`). Mitigation: the resolver already has the relation/allowed-type data (`RelationView.AllowedTypes`); the template helper iterates it. The SPEC names the exact data source.

## Assumptions

- **A1 [VERIFIED]:** SpiceDB's `DeleteRelationships` RPC accepts a `RelationshipFilter` where `ResourceType` is **optional** (`checkIfFilterIsEmpty` requires only *one* field set); a single call with `OptionalLimit: 0` deletes **all** matching tuples within one `ReadWriteTx` (transactional); `OptionalLimit: N` either errors if more than `N` match (`OptionalAllowPartialDeletions: false`) or deletes up to `N` and returns `DeletionProgress: DELETION_PROGRESS_PARTIAL` (`OptionalAllowPartialDeletions: true`); SpiceDB does not cascade. A filter with no `resource_type` has an index-performance cost. Evidence: `github.com/authzed/spicedb@v1.52.0` `internal/services/v1/relationships.go` (the `DeleteRelationships` handler — `validateRelationshipsFilter` comment *"ResourceType is optional"*, the `OptionalLimit`/`OptionalAllowPartialDeletions` branches, the "kick off an unlimited deletion" path); [PR #1739](https://github.com/authzed/spicedb/pull/1739) (made `resource_type` optional; "two calls"; index caveat); [issue #1224](https://github.com/authzed/spicedb/issues/1224) (large-deletion / non-transactional motivation); [issue #315](https://github.com/authzed/spicedb/issues/315) (closed → two-call answer).

- **A2 [VERIFIED]:** The codegen's design stance is verbs-not-workflows — emit composable primitives the caller's domain model assembles; never bake in cascades/workflows that decide the caller's path. Evidence: confirmed with the user during the design discussion for this feature; reinforced by SpiceDB's own choice (#315 → #1739: no `DeleteObject`); through-line already in the codebase (wildcard grants allowed on any relation, with the "only read-side" policy left to the operator; caveat pre-bind-at-write vs defer-to-check is the caller's choice).

- **A3 [VERIFIED]:** The existing `<Resource>.Delete<Rel>Relations(ctx, <Rel>Objects)` is a *targeted* delete — it requires the relation, subject type, and specific subject IDs; it cannot purge all tuples of a relation, and cannot purge across relations. Evidence: `pkg/authz/authz.go:189` declares `DeleteRelations(ctx, from Resource, relation Relation, subject Type, ids []ID) error`; the generated `DeleteViewerRelations(ctx, FolderViewerObjects)` wraps it; `crud.go` (~line 640) builds a `RelationshipFilter` with `OptionalResourceId` + `OptionalRelation` + a subject filter from the args.

- **A4 [VERIFIED]:** Orphaned tuples are not merely a hygiene issue. A resource-side orphan (`(folder:f1, viewer, user:alice)` after `folder:f1` is deleted) still resolves — so `LookupResources(browse, user:alice)` returns the deleted `folder:f1`, and a recreated `folder:f1` inherits the grant. Evidence: SpiceDB has no notion of object existence independent of tuples; the [writing-relationships blog](https://authzed.com/blog/writing-relationships-to-spicedb) calls leftover tuples "superfluous and safe to have around" only in the narrow sense of *Check against a non-existent resource* — it does not cover Lookup ghosts or ID reuse.

- **A5 [HYPOTHESIS]:** Resource-side purge (delete tuples where the object is the resource) is the more common cleanup need; subject-side purge is somewhat less common but in scope (an object can be a subject elsewhere — a deleted `team:t1` that's a `collab` subject on folders), and feasible per A1. Verification: revisit emission scope if a user reports the per-referencing-resource-type subject purge is too coarse or too fine.

## History

<!-- managed by `harness history-update` — do not hand-edit -->
