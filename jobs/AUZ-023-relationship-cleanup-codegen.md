<!-- approved -->

# AUZ-023: Relationship cleanup codegen

| Field      | Value                                              |
|------------|----------------------------------------------------|
| Status     | Done                                               |
| Created    | 2026-05-10                                         |
| Assignee   | Danh Tran                                          |
| Source     | docs/spec-014-relationship-cleanup-codegen.md      |
| Blocked by | —                                                  |

## Goal

Implement SPEC-014. Add `authz.RelationFilter` (struct + `IsEmpty()`) and `authz.ErrEmptyRelationFilter`, plus a new `Engine` method `DeleteRelationsMatching(ctx, RelationFilter) error` implemented by `*spicedb.Engine` as a `client.DeleteRelationships` call (`OptionalLimit: 0`; `SubjectFilter.OptionalRelation` left nil). Add a `subjectReferences` generator helper (per object type → sorted list of referencing definition namespaces, from `RelationView.AllowedTypes`) and emit three generated methods on the typed object handle: `<Resource>.Purge<Rel>Relations(ctx) error` (per relation, `{ResourceType, ResourceID, Relation}`), `<Resource>.PurgeRelations(ctx) error` (per definition, `{ResourceType, ResourceID}`), and `<Type>.PurgeRelationsAsSubject(ctx) error` (per object type that is a subject somewhere — one `{ResourceType: TypeD, SubjectType, SubjectID}` call per referencing definition D, `errors.Join` the failures, best-effort/idempotent). Regenerate `example/authzed/**.gen.go` (committed, byte-identical round-trip); add e2e tests in `extsvc`; add a "Relationship Cleanup" section to `README.md`; CHANGELOG `[1.15.0]`.

## What Stays Unchanged

- `<Resource>.Delete<Rel>Relations(ctx, <Rel>Objects)` and `Engine.DeleteRelations(ctx, from, relation, subject, ids)` — the targeted revoke; unchanged. `Purge*` is additive.
- `internal/generator/adapter.go` — `RelationView.AllowedTypes` already carries `Namespace`/`SubRelation`; no adapter change.
- Existing generated `<entity>.gen.go` content — only grows the new `Purge*` methods.
- The OPA codegen (`--emit-opa`, `opa.gen.go`) — orthogonal; untouched.
- `cmd/authzed-codegen/main.go` — no new flag; `Purge*` always emits (like `Delete<Rel>Relations`).
- No `Delete<Type>()` cascade, no re-parenting, no bounded/non-transactional batch loop, no preconditions, no entity-CRUD — out of scope per SPEC-014.

## Workstreams

### 1. Runtime — `RelationFilter`, sentinel, Engine method

| # | Task | File | Status |
|---|------|------|--------|
| 1.1 | Add `type RelationFilter struct { ResourceType Type; ResourceID ID; Relation Relation; SubjectType Type; SubjectID ID }` with the doc comment from SPEC-014 (the broad-delete warning) | `pkg/authz/authz.go` | [x] |
| 1.2 | Add `func (f RelationFilter) IsEmpty() bool` — true iff every field is the zero string | same | [x] |
| 1.3 | Add `var ErrEmptyRelationFilter = errors.New("empty relation filter")` | same | [x] |
| 1.4 | Add `DeleteRelationsMatching(ctx context.Context, f RelationFilter) error` to the `Engine` interface (after `DeleteRelations`), with the doc comment from SPEC-014 (one transactional delete; `ErrEmptyRelationFilter` on empty; bounded deletion not exposed) | same | [x] |

**Key details:** purely additive — no existing method/type changes. Per ADR-005 / SPEC-014 the field set is exactly these five; `OptionalResourceIdPrefix` and a sub-relation-scoped `SubjectRelation` are deliberately not included (additive later if needed).

### 2. Engine impl — `*spicedb.Engine.DeleteRelationsMatching`

| # | Task | File | Status |
|---|------|------|--------|
| 2.1 | Implement `func (e *Engine) DeleteRelationsMatching(ctx context.Context, f authz.RelationFilter) error` — return `authz.ErrEmptyRelationFilter` if `f.IsEmpty()`; build `*v1.RelationshipFilter{ResourceType, OptionalResourceId, OptionalRelation}`; if `f.SubjectType != "" || f.SubjectID != ""` set `OptionalSubjectFilter = &v1.SubjectFilter{SubjectType, OptionalSubjectId}` (leave `OptionalRelation` nil — per SPEC C3); call `e.client.DeleteRelationships(ctx, &v1.DeleteRelationshipsRequest{RelationshipFilter: rf})` (no `OptionalLimit` → 0); return the (unwrapped or lightly-wrapped) error | `pkg/authz/spicedb/crud.go` | [x] |
| 2.2 | Add a `debugLog` line consistent with the other `crud.go` methods | same | [x] |

