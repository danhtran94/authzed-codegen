# AUZ-008: Lookup with Caveat Context + Conditional Filtering

| Field      | Value                                                |
|------------|------------------------------------------------------|
| Status     | Done                                                 |
| Created    | 2026-05-08                                           |
| Assignee   | danhtran94                                           |
| Source     | jobs/AUZ-007-write-time-caveat-codegen.md            |
| Blocked by | —                                                    |

<!-- approved -->


---

## Goal

Close the Lookup correctness gap from AUZ-007 Discoveries. After this job, `Lookup<Perm><Type>Resources` and `Lookup<Perm><Type>Subjects` for caveat-reaching permissions thread request-time `Context` through to SpiceDB (so callers can supply check-time caveat values), AND filter `LookupPermissionship == CONDITIONAL_PERMISSION` results out of the returned ID slice (matching `Check<Perm>`'s `errorIfDenied` collapse to deny). The bug where Lookup silently includes conditional grants as if they were definite — currently a false-positive class for any caveated permission — goes away.

## Problem

    Current (post-v1.1.0):
      caller → folder.LookupTenantedBrowseUserSubjects(ctx)
                  → engine.LookupSubjects(ctx, ..., subject_type)        ← no Context arg
                      → SpiceDB streams ResolvedSubject{Permissionship}  ← every result, regardless
                          → engine appends every subject_object_id       ← bug: CONDITIONAL pretends to be HAS
                              → caller sees IDs they don't actually have access to ✗

    Symmetric for LookupResources.

The two bugs compound: (i) caller can't supply caveat context to bind missing keys, so SpiceDB returns CONDITIONAL by default, (ii) engine silently surfaces CONDITIONAL results as IDs, so caller's "has permission" lists include false positives.

## Solution: New WithCaveat engine methods + permissionship filter

    After fix:
      caller → folder.LookupTenantedBrowseUserSubjects(ctx, CheckTenantedBrowseCaveats{
                  TenantMatch: &TenantMatchArgs{Tenant: new("acme")},
                })
                  → engine.LookupSubjectsWithCaveat(ctx, ..., caveatParams)
                      → SpiceDB streams ResolvedSubject{Permissionship, PartialCaveatInfo}
                          → engine filters Permissionship != HAS_PERMISSION  ← bug fixed
                              → caller sees only definite grants ✓

The non-caveat `LookupResources` / `LookupSubjects` engine methods get the same filter — for non-caveated permissions the filter is a no-op (no caveat, no CONDITIONAL), but the contract becomes uniform: every result returned has been definitively granted.

### Components

**`Engine.LookupResourcesWithCaveat`** — new interface method
- `(ctx, from Type, match Permission, subject Type, byIDs []ID, caveatParams map[string]any) ([]ID, error)`
- Implementation in `*spicedb.Engine` threads `Context: caveatCtx` to `LookupResourcesRequest` and filters `Permissionship != HAS_PERMISSION` from the streamed responses

**`Engine.LookupSubjectsWithCaveat`** — new interface method
- `(ctx, on Resource, permission Permission, subject Type, caveatParams map[string]any) ([]ID, error)`
- Implementation threads `Context: caveatCtx` to `LookupSubjectsRequest` and filters per `ResolvedSubject.Permissionship`

**Permissionship filter on existing methods** — `Engine.LookupResources` and `Engine.LookupSubjects` gain the same filter. For non-caveat permissions this is a no-op; for safety it ensures the bug can't resurface if a caveated tuple is somehow reached through a non-caveat lookup path.

**Generated `Lookup<Perm><Type>Resources`** — caveat-reaching permissions reuse the existing `CheckXInputs` shape (which already carries `Caveats` per AUZ-007), build the merged map the same way `Check<Perm>` does, and call `LookupResourcesWithCaveat`. Non-caveat permissions stay on the existing `LookupResources` engine method.

**Generated `Lookup<Perm><Type>Subjects`** — caveat-reaching permissions take a new `caveats Check<Perm>Caveats` parameter (function signature changes from `(ctx)` to `(ctx, caveats)`); non-caveat permissions stay at `(ctx)`.

### Why not alternatives

| Approach | Verdict |
|---|---|
| **New `*WithCaveat` engine methods + filter on both old and new** | Mirrors AUZ-006/AUZ-007 pattern; non-breaking for non-caveat callers; closes both gaps in one shape |
| Filter only — leave context threading deferred | Half-fix: callers still can't unblock CONDITIONAL by supplying context. Lookup returns empty for caveated permissions until they switch to a hypothetical-future method. Worse than the current bug for some workflows. |
| Surface CONDITIONAL via richer return type (`[]LookupResult{ID, Permissionship, MissingFields}`) | Better fidelity but requires changing every Lookup return shape. Bigger break, defers conditional handling decisions to every caller. Defer to a future "rich Lookup" job — track in Discoveries if asked for. |
| Take Caveats inside a per-perm `LookupSubjectsInputs` struct (instead of a positional argument) | More uniform with `LookupResources` and `Check<Perm>`, but adds N new generated structs for negligible gain — `Caveats` is the only field. Bare positional argument keeps the surface small. |

## Workstreams

### 1. Runtime interface + spicedb implementation

Adds the two new interface methods, implements them, and applies the permissionship filter to both old and new methods.

| # | Task | File | Status |
|---|------|------|--------|
| 1.1 | Add `LookupResourcesWithCaveat(ctx, from, match, subject, byIDs, caveatParams) ([]ID, error)` to `authz.Engine` | `pkg/authz/authz.go` | [x] |
| 1.2 | Add `LookupSubjectsWithCaveat(ctx, on, permission, subject, caveatParams) ([]ID, error)` to `authz.Engine` | same | [x] |
| 1.3 | Implement `(*Engine).LookupResourcesWithCaveat` — threads `caveatParams` through `LookupResourcesRequest.Context` (via `serializeCaveatMap`) and filters streamed responses on `Permissionship == LOOKUP_PERMISSIONSHIP_HAS_PERMISSION` | `pkg/authz/spicedb/crud.go` | [x] |
| 1.4 | Implement `(*Engine).LookupSubjectsWithCaveat` — threads `caveatParams` through `LookupSubjectsRequest.Context` and filters on `ResolvedSubject.Permissionship` | same | [x] |
| 1.5 | Add the permissionship filter to existing `(*Engine).LookupResources` (no-op for non-caveat permissions; defensive) | same | [x] |
| 1.6 | Add the permissionship filter to existing `(*Engine).LookupSubjects` | same | [x] |

**Key details:** The Engine interface gains 2 methods. WS1+WS2 of AUZ-007 demonstrated the atomic-batch pattern: interface method without impl breaks the `var _ authz.Engine = &Engine{}` assertion. Land 1.1+1.2 with 1.3+1.4 in one batch. Reuses `serializeCaveatMap` (introduced in AUZ-006, hardened in AUZ-007 with reflection coercion).

### 2. Template — codegen routes for caveat-reaching permissions

Generated `Lookup<Perm><Type>Resources` and `Lookup<Perm><Type>Subjects` route through the new WithCaveat methods when the permission reaches ≥1 caveat. Reuses the existing `Check<Perm>Caveats` sub-struct (no new generated types).

| # | Task | File | Status |
|---|------|------|--------|
| 2.1 | In `Lookup<Perm><Type>Resources`, branch on `hasPermCaveats $objectType $perm.Name`. Caveated path emits the same lazy-allocated `caveatCtx` merge body as `Check<Perm>` (reading from `input.Caveats.<CaveatPascal>`), then calls `LookupResourcesWithCaveat(..., caveatCtx)`. Non-caveated path stays on existing `LookupResources` | `internal/templates/object.go.tmpl` | [x] |
| 2.2 | In `Lookup<Perm><Type>Subjects`, branch on `hasPermCaveats`. Caveated path takes a new `caveats Check<Perm>Caveats` positional argument, builds the merged map, and calls `LookupSubjectsWithCaveat(..., caveatCtx)`. Non-caveated path keeps the existing `(ctx)` signature | same | [x] |
| 2.3 | Round-trip the example fixture; confirm caveat-reaching `Lookup<Perm>` methods now emit the WithCaveat engine call and merge body | (verification only) | [x] |

**Key details:** No changes to non-caveat `Lookup<Perm><Type>Resources` / `Lookup<Perm><Type>Subjects` — those continue to call `LookupResources` / `LookupSubjects` (which themselves gain the filter from WS1.5/1.6). The function-signature change for `Lookup<Perm><Type>Subjects` on caveated permissions IS a breaking change for any caller of `LookupTenantedBrowseUserSubjects(ctx)` — they must add the `caveats` argument. Documented in CHANGELOG when shipping.

### 3. Regenerate example fixture

Run codegen against `example/schema.zed` (which already has the AUZ-006/AUZ-007 caveat fixtures). Round-trip idempotent at the new baseline.

| # | Task | File | Status |
|---|------|------|--------|
| 3.1 | Run codegen; inspect `example/authzed/extsvc/folder.gen.go` diff to confirm `LookupTenantedBrowseFolderResources` reads `input.Caveats.TenantMatch` and routes through `LookupResourcesWithCaveat` | `example/authzed/extsvc/folder.gen.go` | [x] |
| 3.2 | Same inspection for `LookupTenantedBrowseUserSubjects` (signature change to take `caveats CheckFolderTenantedBrowseCaveats`) | same | [x] |
| 3.3 | `go build ./...` + `go vet ./...` clean against regenerated output | (verification only) | [x] |
| 3.4 | Run codegen a second time; `git diff --quiet example/authzed/` exits 0 | (verification only) | [x] |

### 4. Tests

E2E coverage for the new paths against live SpiceDB. Each test names a clear scenario.

| # | Task | File | Status |
|---|------|------|--------|
| 4.1 | `TestFolder_LookupTenantedBrowseUserSubjects_GrantedWithCaveat` — write a caveated tuple (defer pattern), call `LookupTenantedBrowseUserSubjects(ctx, Caveats{TenantMatch: ...})` with matching tenant, assert the user is in the returned slice | `example/authzed/extsvc/extsvc_test.go` | [x] |
| 4.2 | `TestFolder_LookupTenantedBrowseFolderResources_GrantedWithCaveat` — write caveated tuple, call `LookupTenantedBrowseFolderResources(ctx, CheckFolderTenantedBrowseInputs{User: ..., Caveats: ...})`, assert the folder is in the returned slice | same | [x] |
| 4.3 | `TestFolder_LookupTenantedBrowse_ConditionalFiltered` — write caveated tuple (defer), call Lookup with NO caveats supplied, assert the result slice is empty (CONDITIONAL filtered out — closes the silent-bug class) | same | [x] |
| 4.4 | `TestFolder_LookupTenantedBrowse_WrongCaveatFiltered` — write tuple bound at write time with `tenant=acme`, call Lookup with mismatched check-time `tenant=other`, assert empty result (write-time wins on collision; eval false; filtered) | same | [x] |
| 4.5 | Run `go test ./pkg/authz/spicedb/... ./example/authzed/...` — all pass (Docker required) | (verification only) | [x] |

### 5. Documentation + release prep

Update CHANGELOG / README to flag the breaking signature change for `Lookup<Perm><Type>Subjects` on caveated permissions, and bump version.

| # | Task | File | Status |
|---|------|------|--------|
| 5.1 | Add `[1.2.0]` entry to CHANGELOG.md naming the new methods, the filter fix, and the breaking signature change for caveated `Lookup<Perm><Type>Subjects` | `CHANGELOG.md` | [x] |
| 5.2 | Update README's Caveats section to mention Lookup-with-caveat and the conditional filter | `README.md` | [x] |
| 5.3 | Tag `v1.2.0` after merge; create GitHub release with notes | (verification only) | [x] |

## Design Decisions

### Filter at engine layer, not at codegen wrapper

`pkg/authz/spicedb/crud.go` filters `Permissionship != HAS_PERMISSION` directly inside `LookupResources` / `LookupResourcesWithCaveat` / `LookupSubjects` / `LookupSubjectsWithCaveat`. The codegen template stays unchanged for the response-handling path (still calls the engine and returns whatever IDs come back). Reasoning: the filter is a SpiceDB wire-truth, not a codegen choice — anywhere the engine touches Lookup output, the contract should be "only definite grants." Surfacing CONDITIONAL distinctly is a future job (would need a richer return type), tracked in this job's Discoveries if asked for.

### Reuse `Check<Perm>Caveats` sub-struct, no new generated types

`Lookup<Perm><Type>Resources` already takes `CheckXInputs` as input — that struct's `Caveats` field is exactly what we need for context. `Lookup<Perm><Type>Subjects` takes a positional `caveats Check<Perm>Caveats` argument rather than wrapping in a new `LookupSubjectsInputs` struct, because the Caveats field would be the only member of such a wrapper. Caller ergonomics > minor uniformity.

### Breaking signature change is documented, not avoided

Caveated `Lookup<Perm><Type>Subjects` changes from `(ctx)` to `(ctx, caveats)`. Could preserve the old signature by adding a sibling `LookupXSubjectsWithCaveat`, but: (i) the old signature is silently buggy for caveated permissions today, (ii) leaving both means callers can pick the buggy one, (iii) the AUZ-006/AUZ-007 precedent was "caveated paths get caveat input." Same precedent here. Bump to v1.2.0 in CHANGELOG.

## What Stays Unchanged

- `Check<Perm>` and `Check<Perm>Inputs` shape — already correct as of AUZ-007
- `Create<Rel>Relations` / `Delete<Rel>Relations` — unaffected
- `Read<Rel><Type>Relations` — Gap C (caveat metadata stripped on Read) stays deferred per AUZ-007 Discoveries
- `Lookup<Perm><Type>Resources` / `Lookup<Perm><Type>Subjects` for **non-caveat** permissions — function signatures unchanged
- `CONDITIONAL_PERMISSION` surfacing on the Check path (`errorIfDenied` still collapses to `ErrPermissionDenied`) — separate future job
- `MissingRequiredContext` from `PartialCaveatInfo` — still dropped (would be exposed by the same future "rich signal" job)

## Implementation Order

    1. WS1 (interface + spicedb impl + filter)  ← single atomic batch (interface method without impl breaks build)
    2. WS2 (template)                            ← independent of WS1; regen requires WS1
    3. WS3 (regenerate fixture)                  ← validates WS1+WS2 produce compilable output
    4. WS4 (tests)                               ← end-to-end proof against live SpiceDB
    5. WS5 (docs + release)                      ← CHANGELOG + version bump after green

WS1.1+1.2 (interface) and WS1.3+1.4 (impl) must land together — `var _ authz.Engine = &spicedb.Engine{}` assertion in `crud.go`. Same atomic-batch pattern as AUZ-007 WS1+WS2.

## Notes

- `LookupResourcesRequest.Context` and `LookupSubjectsRequest.Context` are both `*structpb.Struct` — exact same wire shape as `CheckPermissionRequest.Context`. The engine uses `serializeCaveatMap` for all three (already extended in AUZ-007 to handle nested typed slices and `[]byte` correctly).
- `LookupPermissionship` enum values: `LOOKUP_PERMISSIONSHIP_UNSPECIFIED` (0), `LOOKUP_PERMISSIONSHIP_HAS_PERMISSION` (1), `LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION` (2). Filter is `== HAS_PERMISSION`.
- The streaming response loop in `LookupResources` is paginated (uses `OptionalCursor`); we don't paginate today (loop until EOF). Filter applies per-result inside the loop — no change to pagination behavior.
- Read-with-caveat-metadata (Gap C) stays explicitly deferred. Documented in this job's "What Stays Unchanged" so the next reader knows where it sits.

## Discoveries & Decisions During Implementation

### [Implementer] Existing test sites untouched by the `Lookup<Perm><Type>Subjects` signature change

Pre-emptive concern was that the breaking signature change for caveated `Lookup<Perm><Type>Subjects` (from `(ctx)` to `(ctx, caveats)`) would cascade through existing tests. Audited via grep — zero call sites in any of `example/authzed/{extsvc,bookingsvc,menusvc}/*_test.go` touch the caveated Lookup methods. No migration needed; only the 4 new e2e tests in WS4 exercise these paths.

### [Implementer] Non-caveat `.gen.go` files re-emit with formatting-only diffs

WS3 regen produced diffs in 9 non-caveat `.gen.go` files (e.g. `menusvc/setting.gen.go`, `bookingsvc/employee.gen.go`). Inspection confirmed these are pure formatting changes — a stray blank line gained at function start, trailing-whitespace removed on one line. The template's new `{{ if hasPermCaveats }}{{ else }}` branching for the Lookup methods produced equivalent output for the non-caveat else-branch, but template directives at line boundaries shift surrounding whitespace. Functionally identical; round-trip stays idempotent at the new baseline. No correctness concern.

### [Implementer] Filter applied uniformly closes a latent path

`Engine.LookupResources` and `Engine.LookupSubjects` (the original non-caveat methods) gained the same `Permissionship == HAS_PERMISSION` filter. For caveat-free permissions this is a no-op — SpiceDB never returns CONDITIONAL when no caveat is reachable. But applying it uniformly means the generated codegen (which routes non-caveat permissions through these methods) can't accidentally surface CONDITIONAL results if a future schema change makes a previously-non-caveated permission reach a caveat. Defensive; the filter cost is one branch per streamed response.
