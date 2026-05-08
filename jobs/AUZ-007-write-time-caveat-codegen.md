# AUZ-007: Write-time Caveat Codegen

| Field      | Value                                                                              |
|------------|------------------------------------------------------------------------------------|
| Status     | Done                                                                               |
| Created    | 2026-05-08                                                                         |
| Assignee   | danhtran94                                                                         |
| Source     | docs/spec-003-write-time-caveat-codegen.md (Draft — finalized 2026-05-08, not yet flipped to Accepted) |
| Blocked by | —                                                                                  |

<!-- approved -->


## Goal

Close AUZ-006's write-side gap: the codegen `Create<Rel>Relations` method emits `OptionalCaveat` for `with caveat` relations so callers no longer need to bypass the generated API via `authzed.Client.WriteRelationships` directly. After this job, the AUZ-006 e2e tests (`TestFolder_CheckTenantedBrowse_*`) drop the `writeTenantedViewer` helper and call `CreateTenantedViewerRelations` directly with `UserCaveat: nil` (the deferred-binding pattern — empirically verified as a wire-legal SpiceDB equivalent of the helper's name-only attach). A new wildcard + caveat fixture exercises the both-modes path. The codegen API is permissive on nil Caveat fields per SPEC-003 A6 + C7 — there is no `ErrCaveatRequired` sentinel.

## Problem

    Current (post-bdb0764):
      caller → CreateTenantedViewerRelations(ctx, FolderTenantedViewerObjects{User: [...]})
                  → engine.CreateRelations(ctx, ..., []ID{...})
                      → SpiceDB rejects (with-caveat relation, no OptionalCaveat) ✗

    AUZ-006 e2e tests work around this:
      test → sb.Client.WriteRelationships(ctx, &Request{Updates: [...] OptionalCaveat: ...})
                                               ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
                                               bypasses the codegen entirely

## Solution: Per-allowed-type caveat routing

    After AUZ-007:
      caller → CreateTenantedViewerRelations(ctx, FolderTenantedViewerObjects{
                  User:       [...],
                  UserCaveat: &TenantMatchArgs{Tenant: "acme"},   // pre-bind
                })                                                   ─OR─
                  UserCaveat: nil,                                   // defer-all
                ├─► caveatCtx = nil if UserCaveat is nil, else map from typed Args
                └─► engine.CreateRelationsWithCaveat(ctx, ..., "extsvc/tenant_match", caveatCtx)
                      └─► OptionalCaveat: {CaveatName, Context} ▶ SpiceDB accepts ✓
                          (Context is nil for defer-all, *structpb.Struct for pre-bind)

### Components

**`Engine.CreateRelationsWithCaveat`** — new runtime interface method
- Mirrors `CreateRelations` plus `caveatName string, caveatParams map[string]any`
- Implemented on `*spicedb.Engine`; emits `OptionalCaveat` per `RelationshipUpdate`
- Permissive on `caveatParams = nil` → wire `OptionalCaveat.Context = nil` (name-only attach)

**Generated `<Rel>Objects`** — per-type Caveat fields
- `<TypeName>Caveat *<CaveatPascal>Args` per caveated allowed type, grouped after ID-slice fields and after the optional `Wildcards` sub-struct

**Generated `Create<Rel>Relations`** — per-type routing
- Caveated branches: build ctx map from typed Args (nil Args → nil ctx → name-only attach), call `CreateRelationsWithCaveat` with caveat-name literal
- Non-caveated branches: existing `CreateRelations` (unchanged)
- Wildcard branches: same Caveat field consumed by both regular + wildcard paths when allowed type is both

## Workstreams

### 1. Runtime interface

Foundation. Adds the new Engine interface method.

| # | Task | File | Status |
|---|------|------|--------|
| 1.1 | Add `CreateRelationsWithCaveat(ctx, to, relation, subject, ids, caveatName string, caveatParams map[string]any) error` to `authz.Engine` interface | `pkg/authz/authz.go` | [ ] |

