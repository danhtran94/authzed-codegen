# AUZ-009: Expiration Codegen

| Field      | Value                                       |
|------------|---------------------------------------------|
| Status     | Done                                        |
| Created    | 2026-05-09                                  |
| Assignee   | danhtran94                                  |
| Source     | docs/spec-004-expiration-codegen.md         |
| Blocked by | —                                           |

<!-- approved -->


---

## Goal

Lift the `with expiration` rejection from the codegen and surface per-tuple expiration timestamps as typed fields on `<Rel>Objects.Expirations` (mirroring the `Caveats` and `Wildcards` patterns). After this job, schemas declaring `relation X: T with expiration` (and combined `with cav and expiration`) compile and produce generated code that writes via `OPERATION_TOUCH` with `OptionalExpiresAt` set; SpiceDB filters expired tuples server-side, so Check / Lookup / Read paths require no client-side change. Verified end-to-end with a `with expiration` fixture, a combined `with cav and expiration` fixture, and a "wait for expiry" runtime test using a short TTL.

## Problem

    Current (post-v1.2.0):
      caller declares `relation token: user with expiration` in schema
        → adapter `flattenAllowedTypes` errors: "expiration traits are not supported"
          → codegen exits before any output is written ✗
      caller wants per-tuple TTL but has to bypass codegen and call authzed-go directly

The trait has been a known reject for the lifetime of the codegen (ADR-001 rejection list). SpiceDB has supported expiration since v1.40 and our runtime SpiceDB image already accepts it; the gap is purely in the adapter + template + engine layers.

## Solution: Per-allowed-type `Expirations` sub-struct + auto-TOUCH

    After fix:
      caller declares: relation token: user with expiration
        → adapter accepts, sets AllowedType.IsExpiring = true
          → template emits Expirations <RelName>Expirations sub-struct on <Rel>Objects
            → generated Create<Rel>Relations routes expiring branches through:
                engine.CreateRelationsWithExpiration(..., expiresAt time.Time)
                  → wire: RelationshipUpdate{Operation: OPERATION_TOUCH,
                                              Relationship.OptionalExpiresAt: <ts>}
                    → SpiceDB stores per-tuple TTL ✓

For combined `with cav and expiration`, the same engine method takes both `caveatName + caveatParams + expiresAt`. The four routing combinations (no-trait / caveat / expiration / both) map to three engine methods (the existing two + the new one). SPEC-004 Interface Contracts has the full table.

### Components

**`Engine.CreateRelationsWithExpiration`** — single new interface method covering expiration-only AND caveat+expiration cases via parameter sentinels (`caveatName == ""` and `caveatParams == nil` mean "expiration only"). Always issues `OPERATION_TOUCH`.

**`AllowedType.IsExpiring bool`** — new adapter-level field, set when `RequiredExpiration != nil` on the proto. Drives template routing.

**`<RelName>Expirations` sub-struct** — generated when `anyExpiring $rel.AllowedTypes` is true. One `<IDFieldName> *time.Time` field per expiring allowed type. Reuses the AUZ-007 disambiguation (`IDFieldName` = `<TypeName>` or `<TypeName><CaveatPascal>` on collision).

**`anyExpiring` template helper** — mirrors `anyCaveat` and `anyWildcard`. One-line FuncMap addition.

### Why not alternatives

| Approach | Verdict |
|---|---|
| **Single new engine method `CreateRelationsWithExpiration` covering both pure-expiration and caveat+expiration** | Fewer methods; sentinel params for unused dimensions are clear in context. Picked. |
| Separate `CreateRelationsWithExpiration` + `CreateRelationsWithCaveatAndExpiration` (4 methods total in the family) | Symmetric naming but multiplies surface; the combined case is just a special parameterization of the same wire shape. |
| Always TOUCH (replace existing CREATE everywhere) | Changes the existing duplicate-error semantics on non-expiring relations. Bigger blast radius for negligible benefit. |
| Combined `Constraints` sub-struct holding both Caveats and Expirations | Conflates two things that compose orthogonally; rejected per SPEC-004 C3. |
| Separate `Touch<Rel>Relations` method exposed to callers | Leaks operation choice into the API surface; auto-switch keeps the surface uniform. Picked auto-switch per SPEC-004 C1. |

