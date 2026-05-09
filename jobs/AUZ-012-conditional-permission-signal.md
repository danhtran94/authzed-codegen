# AUZ-012: Conditional Permission Rich Signal

| Field      | Value                                          |
|------------|------------------------------------------------|
| Status     | Done                                           |
| Created    | 2026-05-09                                     |
| Assignee   | danhtran94                                     |
| Source     | docs/spec-007-conditional-permission-signal.md |
| Blocked by | —                                              |

<!-- approved -->

---

## Goal

Surface SpiceDB's `CONDITIONAL_PERMISSION` Permissionship as a typed error carrying `MissingKeys []string`. Today the codegen collapses CONDITIONAL → `ErrPermissionDenied` and silently drops `PartialCaveatInfo.MissingRequiredContext`; recoverable failures (caller forgot a caveat key) become indistinguishable from hard denies. After this job, callers reaching caveats can distinguish the three semantic cases — granted, conditional-with-recovery-hint, hard-denied — and can use the missing-keys list to fetch context and retry. Backward compatibility is preserved via a custom `Is` method on the typed error so existing `errors.Is(err, ErrPermissionDenied)` checks keep matching all deny cases.

## Problem

    Current (post-v1.5.0):
      caller forgets caveat context:
        Check<Perm>(ctx, input)  // input.Caveats.TenantMatch == nil
          → engine.CheckPermissionWithCaveat → SpiceDB returns:
              Permissionship: CONDITIONAL_PERMISSION
              PartialCaveatInfo.MissingRequiredContext: ["tenant"]
            → errorIfDenied collapses to: ErrPermissionDenied ✗
              caller gets generic "permission denied"
              ✗ no way to distinguish "missing context" from "hard deny"
              ✗ cannot recover by fetching the missing keys

The collapse has been documented as deferred work in CHANGELOG entries v1.1.0 / v1.2.0 / v1.3.0 / v1.4.0. SpiceDB returns the recovery hint on the wire; the codegen layer throws it away.

## Solution: Typed error with backward-compat `Is` matching

    After fix:
      caller forgets caveat context:
        Check<Perm>(ctx, input)
          → engine.CheckPermissionWithCaveat → SpiceDB returns CONDITIONAL
            → errorIfDenied switches on Permissionship:
                CONDITIONAL → return &ConditionalPermissionError{MissingKeys: ["tenant"]}
              caller distinguishes:
                errors.Is(err, ErrConditionalPermission) → true (rich-signal path)
                errors.As(err, &cpe) → cpe.MissingKeys == ["tenant"]
                errors.Is(err, ErrPermissionDenied) → true (backward compat)

The single change to `errorIfDenied` (the central error-construction point) propagates the rich signal to every existing caller — `CheckPermission`, `CheckPermissionWithCaveat`, `CheckPermissionUserset`. Generated `Check<Perm>` methods inherit automatically; zero codegen template diff.

### Components

**`authz.ErrConditionalPermission`** — sentinel error for `errors.Is` matching the rich-signal path.

**`authz.ConditionalPermissionError`** — typed struct carrying `MissingKeys []string`; implements custom `Is` method matching both `ErrConditionalPermission` AND `ErrPermissionDenied`.

**`*spicedb.Engine.errorIfDenied`** — switch on `Permissionship`; CONDITIONAL constructs the typed pointer error from `PartialCaveatInfo.MissingRequiredContext`.

### Why not alternatives

| Approach | Verdict |
|---|---|
| **Typed error + sentinel + custom `Is`** (chosen) | KISS. Maintains `(bool, error)` return shape across every Check<Perm>. Zero codegen template change. Idiomatic Go via `errors.Is`/`errors.As`. |
| New parallel method `Check<Perm>WithMissing` | Rejected. Adds a generated method per caveated permission; doubles the surface for a quiet correctness improvement. |
| Tri-state result struct on existing method | Rejected. Breaks every existing call site; cost outweighs the explicit-state benefit when typed-error patterns work. |
| Auto-retry helper in the engine | Out of scope. The SPEC surfaces the missing keys; deciding whether to fetch and retry is the caller's concern. A future helper could wrap this pattern. |

## Workstreams

### 1. Runtime types

Add the sentinel + typed error in `pkg/authz/`. Foundation for the engine impl change.

