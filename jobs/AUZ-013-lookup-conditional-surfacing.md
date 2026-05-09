# AUZ-013: Lookup Conditional Surfacing

| Field      | Value                                            |
|------------|--------------------------------------------------|
| Status     | Done                                              |
| Created    | 2026-05-09                                       |
| Assignee   | danhtran94                                       |
| Source     | docs/spec-008-lookup-conditional-surfacing.md    |
| Blocked by | —                                                |

<!-- approved -->

---

## Goal

Close the symmetric gap to v1.6's Check rich-signal: today `LookupResources` / `LookupSubjects` silently filter `LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION` rows from results, dropping `PartialCaveatInfo.MissingRequiredContext`. After this job, all Lookup paths return a typed `LookupResult` partitioning definite grants from conditional grants; conditional entries carry `MissingKeys` so callers can fetch context and retry. After AUZ-013 ships, Check and Lookup paths give consistent semantics for caveat-reaching schemas — both surface the recoverable-conditional case distinctly from definite grants and from hard denies.

## Problem

    Current (post-v1.6.0):
      caller has a caveat-reaching permission, doesn't supply context:
        LookupTenantedBrowseFolderResources(ctx, input)  // input.Caveats empty
          → engine.LookupResourcesWithCaveat → SpiceDB stream returns rows
              some HAS_PERMISSION, some CONDITIONAL_PERMISSION
            → loop filter: skip Permissionship != HAS_PERMISSION ✗
              caller gets only definite IDs
              ✗ cannot tell "found nothing" from "found conditional, supply context"
              ✗ cannot recover by fetching the missing keys

The collapse is documented as deferred in CHANGELOG entries v1.2.0 (AUZ-008) and v1.6.0 (AUZ-012). v1.6 closed the Check side; AUZ-013 closes the Lookup side.

## Solution: Typed `LookupResult` partitioning definite from conditional

    After fix:
      caller code:
        result, err := folder.LookupTenantedBrowseFolderResources(ctx, input)
          → engine.LookupResourcesWithCaveat returns LookupResult{
              Definite: [definitive grants],
              Conditional: [{ID, MissingKeys=["tenant"]}, ...],
            }
        for _, c := range result.Conditional {
          fetched := fetch(c.MissingKeys)
          retryCheckWithContext(ctx, c.ID, fetched)
        }

Variant-C philosophy from AUZ-010 SPEC-005: uniform replacement across all 4 Lookup paths (Resources, Subjects, *WithCaveat). Schema gaining a caveat later doesn't change method signatures — only what populates in the existing `Conditional` slice.

### Components

**`authz.LookupResult`** — runtime result struct on the Engine surface; `Definite []ID` + `Conditional []LookupConditionalEntry`.

**`authz.LookupConditionalEntry`** — runtime conditional row; `ID + MissingKeys []string`.

**Generated `<Type>LookupResult`** — typed counterpart per resource/subject type; `Definite []<Type>` + `Conditional []<Type>ConditionalLookupEntry`.

**Generated `<Type>ConditionalLookupEntry`** — typed conditional row.

**`*spicedb.Engine.LookupResourcesWithCaveat` / `LookupSubjectsWithCaveat`** — switch on `Permissionship`, populate `Definite` from HAS_PERMISSION rows, `Conditional` from CONDITIONAL_PERMISSION rows + `PartialCaveatInfo.MissingRequiredContext`.

**`*spicedb.Engine.HasPublicSubject`** — body-only update reading `result.Definite` instead of `[]ID`; external `(bool, error)` signature preserved.

### Why not alternatives

| Approach | Verdict |
|---|---|
| **Typed result struct, uniform across all 4 Lookup paths** (chosen) | Variant-C philosophy from AUZ-010 SPEC-005. Single return value (idiomatic Go), safe-by-default (`Definite` as the surface; `Conditional` opt-in), self-documenting via field names, future-extensible. |
| 3-tuple `([]ID, []ConditionalEntry, error)` | Rejected. Awkward Go (three return values), harder to extend (adding fields breaks every caller), no semantic disambiguation between the two slices except by position. |
| Conditional-as-error wrapping | Rejected. Mixes partial-success + typed-error semantics; awkward when err != nil typically means don't trust the result. |
| Parallel methods (e.g. `LookupResourcesWithConditional`) | Rejected. Doubles the surface; "which one do I call?" question. Variant-A philosophy already rejected in AUZ-010. |
| Selective emission (only caveat-reaching permissions) | Rejected. Schema evolution surprise — adding a caveat later changes return shape. Variant-B philosophy already rejected in AUZ-010. |