## Workstreams

### 1. Adapter — accept `with expiration` + new `AllowedType` field

Lift the rejection in `flattenAllowedTypes`; capture the trait into the adapter's domain model. Independent of the engine + template work.

| # | Task | File | Status |
|---|------|------|--------|
| 1.1 | Add `IsExpiring bool` field to `AllowedType` struct | `internal/generator/adapter.go` | [x] |
| 1.2 | In `flattenAllowedTypes`, replace the `expiration traits are not supported` error return with `isExpiring := ar.GetRequiredExpiration() != nil`; populate the new field | same | [x] |
| 1.3 | Confirm the `IDFieldName` disambiguation post-processing already handles same-namespace expiring entries (no new code path needed per A6) | (verification only) | [x] |
| 1.4 | Build verification: `go build ./...` + `go vet ./...` clean (the new field is unused until WS3) | (verification only) | [x] |

**Key details:** Field-add only. No template references yet, so no end-to-end behavior change. Round-trip stays byte-identical against the unchanged example schema.

### 2. Engine interface + spicedb implementation (atomic batch)

Add the new interface method and implement it. Must land together — `var _ authz.Engine = &spicedb.Engine{}` assertion would break otherwise. Same pattern as AUZ-006 Tasks 4+5 and AUZ-007 WS1+WS2.

| # | Task | File | Status |
|---|------|------|--------|
| 2.1 | Add `CreateRelationsWithExpiration(ctx, to, relation, subject, ids, caveatName string, caveatParams map[string]any, expiresAt time.Time) error` to `authz.Engine` | `pkg/authz/authz.go` | [x] |
| 2.2 | Implement `(*Engine).CreateRelationsWithExpiration` — builds `RelationshipUpdate{Operation: OPERATION_TOUCH, Relationship{...OptionalCaveat, OptionalExpiresAt: timestamppb.New(expiresAt)}}`. Reuses `serializeCaveatMap` (only when caveatName != ""). Calls `e.setToken(res.WrittenAt.Token)` | `pkg/authz/spicedb/crud.go` | [x] |
| 2.3 | Add `time` and `google.golang.org/protobuf/types/known/timestamppb` imports if not already present | same | [x] |

**Key details:** Hard-coded `OPERATION_TOUCH` per SPEC-004 C1. The method covers BOTH expiration-only and caveat+expiration via parameter sentinels (`caveatName == ""` skips the OptionalCaveat branch). Build between 2.1 and 2.2 fails — that's expected and resolves at 2.2.

### 3. Template — `Expirations` sub-struct + per-type routing in Create methods

Emit the new sub-struct gated on `anyExpiring`; extend the per-allowed-type routing in `Create<Rel>Relations` (regular + wildcard branches) to pick among four engine methods based on caveat × expiration flags.

| # | Task | File | Status |
|---|------|------|--------|
| 3.1 | Add `anyExpiring` template helper to `Generator.GenerateObjectSource`'s `FuncMap` (mirrors `anyCaveat` / `anyWildcard`) | `internal/generator/generator.go` | [x] |
| 3.2 | In `<Rel>Objects` struct emission, append `Expirations <RelName>Expirations` field guarded on `anyExpiring`, after the existing `Caveats` field | `internal/templates/object.go.tmpl` | [x] |
| 3.3 | After the existing `<RelName>Caveats` sub-struct emission, emit `<RelName>Expirations` sub-struct guarded on `anyExpiring`. Per allowed type with `IsExpiring`, emit one `<IDFieldName> *time.Time` field | same | [x] |
| 3.4 | In `Create<Rel>Relations` regular branch, add per-allowed-type routing: when `$relType.IsExpiring`, build `expiresAt` from `objects.Expirations.<IDFieldName>` (zero `time.Time{}` when nil) and call `CreateRelationsWithExpiration`. The existing caveat / non-caveat branches stay unchanged for non-expiring types | same | [x] |
| 3.5 | Mirror the per-type routing in the wildcard sub-block (`if objects.Wildcards.<IDFieldName>`). Same `Expirations.<IDFieldName>` field is consumed by both regular and wildcard branches when allowed type is both | same | [x] |
| 3.6 | Add `"time"` to the generated file's import set (use a template helper similar to `"context"` gating: only emit when `anyExpiring` is true on any relation in the definition) | same | [x] |