**Key details:** mirror the existing `DeleteRelations` translation style. `OptionalLimit: 0` (omit the field) = one transactional unlimited delete (SPEC A1).

### 3. Generator — `subjectReferences` helper + template func

| # | Task | File | Status |
|---|------|------|--------|
| 3.1 | Add `func subjectReferences(defs []*DefinitionView) map[string][]string` — for each object type appearing in any `RelationView.AllowedTypes[*].Namespace` across `defs`, the set of definition namespaces (`def.ObjectType.String()`) that reference it, returned sorted; ignore `SubRelation` (a `team#admin` reference still counts `extsvc/team` as referenced) | `internal/generator/generator.go` | [x] |
| 3.2 | Register template funcs: `isSubjectType(objType string) bool` (true iff `objType` is a key in `subjectReferences`) and `referencingDefinitions(objType string) []string` (the sorted slice; empty if not a subject type) — both close over the `subjectReferences` map computed once per `GenerateObjectSource` run | same | [x] |

**Key details:** the map is computed in `GenerateObjectSource` (where `g.Definitions` is in scope), before `tmpl.Execute`, and the funcs close over it. SPEC C4 requires the referencing-definitions slice be sorted (deterministic output).

### 4. Template — the three `Purge*` methods

| # | Task | File | Status |
|---|------|------|--------|
| 4.1 | `<Resource>.Purge<Rel>Relations(ctx) error` per relation — body: `return authz.GetEngine(ctx).DeleteRelationsMatching(ctx, authz.RelationFilter{ResourceType: Type<Resource>, ResourceID: authz.ID(<receiver>), Relation: authz.Relation(<Resource><Rel>)})`; Go doc contrasting it with `Delete<Rel>Relations` | `internal/templates/object.go.tmpl` | [x] |
| 4.2 | `<Resource>.PurgeRelations(ctx) error` per definition — body: `return authz.GetEngine(ctx).DeleteRelationsMatching(ctx, authz.RelationFilter{ResourceType: Type<Resource>, ResourceID: authz.ID(<receiver>)})`; Go doc (clears all resource-side tuples; does NOT touch subject-side — see `PurgeRelationsAsSubject`) | same | [x] |
| 4.3 | `<Type>.PurgeRelationsAsSubject(ctx) error` — emitted only when `isSubjectType` is true for this definition's object type; body: iterate `referencingDefinitions` in order, one `eng.DeleteRelationsMatching(ctx, authz.RelationFilter{ResourceType: authz.Type("<D>"), SubjectType: Type<Type>, SubjectID: authz.ID(<receiver>)})` per `D`, collecting `fmt.Errorf("purge <type> as subject of <D>: %w", err)` into `errs`, `return errors.Join(errs...)`; Go doc per SPEC-014 | same | [x] |
| 4.4 | Ensure the template's import block for generated files includes `context`/`errors`/`fmt` when any `PurgeRelationsAsSubject` is emitted (added a `$isSubj` guard so empty-but-subject definitions like `extsvc/customer` still import `context`/`errors`/`fmt`) | same | [x] |

**Key details:** `Type<D>` for a referencing definition `D` whose namespace is e.g. `extsvc/folder` is the generated `TypeFolder` constant — but that constant lives in the `extsvc` package; since `PurgeRelationsAsSubject` for `extsvc/user` is generated into the `extsvc` package too (same prefix), `TypeFolder` resolves. **Open question for WS4 implementation:** if a subject type is referenced by a definition in a *different* package (cross-prefix reference, e.g. `bookingsvc/booking` allows `extsvc/user` as a subject) — `extsvc/user`'s `PurgeRelationsAsSubject` (in package `extsvc`) would need `bookingsvc.TypeBooking`, requiring an import of the `bookingsvc` package. Check whether the fixture schema has cross-prefix subject references; if so, the generated code needs the cross-package import (or `PurgeRelationsAsSubject` uses `authz.Type("bookingsvc/booking")` string literals instead of the typed constant to avoid the import). Decide during implementation; record in Discoveries.

### 5. Regenerate fixtures