## Workstreams

### 1. Runtime types

Add `LookupResult` + `LookupConditionalEntry` in `pkg/authz/`. Foundation for the engine impl change.

| #   | Task | File | Status |
|-----|------|------|--------|
| 1.1 | Add `LookupResult` struct with `Definite []ID` and `Conditional []LookupConditionalEntry` fields | `pkg/authz/authz.go` | [x] |
| 1.2 | Add `LookupConditionalEntry` struct with `ID` and `MissingKeys []string` fields | same | [x] |
| 1.3 | Change `Engine.LookupResources` interface signature to `(LookupResult, error)` | same | [x] |
| 1.4 | Change `Engine.LookupResourcesWithCaveat` interface signature to `(LookupResult, error)` | same | [x] |
| 1.5 | Change `Engine.LookupSubjects` interface signature to `(LookupResult, error)` | same | [x] |
| 1.6 | Change `Engine.LookupSubjectsWithCaveat` interface signature to `(LookupResult, error)` | same | [x] |

**Key details:** Per SPEC-008 C1 — the engine impl initialises both slices to empty (`[]ID{}`, `[]LookupConditionalEntry{}`) so callers can range without nil-checks. Per C2 — Conditional entries are NOT included in Definite (safe-by-default access).

### 2. Engine impl — populate Conditional from wire response

Update the four Lookup impls + `HasPublicSubject` body. Atomic batch — interface change and impl must land together.

| #   | Task | File | Status |
|-----|------|------|--------|
| 2.1 | `LookupResourcesWithCaveat` switch on `data.Permissionship`: HAS_PERMISSION → append to `Definite`; CONDITIONAL_PERMISSION → append `LookupConditionalEntry{ID, MissingKeys: data.PartialCaveatInfo.MissingRequiredContext}` to `Conditional`; UNSPECIFIED dropped | `pkg/authz/spicedb/crud.go` | [x] |
| 2.2 | `LookupResources` (non-caveat passthrough) — adapt return type | same | [x] |
| 2.3 | `LookupSubjectsWithCaveat` mirror — read from `data.Subject.Permissionship` and `data.Subject.PartialCaveatInfo`; append to result fields | same | [x] |
| 2.4 | `LookupSubjects` (non-caveat passthrough) — adapt return type | same | [x] |
| 2.5 | `HasPublicSubject` body update — replace `slices.Contains(ids, WildcardID)` with `for _, id := range result.Definite { if id == WildcardID }` | same | [x] |

**Key details:** Per SPEC-008 A1/A3 — Subjects path reads `Subject.Permissionship` (not deprecated top-level field). Per A2 — UNSPECIFIED rows are dropped silently. Per A5 — error path returns zero-value `LookupResult{}` (matches existing nil-slice-on-error contract).

### 3. Codegen — typed result + conditional entry per type

Emit the typed result struct and conditional entry once per resource/subject type. Update generated `Lookup*` method bodies to project to typed.

| #   | Task | File | Status |
|-----|------|------|--------|
| 3.1 | Emit `<Type>LookupResult` struct (per object type once) with `Definite []<Type>` and `Conditional []<Type>ConditionalLookupEntry` fields | `internal/templates/object.go.tmpl` | [x] |
| 3.2 | Emit `<Type>ConditionalLookupEntry` struct (per object type once) with `ID <Type>` and `MissingKeys []string` fields | same | [x] |
| 3.3 | Update `Lookup<Perm><Type>Resources` body — project engine `LookupResult` to typed `<Type>LookupResult` | same | [x] |
| 3.4 | Update `Lookup<Perm><Type>Subjects` body — project to typed; preserve userset Check input routing from AUZ-011 | same | [x] |
| 3.5 | Wildcard subject methods (`Lookup<Perm><Type>WildcardSubjects`) unchanged — wrap `HasPublicSubject` per AUZ-008 ADR-003 | same | [x] |