**Key details:** All new branches guarded on `anyExpiring` / `$relType.IsExpiring` so expiration-free schemas regenerate byte-identically (per SPEC-004 C8). The 4-way routing decision (none / caveat / expiration / both) lives entirely in the per-allowed-type loop body — no other template structure changes.

### 4. Regenerate example fixture + verify build clean

Add a fixture exercising `with expiration` and `with cav and expiration`; regenerate and verify idempotency.

| # | Task | File | Status |
|---|------|------|--------|
| 4.1 | Add `use expiration` directive at the top of `example/schema.zed` | `example/schema.zed` | [x] |
| 4.2 | Add fixture `relation expiring_viewer: extsvc/user with expiration` + `permission expiring_browse = expiring_viewer` to `extsvc/folder` | same | [x] |
| 4.3 | Add combined fixture `relation gated_token: extsvc/user with extsvc/tenant_match and expiration` + `permission gated_token_check = gated_token` (combines existing `tenant_match` caveat with expiration) | same | [x] |
| 4.4 | Run codegen; inspect new emission in `example/authzed/extsvc/folder.gen.go` for `FolderExpiringViewerExpirations` and `FolderGatedTokenExpirations` sub-structs and the `CreateRelationsWithExpiration` call paths | `example/authzed/extsvc/folder.gen.go` | [x] |
| 4.5 | `go build ./...` + `go vet ./...` clean against regenerated output | (verification only) | [x] |
| 4.6 | Run codegen a second time; `git diff --quiet example/authzed/` exits 0 (idempotent at new baseline) | (verification only) | [x] |

### 5. Tests

End-to-end against live SpiceDB. Each test names a clear behavior gate.

| # | Task | File | Status |
|---|------|------|--------|
| 5.1 | `TestFolder_ExpiringBrowse_GrantsBeforeExpiry` — write tuple with expiration 1h in future; immediately call `CheckExpiringBrowse` and `LookupExpiringBrowse*`; assert grant. Verifies the expiration-only path | `example/authzed/extsvc/extsvc_test.go` | [x] |
| 5.2 | `TestFolder_ExpiringBrowse_DeniesAfterExpiry` — write tuple with expiration 100ms in future; sleep 200ms; call `CheckExpiringBrowse`; assert `ErrPermissionDenied` (server-side filter; no client awareness needed) | same | [x] |
| 5.3 | `TestFolder_GatedToken_GrantsWhenCaveatAndExpiryHold` — write combined caveat+expiration with `Tenant: "acme"` and 1h expiration; check with matching tenant; assert grant. Verifies both gates pass together | same | [x] |
| 5.4 | `TestFolder_GatedToken_DeniesWhenCaveatFailsEvenIfNotExpired` — write tuple with `Tenant: "acme"` + 1h expiration; check with mismatched tenant; assert `ErrPermissionDenied` (caveat fails, expiration irrelevant) | same | [x] |
| 5.5 | `TestFolder_ExpiringBrowse_TouchAllowsRewriteAfterExpiry` — write tuple with 100ms TTL; sleep 200ms; write again with longer TTL via the same `CreateExpiringViewerRelations` call; check; assert grant. Verifies that `OPERATION_TOUCH` allows over-write of expired-but-not-GC'd tuples (per A2). | same | [x] |
| 5.6 | Run `go test ./pkg/authz/spicedb/... ./example/authzed/...` — all pass (Docker required) | (verification only) | [x] |

**Key details:** Tests 5.2 and 5.5 use real `time.Sleep` calls (≤200ms each); aggregate test runtime stays ≤1s extra. The in-memory SpiceDB datastore filters by expiration at evaluation time without waiting for GC, so the "after expiry" assertion fires immediately once `now() > expires_at`. Production datastores with GC delay (per A5) behave identically for the filter but reclaim storage on a longer schedule — not directly testable here.