| # | Task | File | Status |
|---|------|------|--------|
| 5.1 | `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed` | (regenerate) | [x] |
| 5.2 | Verify `example/authzed/extsvc/folder.gen.go` has `func (folder Folder) PurgeViewerRelations(ctx context.Context) error`, `func (folder Folder) PurgeRelations(ctx context.Context) error`, etc.; `example/authzed/extsvc/user.gen.go` has `func (user User) PurgeRelationsAsSubject(ctx context.Context) error` | `example/authzed/extsvc/*.gen.go` | [x] |
| 5.3 | Verify a type referenced as a subject by nothing has NO `PurgeRelationsAsSubject` method | (verify) | [x] |
| 5.4 | Round-trip: regen twice → identical (md5 or `git diff --quiet example/authzed/` after a second run from clean) | (verify) | [x] |

### 6. e2e tests — `extsvc`

| # | Task | File | Status |
|---|------|------|--------|
| 6.1 | `TestFolder_PurgeViewerRelations` — seed `viewer` (user + group) + `any_parent` (folder) on `folder:pg-fv1`; call `PurgeViewerRelations(ctx)`; assert `ReadViewerUserRelations`/`ReadViewerGroupRelations` empty, `ReadAnyParentFolderRelations` unchanged (used `any_parent` as the bystander since `folder` has no non-caveat `owner` relation) | `example/authzed/extsvc/extsvc_purge_test.go` | [x] |
| 6.2 | `TestFolder_PurgeRelations` — seed `viewer` + `any_parent` on `folder:pg-fr1`; call `PurgeRelations(ctx)`; assert all reads empty | same | [x] |
| 6.3 | `TestUser_PurgeRelationsAsSubject` — write `(folder:pg-f-subj, viewer, user:pg-u-subj)`; call `extsvc.User("pg-u-subj").PurgeRelationsAsSubject(ctx)` (iterates article/document/folder/team — all extsvc defs referencing extsvc/user); assert `ReadViewerUserRelations` on the folder no longer includes the user | same | [x] |
| 6.4 | `TestEngine_DeleteRelationsMatching_EmptyFilter` — `sb.Engine.DeleteRelationsMatching(ctx, authz.RelationFilter{})` returns `authz.ErrEmptyRelationFilter` (placed in `extsvc_purge_test.go` — no `pkg/authz/spicedb/*_test.go` exists, and `sb.Engine` is reachable from the extsvc test package) | same | [x] |

### 7. README — "Relationship Cleanup" section

| # | Task | File | Status |
|---|------|------|--------|
| 7.1 | Add a section (heading "Relationship Cleanup"): (a) `Purge<Rel>Relations` / `PurgeRelations` / `PurgeRelationsAsSubject` code examples; (b) contrast `Purge*` (all subjects, no IDs) with the targeted `Delete<Rel>Relations` (you supply the IDs); (c) the hazards — orphaned resource-side tuples make `LookupResources` return ghosts, and object-ID reuse makes a recreated object inherit dead grants; (d) the two-call lifecycle pattern (on object deletion: `PurgeRelations` resource-side, plus `PurgeRelationsAsSubject` if the type is a subject anywhere — idempotent, not jointly atomic, re-run on failure; `OPERATION_DELETE` on DB-transaction failure for resilience); (e) note `DeleteRelationsMatching` for hand-written callers + the broad-filter caveat; link `docs/scope-relationship-cleanup-verbs.md`, `docs/ADR-005-engine-filter-delete.md`, `docs/spec-014-relationship-cleanup-codegen.md`. Placed after "Behavior Notes", before "Verification". Includes the SC11-required strings `reuse` and `LookupResources` + a `Purge` code example. | `README.md` | [x] |

**Key details:** SPEC-014 SC11 requires the section include a `Purge` code example and the strings `reuse` and `LookupResources`. Place the section after "Behavior Notes" (or near the existing delete/write content) — wherever fits the README's flow.

### 8. Verification

| # | Task | Status |
|---|------|--------|
| 8.1 | `go build ./...` exits 0 | [x] |
| 8.2 | `go vet ./...` exits 0 | [x] |
| 8.3 | `go mod tidy` produces no diff (no new deps expected) | [x] |
| 8.4 | `go test ./pkg/authz/spicedb/... ./example/authzed/...` passes (or skips cleanly without Docker) — includes the new purge e2e tests | [x] |
| 8.5 | Round-trip: regen is deterministic (a second `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed` leaves the tree byte-identical). Note: `git diff --quiet example/authzed/` is non-zero here only because the AUZ-023 codegen delta is uncommitted vs HEAD — expected; the determinism bar (regen→regen identical) holds. | [x] |