**Key details:** Per SPEC-008 C5 — `<Type>LookupResult` is per-resource-type or per-subject-type, NOT per-permission. Multiple permissions on the same definition share the struct. Per C10 — generated code uses `authz.FromIDs[<Type>]` (NOT `FromIDsExcludingWildcard`) to preserve wildcard semantics on Lookup.

### 4. Fixture migration

Sweep every existing Lookup call site in test files. Mechanical: `ids, err := X.Lookup...` → `result, err := X.Lookup...; ids := result.Definite`.

| #   | Task | File | Status |
|-----|------|------|--------|
| 4.1 | Run codegen — `go run ./cmd/authzed-codegen --output example/authzed example/schema.zed` — commit regenerated `.gen.go` files | `example/authzed/**/*.gen.go` | [x] |
| 4.2 | Migrate call sites in `extsvc_test.go` to consume `LookupResult.Definite` | `example/authzed/extsvc/extsvc_test.go` | [x] |
| 4.3 | Migrate call sites in `bookingsvc_test.go` | `example/authzed/bookingsvc/bookingsvc_test.go` | [x] |
| 4.4 | Migrate call sites in `menusvc_test.go` | `example/authzed/menusvc/menusvc_test.go` | [x] |

**Key details:** Per SPEC-008 A6 / C9 — codegen idempotent at the new baseline; round-trip check (`git diff --quiet example/authzed/`) zero-diff after a second pass against the new template.

### 5. Testing — conditional surfacing

E2E tests against live SpiceDB cover the new Conditional slice population.

| #   | Task | Status |
|-----|------|--------|
| 5.1 | E2E: definite path — caller supplies tenant=acme, Lookup returns `result.Definite` populated, `result.Conditional` empty — `example/authzed/extsvc/extsvc_test.go` | [x] |
| 5.2 | E2E: conditional path — caller omits tenant, Lookup returns empty `Definite` AND `Conditional` populated with `{ID, MissingKeys=["tenant"]}` — same | [x] |
| 5.3 | E2E: hard-deny path — caller supplies tenant="not-acme" (CEL false), Lookup returns empty `Definite` AND empty `Conditional` (CEL false != indeterminate) — same | [x] |
| 5.4 | E2E: mixed conditional/definite — pre-bind tenant on one tuple, leave another conditional; assert `Definite` and `Conditional` both populate with disjoint IDs — same | [x] |
| 5.5 | E2E regression sweep — re-run AUZ-006/007/008 caveat tests; assert no test breaks per A6 (call-site migration is mechanical) | [x] |

### 6. Documentation + release prep

CHANGELOG, README, version bump.

| #   | Task | Status |
|-----|------|--------|
| 6.1 | Add `[1.7.0]` entry to `CHANGELOG.md` documenting the new types, signature changes, migration recipe, deferred items — `CHANGELOG.md` | [x] |
| 6.2 | Update `README.md` — refresh `Conditional Permission` section (after `Sub-relation References`) to cover both Check and Lookup paths with example — `README.md` | [x] |
| 6.3 | Tag `v1.7.0` after merge; create GitHub release with notes calling out the symmetric closure with v1.6 | [x] |

## Design Decisions

### Variant C — uniform replacement
All 4 Lookup paths get the new return shape, even non-caveat paths (where `Conditional` is always empty). Mirrors AUZ-010's variant-C choice for read paths. Schema evolution is invisible to API.

### Per-resource-type result struct
`<Type>LookupResult` is shared across every Lookup method returning that type. Keeps the generated type count manageable (~2 per type, not ~2 per permission per type). Per SPEC-008 C5.

### Engine surface uses untyped runtime types; codegen wraps to typed
Engine returns `authz.LookupResult` with `[]ID`; generated code casts to `<Type>LookupResult` with `[]<Type>`. Mirrors AUZ-010's `RelationTuple` → `<Rel><Type>Relation` pattern.

### `HasPublicSubject` body-only update
External `(bool, error)` signature preserved. Internal switch from `slices.Contains` to `for _, id := range result.Definite` follows the AUZ-010 `HasPublicRelation` body-rewrite precedent. Conditional wildcards are not exposed via this method (per A4 — extremely rare in practice).