### 6. Documentation + release prep

CHANGELOG entry, README update, version bump.

| # | Task | File | Status |
|---|------|------|--------|
| 6.1 | Add `[1.3.0]` entry to `CHANGELOG.md` documenting `with expiration` support, the new `Engine.CreateRelationsWithExpiration` interface method, and the auto-TOUCH semantics for expiring writes | `CHANGELOG.md` | [x] |
| 6.2 | Update README's Schema Support table — flip `Expiration (with expiration)` from ✗ to ✓ with a description; add a small `Expirations` example after the Caveats section | `README.md` | [x] |
| 6.3 | Tag `v1.3.0` after merge; create GitHub release with notes | (verification only) | [x] |

## Design Decisions

### One engine method covering expiration-only + caveat+expiration

`CreateRelationsWithExpiration(ctx, ..., caveatName, caveatParams, expiresAt)` covers both shapes via parameter sentinels (`caveatName == ""` skips the OptionalCaveat construction). Alternative — four methods (Create / WithCaveat / WithExpiration / WithCaveatAndExpiration) — was rejected: the wire shape differs only in which `Optional*` fields are populated, so multiplying methods adds API surface without functional distinction. Documented in SPEC-004 Interface Contracts.

### Auto-switch to TOUCH instead of exposing operation choice

Per SPEC-004 C1, expiring writes use `OPERATION_TOUCH` because un-GC'd expired tuples may collide on tuple identity. The codegen routes expiring branches through the new method (which hard-codes TOUCH) automatically — caller doesn't pick the operation. Keeps the surface uniform with `Create<Rel>Relations` for non-expiring relations.

### Separate `Caveats` and `Expirations` sub-structs

Each does one thing. A relation with `with cav and expiration` populates both independently. Combining into a `Constraints` struct was considered and rejected — the two traits compose orthogonally and conflating them obscures which dimension a value belongs to.

### `expiresAt time.Time` (value, not pointer) on the engine method

Engine method only fires when the relation has expiration, so a value is always required. The pointer (`*time.Time`) lives at the typed-struct surface (`Expirations.<IDFieldName>`) for nullability — caller can omit the field, codegen passes the zero `time.Time{}` to the engine, SpiceDB stores `0001-01-01` which is immediately past, tuple is immediately filtered. Caller-error catch surfaces at runtime, same trade-off as AUZ-007's permissive-nil semantics on caveat fields.

## What Stays Unchanged

- `CheckPermission` / `Check<Perm>` / `CheckPermissionWithCaveat` — server-side filter handles expiration transparently (per SPEC-004 A4)
- `LookupResources` / `LookupResourcesWithCaveat` and `LookupSubjects` / `LookupSubjectsWithCaveat` — same server-side filter
- `ReadRelations` / `Read<Rel><Type>Relations` — caveat metadata stripping (AUZ-007 Gap C) carries forward to expiration metadata stripping; surfacing `OptionalExpiresAt` on read responses is deferred per SPEC-004 C10
- `DeleteRelations` / `Delete<Rel>Relations` — DELETE matches by 6-column tuple identity (AUZ-007 SPEC-003 A3); expiration metadata is irrelevant to the match. Callers can delete an expiring tuple before its natural expiration via the existing `Delete<Rel>Relations` method.
- `CONDITIONAL_PERMISSION` filter on Lookup paths (AUZ-008) — applies uniformly to expiring permissions too; expiration filtering happens at SpiceDB before the response stream, so the existing filter is a no-op for expiry-induced denials but stays in place for caveat-induced CONDITIONAL.

## Implementation Order

    1. WS1 (adapter)                              ← independent; lifts rejection, adds field
    2. WS2 (engine interface + impl atomic batch) ← single build-check
    3. WS3 (template)                             ← uses WS1's field + WS2's method
    4. WS4 (regenerate fixture)                   ← validates WS1+WS2+WS3 produce compilable output
    5. WS5 (e2e tests)                            ← end-to-end proof against live SpiceDB
    6. WS6 (docs + release)                       ← CHANGELOG + version bump after green

