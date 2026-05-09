# AUZ-014: Consistency Mode Opt-In

| Field      | Value                                          |
|------------|------------------------------------------------|
| Status     | Done                                           |
| Created    | 2026-05-09                                     |
| Assignee   | danhtran94                                     |
| Source     | docs/spec-009-consistency-mode-opt-in.md       |
| Blocked by | —                                              |

<!-- approved -->

---

## Goal

Surface SpiceDB's consistency mode as a per-call override so callers can force `FullyConsistent` evaluation on security-sensitive checks. Today the `*spicedb.Engine` hardcodes a time-based policy: pin to `AtExactSnapshot` post-write, fall through to `MinimumLatency` otherwise. AUZ-011's userset-expiration discovery showed this masks wall-clock semantics — expired tuples appear granted when evaluated against a pinned snapshot. After this job, callers can opt into full consistency via `authz.WithConsistency(ctx, authz.ConsistencyFullyConsistent)`; the override flows through every Check / Lookup / Read method via context. Zero codegen template change; no breaking signature on existing callers.

## Problem

    Current (post-v1.7.0):
      caller has a security-sensitive Check:
        folder.CheckTempCollabView(ctx, input)  // userset + expiration
          → engine.CheckPermissionUserset → SpiceDB returns:
              granted, even when wall-clock is past expiry
            ✗ snapshot pinned to write revision masks expiration
            ✗ no caller-side knob to opt out of cached snapshot

The artifact is documented in AUZ-011 Discoveries. SpiceDB supports four consistency modes on the wire; the engine always picks one. Callers have no override.

## Solution: Context-based override carrying ConsistencyMode

    After fix:
      caller code:
        ctx = authz.WithConsistency(ctx, authz.ConsistencyFullyConsistent)
        err := folder.CheckTempCollabView(ctx, input)
          → engine.CheckPermissionUserset → consistency = FullyConsistent
            → SpiceDB evaluates against most-up-to-date data + wall-clock
              → expired tuple filtered → ErrPermissionDenied ✓

The override propagates via ctx through every read-side method. Zero codegen template change — ctx already flows through. The engine's existing recent-token-or-nil logic remains the default; the override is opt-in per call.

### Components

**`authz.ConsistencyMode`** — closed `int` type with two constants in v1: `ConsistencyDefault` (existing engine behavior), `ConsistencyFullyConsistent` (SpiceDB full read).

**`authz.WithConsistency(ctx, mode)`** — context-derived helper carrying the override.

**`authz.GetConsistency(ctx)`** — engine reads override; returns `ConsistencyDefault` when not set.

**`*spicedb.Engine.getConsistencySnapshot(ctx)`** — refactored to take ctx; switches on `GetConsistency(ctx)`. `FullyConsistent` returns `Consistency_FullyConsistent`; default falls through to existing recent-token logic.

### Why not alternatives

| Approach | Verdict |
|---|---|
| **Context-based override** (chosen) | Zero codegen template change. Idiomatic Go for request-scoped values. Caller sets once at request boundary; downstream Check/Lookup/Read inherit. |
| Variadic options on every Check method | Rejected. Adds boilerplate to ~50 generated method signatures. Each caveat / userset / non-caveat variant gets the option list. |
| Engine-level global default | Rejected. Heavy-handed; whole engine becomes one-mode-fits-all. Per-call control needed for mixed workloads. |
| New parallel methods (e.g. `CheckXFresh`) | Rejected. Doubles the codegen surface; users have to pick one per relation. |
| `AtLeastAsFresh` / `AtExactSnapshot` modes for v1 | Deferred (per SPEC-009 C7). Token-based modes need ZedToken plumbing — caller-supplied tokens, observability for freshness, semantics for stale-token rejection. Future SPEC. |

## Workstreams

### 1. Runtime types + context helpers

Add `ConsistencyMode`, constants, `WithConsistency`/`GetConsistency` in `pkg/authz/`. Foundation for the engine refactor.

| #   | Task | File | Status |
|-----|------|------|--------|
| 1.1 | Add `ConsistencyMode int` type with `ConsistencyDefault` (=0) and `ConsistencyFullyConsistent` (=1) constants | `pkg/authz/authz.go` | [x] |
| 1.2 | Add unexported `consistencyKey struct{}` for the context value key | same | [x] |
| 1.3 | Add `WithConsistency(ctx context.Context, mode ConsistencyMode) context.Context` helper | same | [x] |
| 1.4 | Add `GetConsistency(ctx context.Context) ConsistencyMode` helper returning `ConsistencyDefault` when not set | same | [x] |