## What Stays Unchanged

- `Engine.CheckPermission` / `CheckPermissionWithCaveat` / `CheckPermissionUserset` signatures and behavior — v1.6's typed-error path on Check is the symmetric solution; stays unchanged
- `Engine.CreateRelations` / `CreateRelationsWithCaveat` / `CreateRelationsWithExpiration` / `CreateRelationsToUserset` / `DeleteRelations` / `ReadRelations` — write/read paths don't intersect with Lookup
- `HasPublicRelation` / `HasPublicSubject` external signatures `(bool, error)` — body adapts, signature preserved
- Wildcard-subject methods (`Lookup<Perm><Type>WildcardSubjects`) — still wrap `HasPublicSubject` per AUZ-008 ADR-003
- AUZ-011's userset routing in `Lookup<Perm><Type>Subjects` — stays in place; userset-as-Check-input field on Check inputs continues to route through `CheckPermissionUserset`
- Round-trip idempotency invariant — non-caveat-reaching schemas regenerate with the new shape; subsequent regenerations are zero-diff
- `internal/generator/adapter.go` and `generator.go` — adapter/walker layer unchanged; only the template emits new shapes

## Implementation Order

    1. WS1 Runtime types       ← foundation; pure additions in pkg/authz/
    2. WS2 Engine impl         ← depends on WS1; atomic batch with WS1 (interface + impl)
    3. WS3 Codegen template    ← depends on WS2 (engine return types must match)
    4. WS4 Fixture migration   ← depends on WS3 (regenerated .gen.go); test migration follows
    5. WS5 Tests               ← can parallel with WS4's test migration; new tests cover conditional surfacing
    6. WS6 Docs + release      ← last; depends on everything else passing

WS1 + WS2 + WS3 land as one commit (atomic — interface, impl, template change must land together; the fixture is broken between WS2 and WS3+regen). WS4 lands in the same commit. WS5 follows. WS6 closes the cycle.

## Notes

- No fixture changes — existing AUZ-006 `tenanted_viewer` relation already produces CONDITIONAL_PERMISSION when context is missing. Tests use that fixture directly.
- Round-trip the example fixture before declaring any generator change done. Per `.claude/CLAUDE.md`: `go run ./cmd/authzed-codegen --output example/authzed example/schema.zed && git diff --quiet example/authzed/`.
- Full e2e suite must pass: `go test ./pkg/authz/spicedb/... ./example/authzed/...`.
- Version bump is `1.7.0` (minor). Active-development convention; technically breaking but only consumer is this repo.
- `harness validate-pr-checklist` will hard-block a push with `Status=Done` while any task row is `[ ]`.

## Discoveries & Decisions During Implementation

### [Implementer] Migration scope smaller than estimated

SPEC-008 A6 estimated ~12 Lookup call sites in `example/authzed/**/*_test.go`. Final tally: 11 production sites + 4 new test sites added in WS5. The migration was 100% mechanical (`xxx, err := X.Lookup...` + `assert.Contains(t, xxx, ...)` → `assert.Contains(t, xxx.Definite, ...)`) — no test logic changed. Confirms the variant-C philosophy holds at this codebase scale; uniform replacement remains preferable to schema-evolution-surprise from variant B.

### [Implementer] LSP cache stale, real `go vet` clean

Throughout WS2-WS4 the IDE diagnostics layer reported phantom errors on test sites that compiled cleanly via `go vet ./...` and `go test ./... -count=1`. Same root cause as AUZ-010's discovery: regen-heavy work invalidates the LSP server's cached `.gen.go` view but the tools see fresh state. Authoritative signal during this kind of work remains `go vet ./...` returning empty + `go test` passing — the diagnostics stream is informational only.

### [Implementer] AUZ-008 silent-filter regression test now reads `.Definite`

`TestFolder_LookupTenantedBrowseUserSubjects_ConditionalFiltered` was the original AUZ-008 regression test verifying CONDITIONAL_PERMISSION rows don't leak into the result. Post-AUZ-013 the assertion reads `users.Definite` instead of `users` (the slice → struct shift). Test still passes — the behavior it asserts (CONDITIONAL not appearing as a definite grant) is unchanged; only the access path moved. Documented inline in the test comment.