### 9. CHANGELOG

| # | Task | File | Status |
|---|------|------|--------|
| 9.1 | Add a `[1.15.0]` entry: the additive `authz.RelationFilter` + `authz.ErrEmptyRelationFilter` + `Engine.DeleteRelationsMatching`; the generated `Purge<Rel>Relations` / `PurgeRelations` / `PurgeRelationsAsSubject`; note this is MINOR (additive interface method); link the scope/ADR-005/SPEC-014; mention the verbs-not-workflows framing (no `Delete<Type>()` cascade) | `CHANGELOG.md` | [x] |

## Design Decisions

### `PurgeRelationsAsSubject` is best-effort
Per SPEC-014: when `PurgeRelationsAsSubject` makes N per-referencing-resource-type calls and one fails, the rest still run; the method returns `errors.Join` of the failures (nil if all succeeded). Rationale: a cleanup op should remove as much as possible even if one resource type's delete errors; the op is idempotent, so the caller re-runs the whole thing. Recorded in SPEC-014's Unresolved-resolved.

### `PurgeRelations` and `Purge<Rel>Relations` both exist
`PurgeRelations(ctx)` = one `{ResourceType, ResourceID}` call clearing all of a resource's tuples atomically (the "object deleted" hook). `Purge<Rel>Relations(ctx)` = one `{ResourceType, ResourceID, Relation}` call clearing one relation entirely (the "this relation no longer applies" hook). Distinct uses; both emitted.

### `Purge*` always emits — no flag
Unlike `--emit-opa`, the `Purge*` methods are always generated (like `Delete<Rel>Relations`, `Create<Rel>Relations`). They add no new dependency and their presence is harmless until called.

### `RelationFilter` field set
Exactly the five the codegen uses (`ResourceType`, `ResourceID`, `Relation`, `SubjectType`, `SubjectID`). `OptionalResourceIdPrefix` and a sub-relation-scoped `SubjectRelation` are not included — no current use, additive later (SPEC C6). `PurgeRelationsAsSubject` deliberately wants the all-sub-relations behavior, which is the only behavior without a `SubjectRelation` field.

## Implementation Order

```
WS1 — Runtime: RelationFilter + sentinel + Engine method   ← unblocks WS2, WS4
   ▼
WS2 — Engine impl: *spicedb.Engine.DeleteRelationsMatching  ← depends on WS1
   ▼
WS3 — Generator: subjectReferences + template funcs        ← unblocks WS4
   ▼
WS4 — Template: Purge<Rel>Relations / PurgeRelations / PurgeRelationsAsSubject  ← depends on WS1 + WS3
   ▼
WS5 — Regenerate fixtures                                  ← depends on WS2 + WS4
   ▼
WS6 — e2e tests        ┐ both depend on WS5
WS7 — README section    ┤ WS7 can parallel WS6
   ▼                    │
WS8 — Verification      ┘ ← depends on WS1–7
   ▼
WS9 — CHANGELOG          ← parallel to WS8
```

## Notes

- **Cross-prefix subject references (WS4.3)** — if the fixture schema has a definition in package P1 that allows an object type from package P2 as a subject, P2's `PurgeRelationsAsSubject` (generated into package P2) would need P1's `Type<...>` constant — a cross-package import in generated code. Resolve in WS4: either import the other package, or have `PurgeRelationsAsSubject` use `authz.Type("p1/def")` string literals (no import, but loses the typed-constant link). Check `example/schema.zed` for cross-prefix subject references first; record the decision in Discoveries. (If there are none, this is moot — every `PurgeRelationsAsSubject`'s referencing definitions share the subject type's prefix.)
- **`subjectReferences` and the existing resolver** — the data is already in `RelationView.AllowedTypes` (`Namespace`, `SubRelation`); the helper just walks `g.Definitions[*].Relations[*].AllowedTypes[*].Namespace`. No resolver change; no `adapter.go` change.
- **OPA round-trip** — the round-trip check uses `--emit-opa` (per the updated CLAUDE.md verify loop), so `opa.gen.go` regenerates too; the `Purge*` additions are in `<entity>.gen.go`, not `opa.gen.go` — but verify both diff cleanly.
- **Test file location** — `pkg/authz/spicedb/` may not have a `_test.go` today; WS6.4's empty-filter test can go in `extsvc_test.go` (it just needs `sb.Engine`) if creating a `pkg/authz/spicedb/spicedb_test.go` is more friction than it's worth. Decide in WS6.