**Key details:** Per SPEC-009 C1 — `iota`-based int constants are positional-stable; appending new modes is additive. Per A6 — unexported struct key prevents external collisions on context values.

### 2. Engine impl — `getConsistencySnapshot` refactor

Refactor the central consistency-selection helper to take ctx, switch on the override, and update all 6 internal call sites.

| #   | Task | File | Status |
|-----|------|------|--------|
| 2.1 | Change `getConsistencySnapshot()` signature to `getConsistencySnapshot(ctx context.Context)`; switch on `authz.GetConsistency(ctx)` — `ConsistencyFullyConsistent` returns `Consistency_FullyConsistent`; default branch preserves existing recent-token-or-nil logic | `pkg/authz/spicedb/crud.go` | [x] |
| 2.2 | Update 6 internal call sites in Check / Lookup / Read paths to pass `ctx` to `getConsistencySnapshot` | same | [x] |

**Key details:** Per SPEC-009 A1 — every read-side method already takes ctx as first parameter; the call site update is purely mechanical. Per C5 — default behavior preserved unchanged for read-your-own-writes optimisation.

### 3. Testing — full-consistency override flips expired-userset behavior

E2E tests verify the override resolves AUZ-011's userset-expiration artifact and confirm default behavior is unchanged.

| #   | Task | Status |
|-----|------|--------|
| 3.1 | E2E: default-ctx behavior unchanged — re-run AUZ-011's `TestFolder_TempCollab_UsersetWithExpiration` and the broader AUZ-009/006/007 caveat suite; assert no test breaks — `example/authzed/extsvc/extsvc_test.go` | [x] |
| 3.2 | E2E: full-consistency override on userset expiration — write `temp_collab` userset with short TTL, sleep past expiry, Check WITH `ConsistencyFullyConsistent` → assert `ErrPermissionDenied` (verifies override path) — same | [x] |
| 3.3 | E2E: full-consistency override on direct-subject expiring tuple — write `expiring_viewer` with short TTL, sleep past, Check WITH override → assert `ErrPermissionDenied` (sanity check) — same | [x] |
| 3.4 | E2E: full-consistency override on non-expiring tuple — write a regular `viewer` tuple, Check WITH override → assert grant; verifies override doesn't break the happy path — same | [x] |
| 3.5 | E2E: regression sweep — `go test ./pkg/authz/spicedb/... ./example/authzed/...`; assert all 4 packages pass after WS2 refactor and WS3 additions — `example/authzed/extsvc/extsvc_test.go` | [x] |

### 4. Documentation + release prep

CHANGELOG, README, version bump.

| #   | Task | Status |
|-----|------|--------|
| 4.1 | Add `[1.8.0]` entry to `CHANGELOG.md` documenting the new `ConsistencyMode` type, helpers, engine refactor, and the AUZ-011-hypothesis re-verification — `CHANGELOG.md` | [x] |
| 4.2 | Update `README.md` — add `Consistency` section after `Conditional Permission` showing the override pattern — `README.md` | [x] |
| 4.3 | Tag `v1.8.0` after merge; create GitHub release with notes calling out the security-sensitive-checks use case | [x] |

## Design Decisions

### Context-based override
ctx already flows through every generated method. Zero codegen template change required. Caller sets once at request boundary; downstream inherits. Mirrors stdlib patterns (`context.WithValue` with private key).

### Two modes in v1, defer token-based modes
`ConsistencyDefault` (existing) + `ConsistencyFullyConsistent` (new) cover the 80% case. `AtLeastAsFresh` / `AtExactSnapshot` need ZedToken management plumbing — separate design, separate SPEC.

### Engine state preserved as default
The existing recent-token logic remains the default consistency selection. Read-your-own-writes optimisation continues to work unchanged. The override is purely additive.

### No write-side change
Writes always go to the latest revision. SpiceDB doesn't expose write-side consistency; SPEC-009 doesn't introduce one.

## What Stays Unchanged