**Key details:** Build fails between 1.1 and 2.x because `var _ authz.Engine = &spicedb.Engine{}` assertion needs the impl. Land WS1+WS2 in one batch — see Implementation Order. No new sentinel error — codegen wrapper is permissive on nil per SPEC-003 C7.

### 2. SpiceDB engine implementation

Implement `CreateRelationsWithCaveat` on `*spicedb.Engine`.

| # | Task | File | Status |
|---|------|------|--------|
| 2.1 | Add `(*Engine).CreateRelationsWithCaveat` — mirrors `CreateRelations` body plus `OptionalCaveat: &v1.ContextualizedCaveat{CaveatName, Context}` from `serializeCaveatMap`; calls `e.setToken(res.WrittenAt.Token)` on success | `pkg/authz/spicedb/crud.go` | [ ] |

**Key details:** No new imports beyond AUZ-006 (`structpb` already direct). Engine layer is permissive on `caveatParams = nil` — strictness lives only in the codegen wrapper.

### 3. Template emission

Extend the codegen template with per-allowed-type Caveat fields and per-type routing.

| # | Task | File | Status |
|---|------|------|--------|
| 3.1 | Add second range pass over `$rel.AllowedTypes` in `<Rel>Objects` struct body, guarded on `$relType.CaveatName != ""`, emitting `<TypeName>Caveat *<CaveatPascal>Args` fields after the ID-slice fields and Wildcards sub-struct | `internal/templates/object.go.tmpl` | [ ] |
| 3.2 | In `Create<Rel>Relations` regular branch, switch on `{{ if $relType.CaveatName }}` per allowed type — caveated path emits `var <type>CaveatCtx map[string]any` + conditional ctx-map build (`if objects.<Type>Caveat != nil { ... }`) + `CreateRelationsWithCaveat(...)` call with embedded caveat-name literal. Nil Args struct → nil ctx → name-only attach on wire | same | [ ] |
| 3.3 | Mirror the per-type branching in the wildcard sub-block (`if objects.Wildcards.<Type>` path) so wildcard + caveat allowed types route through `CreateRelationsWithCaveat` with `[]authz.ID{authz.WildcardID}` and the same `<Type>Caveat` field | same | [ ] |

**Key details:** All new branches guarded by `{{ if $relType.CaveatName }}` — caveat-free schemas regenerate byte-identically (SPEC-003 C6).

### 4. Regenerate example fixture

Run codegen against existing `example/schema.zed`. Round-trip baseline shifts — `folder.gen.go` gains new emission for `tenanted_viewer`.

| # | Task | File | Status |
|---|------|------|--------|
| 4.1 | Run codegen; inspect `folder.gen.go` diff to confirm `UserCaveat *TenantMatchArgs` field on `FolderTenantedViewerObjects` and `CreateTenantedViewerRelations` body emits `CreateRelationsWithCaveat(..., "extsvc/tenant_match", userCaveatCtx)` | `example/authzed/extsvc/folder.gen.go` | [ ] |
| 4.2 | `go build ./...` + `go vet ./...` clean against regenerated output | (verification only) | [ ] |
| 4.3 | Run codegen a second time; confirm `git diff --quiet example/authzed/` exits 0 (idempotent at new baseline) | (verification only) | [ ] |

### 5. Testing

Port AUZ-006 tests off the bypass directly (deferred-binding via `UserCaveat: nil`); add wildcard+caveat fixture and tests; add a pre-binding test to exercise the non-nil Args path.