WS2 must be atomic (interface method without impl breaks `var _ authz.Engine = &spicedb.Engine{}` assertion). WS1 is independent and can land alone — the new field is unused until WS3 references it. WS3 references both WS1's `IsExpiring` and WS2's new engine method, so it must follow both.

## Notes

- `OptionalExpiresAt` is `*timestamppb.Timestamp` on the wire. Standard constructor: `timestamppb.New(time.Time)`. No new direct dependency — `timestamppb` is already transitively imported via `authzed-go`.
- `serializeCaveatMap` is reused unchanged in the combined caveat+expiration path. The runtime-layer reflection coercion shipped in AUZ-007 covers all caveat parameter types; expiration adds no new value-shape concerns.
- The example schema's `tenant_match` caveat is reused for the combined fixture (`gated_token`). No new caveat declarations needed.
- Tests 5.2 and 5.5 use ≤200ms `time.Sleep` calls — the in-memory datastore filters at evaluation time, no GC delay observed.
- `harness validate-pr-checklist` will hard-block a push with `Status=Done` while any task row is `[ ]` (per the AUZ-007 chore-commit experience). Flip checkboxes as work progresses, not in a final sweep.

## Discoveries & Decisions During Implementation

### [Implementer] Trim markers needed on conditional `time` import

WS3 first-pass round-trip failed — the conditional `{{ if anyExpiringInRels .Relations }}"time"{{ end }}` template directive emitted a trailing whitespace line in non-expiring `.gen.go` files (the gated-empty branch left whitespace from surrounding indentation). Fix: collapse the directive onto the same line as the preceding `"context"` directive so when expiration is absent, no extra line slips in. Round-trip went byte-identical after.

### [Implementer] Test 4 mis-applied write-time-precedence

`TestFolder_GatedToken_DeniesWhenCaveatFailsEvenIfNotExpired` initially wrote `Tenant: "acme"` at write-time and checked with `Tenant: "not-acme"` expecting deny. Per SPEC-003 A6 (write-time wins on collision), the eval was `"acme" == "acme"` → grant — the check-time value was overridden. Fixed by deferring the caveat at write (empty `Caveats` sub-struct) so the check-time `"not-acme"` value reaches eval and produces the expected deny. Pattern matches the AUZ-006 `TestFolder_CheckTenantedBrowse_WrongTenant` test that exercises the same defer-then-mismatch.

### [Implementer] Wildcard + expiration verification gap (closed in patch)

After v1.3.0 shipped, a review surfaced that AUZ-009's e2e coverage tested only concrete-subject expiration (`extsvc/user with expiration`), not the wildcard combinations. AUZ-007 had verified wildcard + caveat (`guarded_viewer: extsvc/user:* with extsvc/tenant_match`), but no fixture covered `type:* with expiration` or the triple `type:* with caveat and expiration`. Mechanically the codegen should handle these — `IsExpiring` and `IsWildcard` are independent flags on `AllowedType`, and the template's three sub-structs (`Wildcards`, `Caveats`, `Expirations`) are generated from the same set — but "should" is not "verified". Closed by adding two relations (`public_until`, `public_gated`) to `example/schema.zed` and 4 e2e tests covering the realistic "public for users but in time" use case (wildcard grant within TTL, denied after expiry, plus the triple with caveat eval). All 4 pass first-try; no codegen changes were needed. No version bump because the codegen behavior was already correct — only verification depth grew.

### [Implementer] No need for fresh disambiguation logic

SPEC-004 A6 hypothesized that the `IDFieldName` disambiguation post-processing in `flattenAllowedTypes` (introduced for caveat collisions) would extend to expiration without new code. Confirmed during WS1 — the existing post-processing already runs for every `(Namespace, IsWildcard)` group, so an expiring allowed type that collides with another caveated allowed type of the same namespace gets the same `<TypeName><CaveatPascal>` treatment automatically. No expiration-specific disambiguation logic was added.