- `Engine.CheckPermission` / `CheckPermissionWithCaveat` / `CheckPermissionUserset` signatures — no changes
- `Engine.LookupResources` / `LookupResourcesWithCaveat` / `LookupSubjects` / `LookupSubjectsWithCaveat` signatures — no changes
- `Engine.ReadRelations` signature — no change
- `Engine.HasPublicRelation` / `HasPublicSubject` signatures — no changes
- `Engine.CreateRelations*` / `DeleteRelations` — write-side methods don't observe consistency; no change
- All generated `Check<Perm>` / `Lookup<Perm>...` / `Read<Rel><Type>Relations` method signatures — codegen unchanged
- Round-trip idempotency invariant — `git diff --quiet example/authzed/` zero-diff against v1.7.0
- `example/authzed/**/*.gen.go` — no regeneration required
- `internal/templates/object.go.tmpl` — no changes
- `internal/generator/adapter.go` / `generator.go` — no changes
- Existing recent-token-based behavior — preserved as `ConsistencyDefault` fallthrough

## Implementation Order

    1. WS1 Runtime types + helpers   ← foundation; pure additions in pkg/authz/
    2. WS2 Engine impl refactor       ← depends on WS1 (GetConsistency must exist)
    3. WS3 Tests                       ← depends on WS2 (engine must honor override)
    4. WS4 Docs + release              ← last; depends on test pass

WS1 + WS2 land as one commit (atomic — types must exist before the engine reads them). WS3 lands separately for reviewability. WS4 closes the cycle.

## Notes

- No fixture changes — existing AUZ-009 `expiring_viewer` and AUZ-011 `temp_collab` fixtures already produce expired tuples for testing.
- Codegen is unchanged; round-trip check should be zero-diff against v1.7.0 baseline.
- Version bump is `1.8.0` (minor) — pure additive runtime API. No breaking changes per SPEC-009 C5/C6.
- `harness validate-pr-checklist` will hard-block a push with `Status=Done` while any task row is `[ ]`.
- Test the override flips behavior on userset (AUZ-011 artifact) AND verify default-ctx behavior across the full suite is unchanged.

## Discoveries & Decisions During Implementation

### [Implementer] AUZ-011 userset-expiration hypothesis didn't reproduce under v1.8 testing

WS3 task 3.2 was framed as "closes AUZ-011 artifact" — SPEC-009 C8 / A4 hypothesized that `AtExactSnapshot` consistency masks wall-clock expiration on userset tuples (per AUZ-011 Discoveries section "AtExactSnapshot pins userset expiration evaluation to the snapshot revision"). The original AUZ-011 Discovery documented `CheckPermissionUserset` returning granted on a userset tuple after sleeping past TTL.

Empirical re-verification during AUZ-014 with the same fixture (`temp_collab`), same TTL (150ms), same sleep (250ms), and confirmed AtExactSnapshot path (debug log: "Using consistency snapshot with token: ...") returned `ErrPermissionDenied` — SpiceDB filtered the expired tuple under default consistency, contradicting the AUZ-011 Discovery. The behavior under `ConsistencyFullyConsistent` was the same (deny).

Possible explanations: SpiceDB v1.40+ enforces wall-clock expiration filtering regardless of the snapshot revision pin (the snapshot controls which tuples are visible, but expiration is evaluated at evaluation time, not snapshot time); OR the AUZ-011 observation was timing-flaky.

Net consequence: AUZ-014's value framing shifted. The override is still useful for security-sensitive workloads where the engine's time-based policy is too permissive (e.g. caller wants to skip the recent-token pin and force fresh evaluation), but the dramatic before/after demonstration on userset expiration doesn't reproduce. Removed the failed test (`TestFolder_TempCollab_ExpiredUserset_DefaultConsistencyGrants` — asserted grant, got deny); kept the override-works tests which demonstrate the new feature behaves correctly in both expired and non-expired scenarios.

### [Implementer] Refactor of `getConsistencySnapshot` was mechanical

Six internal call sites in `crud.go` (Check / CheckWithCaveat / CheckUserset / LookupResources / LookupSubjects / ReadRelations) all already had `ctx` as the first parameter. The refactor `getConsistencySnapshot()` → `getConsistencySnapshot(ctx)` was a pure mechanical sweep — `replace_all` with the corrected call. No semantic edits beyond the signature.

### [Implementer] No codegen template change confirmed at runtime

SPEC-009 claimed zero template change; verified by `git diff --quiet example/authzed/` returning zero against the v1.7.0 baseline AFTER the engine refactor. Generated `.gen.go` files are byte-identical. The override propagates entirely through the ctx already plumbed by the codegen.