| # | Task | File | Status |
|---|------|------|--------|
| 5.1 | Replace each `writeTenantedViewer(ctx, t, folderID, userID)` call in the 3 existing tests with `extsvc.Folder(folderID).CreateTenantedViewerRelations(ctx, FolderTenantedViewerObjects{User: []User{userID}, UserCaveat: nil})` — preserves the deferred-binding pattern by passing nil at write, supplying `Tenant` at check time. Delete the `writeTenantedViewer` helper | `example/authzed/extsvc/extsvc_test.go` | [ ] |
| 5.2 | Add `TestFolder_CreateTenantedViewer_PreBound_WriteWins` — write with `UserCaveat: &TenantMatchArgs{Tenant: "acme"}`; check with mismatched check-time `Tenant: "other"`; assert grant (write-time wins per SPEC-003 A6 [A3]). Locks in the pre-binding semantics so future regressions surface | same | [ ] |
| 5.3 | Append `relation guarded_viewer: extsvc/user:* with extsvc/tenant_match` + `permission guarded_browse = guarded_viewer` to `example/schema.zed` (Authzed docs confirm wildcard + caveat is schema-legal — see SPEC-003 A5). Regenerate; add `TestFolder_CreateGuardedViewer_Wildcard` exercising `objects.Wildcards.User = true` with both `UserCaveat: nil` (defer) and `UserCaveat: &TenantMatchArgs{Tenant: "acme"}` (pre-bind) variants | `example/schema.zed` + `example/authzed/extsvc/folder.gen.go` + test file | [ ] |
| 5.4 | Run `go test ./pkg/authz/spicedb/... ./example/authzed/...` — all pass (Docker required) | (verification only) | [ ] |

## Design Decisions

### Atomic landing of WS1+WS2

The interface method without an implementation breaks `var _ authz.Engine = &spicedb.Engine{}` in `crud.go`. WS1 and WS2 land in one batch (single build-check) — same pattern as AUZ-006 Tasks 4+5. Tolerating a transient broken state mid-batch is wasteful and confuses build-check semantics.

### Caveat name as string literal in generated code

The codegen embeds `"extsvc/tenant_match"` as a literal in `CreateRelationsWithCaveat`. Per SPEC-003 C1, this makes caveat identity part of the generated code's contract — callers cannot substitute a different caveat through the typed API. Alternative (a `CaveatName string` field on `<Rel>Objects`) was rejected for the same reason.

### Caveat field name uses TypeName, not CaveatPascal

Two allowed types in the same namespace gated by the same caveat (`viewer: user with cav | group with cav`) yield distinct field names (`UserCaveat`, `GroupCaveat`) sharing one `<CaveatPascal>Args` struct. Per SPEC-003 C8.

### Permissive nil semantics (codegen + engine) — no `ErrCaveatRequired`

Both layers accept nil and translate it to `OptionalCaveat.Context = nil` on the wire. Empirical probe (SPEC-003 A6) confirmed this is a SpiceDB-supported "name-only attach" equivalent to the AUZ-006 helper pattern. The deferred-binding pattern is the right shape for caveats whose parameters are exclusively request-data (set by the caller at check time). Erroring at the codegen boundary on nil — the original draft of this plan — would block legitimate use cases without adding any safety: write-time wins on collision (SPEC-003 A6 [A3]), so omitting binding cannot bypass policy.

### `writeTenantedViewer` helper is removed, not retained

After AUZ-007 the bypass is no longer needed. We delete the helper because keeping it would imply the codegen still has a write-side gap. Test-infra additions from AUZ-006 (`Sandbox.Client`, `Engine.SetSnapshotToken`) remain available for future tests that legitimately need direct API access — they're now optional rather than required.

## What Stays Unchanged

- `Delete<Rel>Relations` and `Engine.DeleteRelations` (per SPEC-003 A3 + C5: 6-column tuple identity ignores caveat metadata on DELETE)
- `Read<Rel><Type>Relations` and read-side caveat metadata surface (out of scope)
- AUZ-006's `Check<Perm>` path, `permCaveat` walker, per-permission caveat cap, `<CaveatPascal>Args` struct emission
- `cmd/authzed-codegen/main.go` (AUZ-006 wiring suffices)
- `bookingsvc`, `menusvc`, and non-caveat `extsvc` parts of `example/authzed/` — round-trip stays byte-identical
- `Sandbox.Client` field and `Engine.SetSnapshotToken` method

## Implementation Order

    1. WS1+WS2 (atomic: interface + sentinel + spicedb impl)  ← single build-check
    2. WS3       (template)                                    ← independent of 1
    3. WS4       (regenerate fixture)                          ← validates 1+2+3
    4. WS5       (testing)                                     ← end-to-end proof