## Discoveries & Decisions During Implementation

### [Implementer] `DeleteRelations` and `DeleteRelationsMatching` use different SpiceDB RPCs
The pre-existing targeted `*spicedb.Engine.DeleteRelations(ctx, from, relation, subject, ids)` does **not** call `DeleteRelationships` — it issues a `WriteRelationships` with `RELATIONSHIP_OPERATION_DELETE` updates, one per ID. The new `DeleteRelationsMatching` is the first place in `crud.go` that uses the `DeleteRelationships` RPC (filter-based, server-side). They coexist: enumerated-IDs revoke → `WriteRelationships`; filter purge → `DeleteRelationships`. Not a conflict, just worth knowing — the two delete paths look similar from the typed surface but hit different gRPC methods.

### [Implementer] `PurgeRelationsAsSubject` references referencing-defs by string literal, not typed constant
WS4.3's open question (cross-prefix subject references → cross-package import in generated code) resolved in favor of `authz.Type("prefix/def")` string literals over the typed `Type<D>` constant. The fixture schema (`example/schema.zed`) has **no** cross-prefix subject references — every subject type's referencing definitions share its package — so the typed constant *would* have compiled. But the literal is robust to a future cross-prefix schema with zero generated-code import churn, and the readability loss is minor (the filter is internal plumbing, not a public constant). The generator's `referencingDefinitions` returns the bare namespace strings (`"extsvc/folder"`), which the template wraps in `authz.Type(...)`.

### [Implementer] Empty-but-subject definitions need conditional imports
`extsvc/customer` (and a few `bookingsvc`/`menusvc` definitions) declare zero relations but appear as allowed subjects elsewhere — so they get a `PurgeRelationsAsSubject` method, which needs `context`, `errors`, `fmt`. The template's import block previously gated `context` on `len(.Relations) > 0`; added a parallel `$isSubj := isSubjectType ...` guard so the import block emits `context`/`errors`/`fmt` when the definition is a subject type even with no relations. Verified `bookingsvc/customer.gen.go` compiles. The Go doc on `PurgeRelations` (emitted for every definition, including resource-only ones like `extsvc/article`) references `PurgeRelationsAsSubject` with the parenthetical "(emitted when X is a subject anywhere in the schema)" — accurate whether or not the method is actually present, so no conditional-comment complexity was added.

### [Implementer] Subject-arg test bystander relation
WS6.1/6.2's plan said seed `viewer` + `owner` on a folder, but `extsvc/folder` has no plain `owner` relation (it has many caveat-bearing relations and a handful of `folder→folder` parent relations). Used `any_parent` (`extsvc/folder`, no caveat) as the "untouched bystander" relation instead — `PurgeViewerRelations` clears `viewer`, leaves `any_parent` intact; `PurgeRelations` clears both.

### [Implementer] Expanded edge-case e2e coverage (post-review "test carefully")
`extsvc_purge_test.go` grew from the 4 SPEC-mandated tests to 11 (+3 sub-tests) covering: idempotency / empty-target filter (no-match delete is not an error), resource-ID isolation (sibling resource untouched), wildcard-tuple deletion (`PurgeGuestRelations` removes `guest, user:*` — the filter is by (resource, relation), shape-agnostic), the two-call lifecycle (`PurgeRelations` resource-side leaves the subject-side `(child, any_parent, parent)` orphan; `PurgeRelationsAsSubject` finishes it — directly demos the README hazard), userset-subject removal (`team:t#admin` matched by a `{folder, team, t}` filter with nil `OptionalSubjectFilter.OptionalRelation` — verifies SPEC C3), the resource-type-less `{SubjectType, SubjectID}` raw form (SpiceDB accepts a `RelationshipFilter` with no `resource_type` — the single-RPC alternative to the `PurgeRelationsAsSubject` fan-out), and the three filter shapes the generated wrappers are built from. `TestUser_PurgeRelationsAsSubject` was also strengthened to seed two referencing definitions (`folder.viewer` + `document.owner`) + a bystander subject. No code changes — all behaviors were already correct; the tests pin them.

### [Implementer] Round-trip "git diff" caveat
`go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed && git diff --quiet example/authzed/` exits non-zero in this job because the AUZ-023 codegen delta is uncommitted vs HEAD — that's expected, not a regression. The determinism bar (regenerate twice → byte-identical) holds (md5-verified). Once committed, the standard `git diff --quiet` round-trip check passes again.