| #   | Task | File | Status |
|-----|------|------|--------|
| 1.1 | Add `ErrConditionalPermission` sentinel via `errors.New` | `pkg/authz/authz.go` | [x] |
| 1.2 | Add `ConditionalPermissionError` struct with `MissingKeys []string` field | same | [x] |
| 1.3 | Implement `Error()` returning `"conditional permission: missing %v"` formatted string | same | [x] |
| 1.4 | Implement custom `Is(target error) bool` matching both `ErrConditionalPermission` and `ErrPermissionDenied` | same | [x] |
| 1.5 | Add `errors`, `fmt` imports if missing (likely both already present) | same | [x] |

**Key details:** Per SPEC-007 C1 — return as a pointer (`*ConditionalPermissionError`) for `errors.As` compat. Per A3/A4 — Go stdlib `errors.Is`/`errors.As` honor custom `Is` methods and pointer type matching.

### 2. Engine impl — extend `errorIfDenied`

Update the single point of error construction. All existing Check methods (`CheckPermission`, `CheckPermissionWithCaveat`, `CheckPermissionUserset`) call `errorIfDenied`, so they all inherit the new behavior automatically.

| #   | Task | File | Status |
|-----|------|------|--------|
| 2.1 | Replace the if/else in `errorIfDenied` with a `switch res.Permissionship` covering HAS_PERMISSION, CONDITIONAL_PERMISSION, default | `pkg/authz/spicedb/crud.go` | [x] |
| 2.2 | On CONDITIONAL_PERMISSION, read `res.PartialCaveatInfo.MissingRequiredContext` (nil-safe) and return `&authz.ConditionalPermissionError{MissingKeys: ...}` | same | [x] |

**Key details:** Per SPEC-007 C3 / A2 — `MissingKeys` may be empty when SpiceDB returns CONDITIONAL without populated `MissingRequiredContext`. Code must handle the nil `PartialCaveatInfo` case explicitly. No new imports needed; `v1.CheckPermissionResponse_PERMISSIONSHIP_CONDITIONAL_PERMISSION` is already in the imported proto types.

### 3. Testing — three semantic cases distinguish

E2E tests against live SpiceDB cover the new branch and verify backward compat at the `errors.Is(_, ErrPermissionDenied)` level.

| #   | Task | Status |
|-----|------|--------|
| 3.1 | E2E: granted path — supply tenant=acme matching schema, assert `err == nil` — `example/authzed/extsvc/extsvc_test.go` | [x] |
| 3.2 | E2E: conditional path — write `tenanted_viewer` tuple WITHOUT pre-context; Check WITHOUT input.Caveats.TenantMatch → assert `errors.Is(err, ErrConditionalPermission)` AND `errors.As(err, &cpe)` extracts `MissingKeys = ["tenant"]` — same | [x] |
| 3.3 | E2E: hard-deny path — supply tenant="not-acme" (caveat evaluates false); assert `errors.Is(err, ErrPermissionDenied)` AND `!errors.Is(err, ErrConditionalPermission)` (CEL returned false, not indeterminate) — same | [x] |
| 3.4 | E2E: backward-compat assertion — conditional path also matches `errors.Is(_, ErrPermissionDenied)` (custom `Is` matches both targets per SPEC-007 C2) — same | [x] |
| 3.5 | E2E: regression sweep — re-run AUZ-006/007 caveat tests; assert no test breaks per SPEC-007 A6 | [x] |

### 4. Documentation + release prep

CHANGELOG, README, version bump.

| #   | Task | Status |
|-----|------|--------|
| 4.1 | Add `[1.6.0]` entry to `CHANGELOG.md` documenting the new error type, backward-compat preservation, deferred Lookup filter — `CHANGELOG.md` | [x] |
| 4.2 | Update `README.md` — add `Conditional Permission` section after `Sub-relation References` showing the three-case caller pattern with `errors.Is` / `errors.As` — `README.md` | [x] |
| 4.3 | Tag `v1.6.0` after merge; create GitHub release with notes calling out the rich-signal addition and the explicit backward-compat preservation | [x] |

## Design Decisions