WS1+WS2 must land together because the `var _ authz.Engine = &Engine{}` assertion in `crud.go` requires the concrete to satisfy the interface. WS3 can technically land before WS1+WS2 (template doesn't compile in isolation) but the regen in WS4 needs all three.

## Notes

- Caveat-name embedding: codegen emits the prefixed name (`"extsvc/tenant_match"`) per SPEC-003 example output
- Both engine and codegen wrapper are permissive on nil — translates to `OptionalCaveat.Context = nil` on wire (SPEC-003 C7, A6)
- Round-trip is the regression bar — after AUZ-007 lands, regen at the new baseline must be idempotent
- `serializeCaveatMap` reused unchanged from AUZ-006
- Wildcard + caveat schema declaration is officially supported (Authzed docs example: `relation viewer: user:* with has_matching_group_id`) — WS5 task 5.3 expected to compile cleanly

## Discoveries & Decisions During Implementation

### [Implementer] Pre-execution research flipped the Nil semantics decision

The original AUZ-007 plan picked `Nil = error` (codegen wrapper returns `ErrCaveatRequired` when `<Type>Caveat` is nil). User asked for online research before approval; that surfaced two binding facts: (a) Authzed docs explicitly endorse the deferred-binding pattern where write-time omits parameters and check-time provides them; (b) `OptionalCaveat{CaveatName, Context: nil}` is wire-legal — empirical probe (SPEC-003 A6 [B1]) confirmed SpiceDB stores it cleanly. Erroring at the codegen boundary on a wire-legal pattern would have blocked the AUZ-006 test pattern's port and any production use case where caveat parameters are exclusively request-data. Decision was flipped to `Nil = name-only attach` BEFORE any code was written — saved a sentinel error definition and ~30 lines of generated nil-guard logic per caveated branch.

### [Implementer] Empirical merge-precedence probe doubled as A6 evidence

The probe written to verify the per-key union + write-time precedence rules (against a fresh SpiceDB v1.52 container) directly populates SPEC-003 Assumption A6's evidence field. Seven scenarios verified across three test caveats — including the attacker-override case [A3], nil-Context write [B1], CONDITIONAL with `missing_required_context` field name [A2, B2], and same-key collision with write-time wins [C1]. The `TestFolder_CreateTenantedViewer_PreBound_WriteWins` test added in WS5.2 locks the [A3] scenario at the codegen integration level — a future regression in SpiceDB or our serialization would surface here.

### [Implementer] `caveatCtx` variable shadowing across regular + wildcard branches

The generated `Create<Rel>Relations` body declares `var caveatCtx map[string]any` inside both the `if len(objects.<Type>) > 0 { ... }` block and the sibling `if objects.Wildcards.<Type> { ... }` block when the allowed type is both wildcard + caveated. Both blocks use the same name. Go's per-`{}`-block scoping makes this safe — the two `caveatCtx` declarations live in disjoint scopes and never collide. Pattern verified by `TestFolder_CreateGuardedViewer_*` exercising both branches with the same `UserCaveat` field.

### [Implementer] Schema delta isolated to extsvc/folder; round-trip clean elsewhere

Adding the wildcard+caveat fixture (`relation guarded_viewer: extsvc/user:* with extsvc/tenant_match` + `permission guarded_browse = guarded_viewer`) regenerated only `example/authzed/extsvc/folder.gen.go`. The other 17 generated files in `bookingsvc/`, `menusvc/`, and the non-folder `extsvc/*` are byte-identical to the AUZ-006 baseline. Caveat-free template branches stayed dormant per SPEC-003 C6.

### [Implementer] Latent `serializeCaveatMap` bug exposed by multi-type fixtures (post-merge extension)

After AUZ-007 closed, additional fixtures (`within_window` with `list<string>` + `string`, `quota_check` with `bool` + `int`, mixed-branch `collaborator` relation) were added to broaden caveat coverage. First runs failed with `serialize caveat params: proto: invalid type: []string` for any caveat carrying a `list<T>` parameter. Root cause: `structpb.NewStruct` only accepts `[]any` for ListValue encoding, not typed slices like `[]string`/`[]int64` — the AUZ-006 implementation passed user maps through unchanged. AUZ-006 missed this because its only caveat was string-only. Fixed by adding `coerceStructpbMap` / `coerceStructpbValue` / generic `toAnySlice[T any]` helpers in `pkg/authz/spicedb/crud.go` to convert typed slices to `[]any` (and recurse through nested maps) at the wire boundary. The fix lives in the runtime layer so the codegen stays simple — generated call sites continue passing typed slices directly, the engine converts at the wire edge. Same fix applies to both `CreateRelationsWithCaveat` (write side) and `CheckPermissionWithCaveat` (read side) since both call `serializeCaveatMap`.

### [Implementer] Caveat coverage breadth — fixtures across all supported parameter types

Added beyond the original AUZ-007 scope to lock in codegen behavior for caveats encountered in production schemas:

- **Multi-param caveat** (`within_window(allowed_actions list<string>, requested_action string)`) — exercises multi-key context-map building and `list<string>` Go type emission. Added `act` permission. 3 tests: defer-all + grant, defer-all + deny, pre-bind-both + write-wins-on-action.
- **Mixed caveated/non-caveated allowed types** (`collaborator: extsvc/user with extsvc/within_window | extsvc/group`, `permission collaborate = collaborator`) — User branch routes through `CreateRelationsWithCaveat`, Group through `CreateRelations`, both within one method body. 3 tests: User-caveated, Group-non-caveated, both-branches-in-one-call.
- **Bool + int param types** (`quota_check(within_quota bool, max_uses int)`) — exercises `caveatTypeToGo` for non-string types (`int → int64`, `bool → bool`). Added `rate_limited` relation + `rate_check` permission. 2 tests.
- **Double param** (`min_score(min_required double, current double)`) — exercises `double → float64` mapping. 2 tests covering above-min grant + below-min deny.
- **Bytes param** (`has_token(token bytes) { size(token) > 0 }`) — exercises `bytes → []byte` mapping. 1 test.
- **Uint param** (`version_check(min_version uint)`) — exercises `uint → uint64`. 1 test.
- **Nested list type** (`matrix_check(rows list<list<string>>) { size(rows) > 0 }`) — generates `Rows [][]string`; exercises the reflection-based slice coercion in `coerceStructpbValue` for typed slices the type-switch doesn't enumerate. 1 test.
- **Delete on caveated tuple** — verifies SPEC-003 C5/A3 empirically. Write a caveated tuple via codegen, verify Check grants, delete via plain `Delete<Rel>Relations` (no caveat), verify Check now denies. The 6-column tuple-identity match removes the caveated tuple without needing `OptionalCaveat` on the wire.

Final caveat test count: 20 across all dimensions (parameter shape, Go type emission, binding pattern, codegen path, delete semantics). All pass against live SpiceDB.

### [Implementer] SpiceDB schema parser rejects zero-parameter caveats

Tried to add a zero-param caveat fixture (`caveat extsvc/always_true() { true }`) to exercise the codegen path where `<Pascal>Args` is an empty struct. SpiceDB's schema compiler rejected it: `parse error: line N, column 27: Unexpected token at root level: TokenTypeRightParen`. The grammar requires at least one parameter between the parens. Fixture removed; the codegen path for empty-`Args` structs remains theoretically reachable (the template produces `caveatCtx = map[string]any{}` which `serializeCaveatMap` collapses to `(nil, nil)`) but cannot be exercised with a real schema.

### [Implementer] Post-AUZ-007 restructure — nested `Caveats` on Objects + read-side cap lift

After the original AUZ-007 work shipped, two iterative refactors landed inline (still under this job since SPEC-003 wasn't yet pushed and the changes are coherent extensions of the same surface):

**Phase A — Objects-side `Caveats` nesting (mirrors Wildcards pattern).** Replaced flat `<TypeName>Caveat *<CaveatPascal>Args` fields on `<Rel>Objects` with a nested `Caveats <RelName>Caveats` sub-struct, exactly mirroring the existing `Wildcards <RelName>Wildcards` pattern. New template helper `anyCaveat` parallels `anyWildcard`. Cleaner discovery (`objects.Caveats.<Type>` ↔ `objects.Wildcards.<Type>`), no top-level pollution when relations have multiple caveated allowed types, no field-name collision risk with allowed-type names. ~13 test sites migrated from `UserCaveat: x` to `Caveats: extsvc.<RelName>Caveats{User: x}` (or omitted entirely for the nil-defer case — zero-value sub-struct gives identical name-only-attach semantics).

**Phase B — Read-side caveat cap lifted.** AUZ-006's `permCaveat` walker errored when 2+ distinct caveats were reachable from one permission. The cap was a codegen simplification, NOT a SpiceDB wire constraint — verified empirically (SPEC-003 A6) that a single `CheckPermissionRequest.Context` is a key-bag where SpiceDB matches keys per-tuple to whichever caveat each tuple needs. Replaced `permCaveat` returning `string` (or error) with `permCaveats` returning `[]string` of unique caveat names. Added `detectPermCaveatCollisions` — errors at codegen if 2+ caveats reachable from one permission share a parameter name (the wire is shared key-bag, so two caveats claiming the same key would silently collide). On the template side, `Check<Perm>Inputs` gains a nested `Caveats Check<Perm>Caveats` sub-struct (one field per unique caveat reachable, named `<CaveatPascal>`), and the `Check<Perm>` body lazily allocates a single map and merges all non-nil `input.Caveats.<X>` entries. Generated method body for a multi-caveat permission:

```go
var caveatCtx map[string]any
if input.Caveats.TenantMatch != nil {
    if caveatCtx == nil { caveatCtx = map[string]any{} }
    caveatCtx["tenant"] = input.Caveats.TenantMatch.Tenant
}
if input.Caveats.WithinWindow != nil {
    if caveatCtx == nil { caveatCtx = map[string]any{} }
    caveatCtx["allowed_actions"]  = input.Caveats.WithinWindow.AllowedActions
    caveatCtx["requested_action"] = input.Caveats.WithinWindow.RequestedAction
}
// ... CheckPermissionWithCaveat(..., caveatCtx)
```

New fixture `multi_check = tenanted_user + windowed_user` reaches both `tenant_match` and `within_window` caveats. 3 tests prove the cap-lift end-to-end: grant via tenanted path with only `Caveats.TenantMatch` supplied; grant via windowed path with only `Caveats.WithinWindow` supplied; deny when neither caveat is bound. Total caveat test count after restructure: 23.

SPEC-003 C4 (per-permission caveat cap "remains in force") and C7 (codegen-wrapper enforcing nil-guard) are no longer accurate for the post-restructure design — both are superseded by the nested Caveats pattern. Inline doc updates to SPEC-003 plus a follow-up SPEC capturing the lifted-cap design as the canonical surface should land before this job's commit.

### [Implementer] Lookup/Read caveat support — analysis-only, deferred

After AUZ-007's Check-side caveat work shipped, audited the parallel `Lookup<Perm><Type>Resources`, `Lookup<Perm><Type>Subjects`, and `Read<Rel><Type>Relations` paths against SpiceDB's wire capabilities. Three gaps surfaced; deferred to a future job by user request.

**Gap A — Lookup drops request-time `Context`.** SpiceDB's `LookupResourcesRequest.Context` and `LookupSubjectsRequest.Context` accept `*structpb.Struct` for caveat eval (same wire shape as `CheckPermissionRequest.Context`), but our `Engine.LookupResources` / `Engine.LookupSubjects` methods don't expose it. For a caveat-reaching permission like `tenanted_browse = tenanted_viewer`, every Lookup result returns `CONDITIONAL_PERMISSION` because the `tenant` key has nothing to bind to. Callers can't unblock by supplying context. Fix shape: add `LookupResourcesWithCaveat` / `LookupSubjectsWithCaveat` engine methods mirroring the Check pattern; route caveat-reaching permissions through them; reuse `Check<Perm>Caveats` sub-struct on the input.

**Gap B — Lookup silently returns CONDITIONAL as HAS.** `pkg/authz/spicedb/crud.go` `LookupResources` / `LookupSubjects` append every `data.ResourceObjectId` to the result slice without inspecting `data.Permissionship`. A `CONDITIONAL_PERMISSION` response (keys missing) gets returned identically to a `HAS_PERMISSION` response — callers think they have access when they don't. This is a silent correctness bug for caveated permissions. Fix shape: either filter (`Permissionship != HAS_PERMISSION` drops the result, matching `errorIfDenied`'s collapse behavior on Check) or surface (return `[]LookupResult{ID, Permissionship, MissingFields []string}` so callers decide). A+B should ship together — A without B leaves the silent bug; B without A doesn't let callers fix the missing-context cause.

**Gap C — Read strips caveat metadata.** `ReadRelationshipsResponse.Relationship.OptionalCaveat` carries `(CaveatName, Context *structpb.Struct)` per tuple — the codegen could surface "this user has viewer access IF tenant=acme" so callers can inspect what conditions are attached. Today we drop everything except the subject ID. Fix shape: add `Read<Rel><Type>RelationsWithCaveat` returning `[]ReadResult[T]{ID, Caveat *<CaveatPascal>Args, CaveatName string}`; keep the existing ID-only method for non-caveated relations. Independent of A+B (Read doesn't take request-time context — pure retrieval).

Recommended: ship A+B as one job (correctness fix + missing input). Defer C separately (richer query feature, not a bug).

### [Implementer] Per-field pointer types lift the partial-binding limitation

The pre-restructure `<CaveatPascal>Args` struct used value-type fields, which bound every parameter at write time when the Args was non-nil — zero values became explicit binds, write-time-precedence locked them in, and check-side overrides got silently ignored. SPEC-003 Overview called this out as a deferred enhancement: "Generate per-field pointer types in `<CaveatPascal>Args` for fine-grained partial binding within a single caveat — value-type fields are sufficient for v1; pointer fields are a future enhancement when a real schema needs per-param defer."

Lifted that here. `caveatTypeToGo` now wraps **scalar** types (`bool`, `string`, `int`, `uint`, `double`) in pointer; **container** types (`[]T`, `[]byte`, `map[string]any`) stay as-is because they're naturally nilable in Go. The asymmetry tracks idiomatic Go (slice/map/bytes use nil for "absent"; scalars need pointer wrap because `var s string = ""` can't distinguish "unset" from "explicit empty"). A new `authz.Ptr[T any](v T) *T` helper — added to `pkg/authz/authz.go` — wraps literal values, since Go forbids `&"acme"`.

Template changes: instead of building the wire context in one map literal when the Args is non-nil, the codegen emits per-field nil-checks and only adds keys whose values were actually supplied. A new `deref` template helper inserts `*` for pointer fields and nothing for container fields. Generated body for a caveated allowed type now looks like:

```go
var caveatCtx map[string]any
if c := objects.Caveats.User; c != nil {
    caveatCtx = map[string]any{}
    if c.AllowedActions != nil {            // []string — nil-check, no deref
        caveatCtx["allowed_actions"] = c.AllowedActions
    }
    if c.RequestedAction != nil {           // *string — nil-check + deref
        caveatCtx["requested_action"] = *c.RequestedAction
    }
}
```

Migration cost: 34 caveat-Args construction sites in tests across all 3 namespaces. Patterns:
- `Tenant: "acme"` → `Tenant: authz.Ptr("acme")` (string field)
- `MinRequired: 0.5` → `MinRequired: authz.Ptr(0.5)` (untyped float defaults to float64, helper inference works)
- `MaxUses: 5` → `MaxUses: authz.Ptr[int64](5)` (untyped int defaults to int, explicit type param needed for int64)
- `MinVersion: 1` → `MinVersion: authz.Ptr[uint64](1)` (same uint64 reason)
- `WithinQuota: true` → `WithinQuota: authz.Ptr(true)` (bool defaults work)
- Slices stay direct: `AllowedActions: []string{"read"}` unchanged
- Bytes stay direct: `Token: []byte("opaque")` unchanged
- Nested lists stay direct: `Rows: [][]string{...}` unchanged

Two new tests prove the partial-binding semantics end-to-end: `TestFolder_Act_PartialBindWithinSingleCaveat_Grants` (write supplies `allowed_actions`, check supplies `requested_action`, merge produces eval-ready context) and `TestFolder_Act_PartialBind_DeniesWhenActionNotPolicyAllowed` (write supplies a restrictive policy, check supplies a request that violates it, eval false). The codegen-driven path now matches SpiceDB's documented per-key union behavior at the FIELD grain — the deepest level of partial binding the schema can express.

### [Implementer] Caveat × operator coverage gap closed

Audit showed only union (`+`) was tested with caveats. Arrow (`->`), intersection (`&`), and exclusion (`-`) had structural AUZ-005 coverage but never with caveats reaching them. The codegen treats all four operators uniformly for caveat collection (per SPEC-001: intersection/exclusion/union walk children identically; arrow collects LeftRel only), but uniform handling is a hypothesis until exercised.

Added three operator × caveat fixtures:

- **Arrow + caveat:** `relation gated_root: extsvc/folder with extsvc/tenant_match` + `permission via_gated_root = gated_root->browse`. Generated `CheckFolderViaGatedRootCaveats` carries only `TenantMatch` (LeftRel caveat), confirming the arrow's right-side caveats correctly stay out of scope.
- **Intersection + caveat:** `permission elite_access = scored_viewer & token_viewer`. Generated `CheckFolderEliteAccessCaveats` carries both `MinScore` + `HasToken` — both legs' caveats reachable, both must satisfy for grant.
- **Exclusion + caveat:** `permission scored_minus_token = scored_viewer - token_viewer`. Generated `CheckFolderScoredMinusTokenCaveats` matches intersection's shape — confirming SPEC-001's "intersection/exclusion treated as union for codegen" extends to caveat collection.

6 new tests cover grant + deny paths for each operator. All pass against live SpiceDB. Total caveat test count: 29.

### [Implementer] `[]byte` requires structpb passthrough, not reflection coercion

The reflection fallback in `coerceStructpbValue` (added to handle nested typed slices like `[][]string`) initially treated `[]byte` as a generic typed slice and converted it to `[]any` of byte values. SpiceDB rejected the wire payload: `could not convert context parameter `token`: for bytes: bytes requires a base64 unicode string, found: []interface {}`. Root cause: SpiceDB CEL's `bytes` type expects a base64-encoded string on the wire — exactly what `structpb.NewValue` natively produces for `[]byte` input. Fix: short-circuit `case []byte` BEFORE the reflection fallback, passing through unchanged so structpb's native encoding kicks in. Lesson: the reflection-based slice fallback must allow-list types that have native structpb handling.

---

## Verification log

- `go build ./...` — clean
- `go vet ./...` — clean
- Round-trip idempotent at new baseline (regen → `git diff --quiet example/authzed/` exits 0)
- `go test ./pkg/authz/spicedb/... ./example/authzed/bookingsvc/... ./example/authzed/menusvc/... ./example/authzed/extsvc/...` — all PASS
- 6 new caveat tests pass against live SpiceDB:
  - `TestFolder_CheckTenantedBrowse_MatchTenant` (ported off bypass; deferred binding)
  - `TestFolder_CheckTenantedBrowse_WrongTenant` (ported off bypass; deferred binding)
  - `TestFolder_CheckTenantedBrowse_NilCaveat` (ported off bypass; deferred binding)
  - `TestFolder_CreateTenantedViewer_PreBound_WriteWins` (NEW — write-time precedence)
  - `TestFolder_CreateGuardedViewer_WildcardDefer` (NEW — wildcard + caveat, defer)
  - `TestFolder_CreateGuardedViewer_WildcardPreBound` (NEW — wildcard + caveat, pre-bind)