### Single point of change in `errorIfDenied`
All Check methods (existing and future) call `errorIfDenied` to construct deny errors. Updating that one function propagates the rich signal everywhere — CheckPermission, CheckPermissionWithCaveat (AUZ-006/007), CheckPermissionUserset (AUZ-011). Per SPEC-007 — zero codegen template change required, zero regenerated `.gen.go` files.

### Backward compat via custom `Is` method
The typed `*ConditionalPermissionError.Is(target)` returns true for BOTH `ErrConditionalPermission` AND `ErrPermissionDenied`. Existing v1.5 code checking `errors.Is(err, ErrPermissionDenied)` keeps matching. New code can additionally check for the conditional sentinel and extract details. Avoids breaking changes for the only consumer (this repo).

### Lookup paths unchanged
AUZ-008 already silently filters `CONDITIONAL_PERMISSION != HAS_PERMISSION` from Lookup results. Surfacing the conditional-but-recoverable subset would require changing the typed return shape (e.g. add `[]ConditionalEntry{ID, MissingKeys}` alongside `[]ID`). Heavier scope; deferred to a future SPEC if real demand surfaces.

### `MissingKeys` is `[]string`, not `map`
The wire field `PartialCaveatInfo.MissingRequiredContext` is a list of parameter names. Mapping back to typed `<Caveat>Args` is the caller's concern — callers know which caveat the missing keys belong to from the call context (the permission they Checked). Auto-decode would require enumerating reachable caveats per permission and is out of scope.

## What Stays Unchanged

- `Engine.CheckPermission` / `CheckPermissionWithCaveat` / `CheckPermissionUserset` signatures — only the error type returned changes
- `Engine.LookupResources` / `LookupSubjects` / `*WithCaveat` — Lookup paths keep the silent filter
- `Engine.CreateRelations` / `CreateRelationsWithCaveat` / `CreateRelationsWithExpiration` / `CreateRelationsToUserset` / `DeleteRelations` / `ReadRelations` / `HasPublicRelation` / `HasPublicSubject` — write/read paths don't intersect with Permissionship signaling
- `internal/templates/object.go.tmpl` — no template changes (rich error flows through existing return)
- `internal/generator/adapter.go` / `generator.go` — no codegen-layer changes
- `example/authzed/**/*.gen.go` — no regenerated files; round-trip stable
- `Check<Perm>Inputs` struct shapes — caller still supplies the same caveat fields; the engine just returns more detail when context is missing
- `ErrPermissionDenied` sentinel — preserved as the bucket for `NO_PERMISSION` AND (via custom `Is`) the backward-compat match for `CONDITIONAL_PERMISSION`

## Implementation Order

    1. WS1 Runtime types       ← foundation; pure additions in pkg/authz/
    2. WS2 Engine impl         ← depends on WS1 (the typed error must exist)
    3. WS3 Tests               ← depends on WS2 (engine must return the new error)
    4. WS4 Docs + release      ← last; depends on test pass

WS1 + WS2 land as one commit (atomic — types and impl belong together). WS3 lands separately to keep the test diff reviewable. WS4 closes the cycle.

## Notes

- No fixture changes — the existing AUZ-006 `tenanted_viewer` relation already produces CONDITIONAL_PERMISSION when caveat context is missing. Tests use that fixture directly.
- Full e2e suite must pass: `go test ./pkg/authz/spicedb/... ./example/authzed/...`. Tests skip cleanly when Docker is unavailable.
- Codegen is unchanged; round-trip check (`git diff --quiet example/authzed/`) should remain zero-diff against v1.5.0 baseline.
- Version bump is `1.6.0` (minor) — additive runtime types, no breaking changes (per SPEC-007 C2/C6 backward compat).
- `harness validate-pr-checklist` will hard-block a push with `Status=Done` while any task row is `[ ]`.

## Discoveries & Decisions During Implementation

### [Implementer] No discoveries this session

WS1-WS4 proceeded exactly as planned. The atomic batch (types + engine impl) compiled clean on first edit; the four e2e tests passed first-try; full regression sweep (4 packages) green. SPEC-007's design picks (typed-error + custom Is method, single change in errorIfDenied, zero codegen change) held up perfectly — the rich signal flows through every existing Check method automatically. Backward-compat custom Is method exercises in the e2e suite confirm AUZ-006/007 caveat tests still pass without modification, validating the central design constraint.
