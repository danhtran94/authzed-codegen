# [SPEC-009] Consistency Mode Opt-In

| Field      | Value                                                |
|------------|------------------------------------------------------|
| Status     | Accepted                                             |
| Created    | 2026-05-09                                           |
| Author     | Danh Tran                                            |
| Implements | (closes hardcoded-consistency artifact from AUZ-011) |

---

## Overview

This SPEC adds caller-controlled consistency mode selection on the read-side engine paths (Check, Lookup, Read). The current `*spicedb.Engine` hardcodes a time-based policy: pin to `AtExactSnapshot` when a recent write token is available, fall through to `MinimumLatency` (nil) otherwise. That policy is correct for read-your-own-writes but masks SpiceDB's expiration semantics under snapshot consistency — AUZ-011's userset-expiration discovery showed that `CheckPermissionUserset` against an expired tuple returns granted because the snapshot is pinned to the write timestamp, not wall-clock. Security-sensitive callers need to opt out of the cached snapshot. SPEC-009 introduces a context-based override: `authz.WithConsistency(ctx, mode)` lets the caller force `FullyConsistent` evaluation per-call. Codegen surface is unchanged — ctx already flows through every generated method.

**What this component does:** Add `ConsistencyMode` runtime type and `ConsistencyDefault` / `ConsistencyFullyConsistent` constants in `pkg/authz/authz.go`. Add `WithConsistency(ctx, mode)` and `GetConsistency(ctx)` context helpers. Refactor `*spicedb.Engine.getConsistencySnapshot()` to take a `ctx` parameter, read the override via `authz.GetConsistency(ctx)`, and return `Consistency_FullyConsistent` when `ConsistencyFullyConsistent` is set. Default behavior (recent-token-or-nil from the existing time-based logic) is preserved when no override is set. Update the 6 internal call sites in `crud.go` to pass `ctx` to `getConsistencySnapshot`.

**What this component does not do:** Add `AtLeastAsFresh` or `AtExactSnapshot` modes — these require ZedToken management plumbing which the engine already handles internally for read-your-own-writes; surfacing them to callers needs a separate design (token threading, observability). Modify the codegen template — ctx is already plumbed through every generated method, so the override flows through transparently. Change write-side methods (`CreateRelations*`, `DeleteRelations`) — writes always go to the latest revision, no consistency option applies. Provide a global engine-level default — the override is per-call to keep the policy explicit at the call site. Auto-detect security-sensitive permissions — caller decides which checks need full consistency.

---

## Interface Contracts

### Runtime types — `pkg/authz/authz.go`

```go
// ConsistencyMode controls how strongly read-side methods (Check, Lookup,
// Read) observe writes when evaluating against SpiceDB. Set per-call via
// authz.WithConsistency(ctx, mode); read by *spicedb.Engine internally.
type ConsistencyMode int

const (
    // ConsistencyDefault preserves the engine's existing behavior: pin to
    // AtExactSnapshot when a recent write token is available (within the
    // engine's durationExpire window), otherwise fall through to SpiceDB's
    // MinimumLatency default. Optimised for read-your-own-writes after a
    // recent Create/Delete on the same engine instance.
    ConsistencyDefault ConsistencyMode = 0

    // ConsistencyFullyConsistent forces SpiceDB to evaluate against the
    // most up-to-date data, bypassing any cached snapshot. Slower than
    // default; required for security-sensitive checks where stale reads
    // are unacceptable AND for any check that depends on wall-clock
    // semantics like expiration filtering on userset tuples (per
    // AUZ-011 Discoveries).
    ConsistencyFullyConsistent ConsistencyMode = 1
)

// WithConsistency returns a derived context carrying the consistency mode
// override. Engine read-side methods (Check, Lookup, Read) honor the override
// transparently — no codegen-method signature change.
func WithConsistency(ctx context.Context, mode ConsistencyMode) context.Context

// GetConsistency returns the consistency mode set on the context, or
// ConsistencyDefault if not set. Engine impls call this from
// getConsistencySnapshot to drive the per-call wire selection.
func GetConsistency(ctx context.Context) ConsistencyMode
```

### Engine implementation — `pkg/authz/spicedb/crud.go`

`getConsistencySnapshot` gains a `ctx` parameter and switches on the mode:

```go
func (e *Engine) getConsistencySnapshot(ctx context.Context) *v1.Consistency {
    switch authz.GetConsistency(ctx) {
    case authz.ConsistencyFullyConsistent:
        e.debugLog("Using full consistency (ctx override)")
        return &v1.Consistency{
            Requirement: &v1.Consistency_FullyConsistent{FullyConsistent: true},
        }
    default:
        // Existing logic — preserved unchanged.
        now := time.Now().UnixNano()
        if now-e.setTokenTime > e.durationExpire.Nanoseconds() {
            e.debugLog("Using default consistency")
            return nil
        }
        e.debugLog("Using consistency snapshot with token: %s", e.token)
        return &v1.Consistency{
            Requirement: &v1.Consistency_AtExactSnapshot{
                AtExactSnapshot: &v1.ZedToken{Token: e.token},
            },
        }
    }
}
```

All 6 internal call sites in `crud.go` (Check / Lookup / Read paths) update their invocation from `e.getConsistencySnapshot()` to `e.getConsistencySnapshot(ctx)`. Per A1 — every Engine method already takes `ctx` as the first parameter, so this is a mechanical edit.

### Generated code — no change

Generated `Check<Perm>` / `Lookup<Perm>...` / `Read<Rel><Type>Relations` methods bubble `ctx` through to the engine method. The override is invisible to the codegen layer:

```go
// Generated body (unchanged):
err := authz.GetEngine(ctx).CheckPermissionWithCaveat(ctx, ..., caveatCtx)
//                          ^^^                       ^^^
//                          engine method         ctx flows through
```

Caller pattern:

```go
// Default behavior — engine uses recent-token-or-nil:
err := folder.CheckTenantedBrowse(ctx, input)

// Force full consistency for security-sensitive check:
ctx = authz.WithConsistency(ctx, authz.ConsistencyFullyConsistent)
err := folder.CheckTenantedBrowse(ctx, input)
//                                ^^^
//                                Engine reads override and uses FullyConsistent on the wire
```

The override applies to every read-side method called with that ctx. Caller scope it at the request boundary and downstream Check/Lookup/Read inherits transparently.

### Write paths — unchanged

`CreateRelations` / `CreateRelationsWithCaveat` / `CreateRelationsWithExpiration` / `CreateRelationsToUserset` / `DeleteRelations` do not call `getConsistencySnapshot`. SpiceDB writes always commit at the latest revision; consistency is a read-side concept. Per SPEC-009 scope — write paths stay untouched.

---

## Sequence

Wire flow with full-consistency override:

```
caller code:
    ctx = authz.WithConsistency(ctx, authz.ConsistencyFullyConsistent)
    err := folder.CheckTenantedBrowse(ctx, input)
         │
         ▼
generated method body (unchanged):
    └─► engine.CheckPermissionWithCaveat(ctx, ..., caveatCtx)

         │
         ▼
*spicedb.Engine.CheckPermissionWithCaveat:
    ├─► consistency := e.getConsistencySnapshot(ctx)
    │     └─► authz.GetConsistency(ctx) returns ConsistencyFullyConsistent
    │           ├─► returns Consistency{Requirement: FullyConsistent{true}}
    │
    └─► client.CheckPermission(ctx, &CheckPermissionRequest{
            Consistency: <fully-consistent>,
            ...
        })

         │
         ▼
SpiceDB evaluator:
    ├─► reads at most-up-to-date revision (no snapshot pinning)
    ├─► evaluates expiration against wall-clock time → expired tuples filtered
    └─► returns Permissionship reflecting current truth
```

Default behavior (no override):

```
caller code:
    err := folder.CheckTenantedBrowse(ctx, input)  // ctx without override
         │
         ▼
*spicedb.Engine.getConsistencySnapshot(ctx):
    ├─► authz.GetConsistency(ctx) returns ConsistencyDefault
    │
    └─► fall-through to existing logic:
          ├─► if recent token exists → AtExactSnapshot pinned
          └─► else → nil (MinimumLatency on the wire)
```

---

## Errors

No new error classes. The override changes the wire-level `Consistency` field; SpiceDB returns the same error shape regardless of consistency mode. Per A2 — invalid consistency requests are caught at compile time (the proto enum is closed).

---

## Constraints

- **C1.** `ConsistencyMode` is a closed `int` type. Adding new modes (e.g. `ConsistencyMinimumLatency` to force minimum-latency even when a recent token exists) is additive — append constants, no existing-caller break. Per A3 — Go's const iota gives stable integer values for backward compat.

- **C2.** `WithConsistency(ctx, ConsistencyDefault)` is a no-op semantically — engine reads the override, sees default, falls through to existing logic. Useful for explicitly resetting the override at a sub-request boundary.

- **C3.** Override propagates through every read-side call until the ctx is replaced. Per the request-scoped semantics — caller sets at the request boundary, all downstream Check/Lookup/Read inherit. To scope tighter, derive a child ctx without override.

- **C4.** Write paths (`CreateRelations*`, `DeleteRelations`) ignore the consistency override. Writes commit at the latest revision regardless. Per SPEC-009 scope — write-side consistency is not a concept SpiceDB exposes.

- **C5.** Engine's existing recent-token-based logic is preserved for `ConsistencyDefault`. Read-your-own-writes optimisation continues to work — a Create<Rel>Relations followed by a Check<Perm> on the same engine instance still uses AtExactSnapshot pinned to the write token, returning the just-written tuple deterministically.

- **C6.** No codegen template change. Round-trip idempotency stable — `git diff --quiet example/authzed/` is zero-diff against v1.7.0. Generated `.gen.go` files are byte-identical.

- **C7.** `AtLeastAsFresh` and `AtExactSnapshot` modes are deferred. The engine already uses `AtExactSnapshot` internally for read-your-own-writes; surfacing token-based modes to callers needs a separate design (caller-supplied ZedToken plumbing, observability for token freshness, semantics for stale-token rejection). Tracked as future work.

- **C8.** Test verification uses the existing AUZ-011 `temp_collab` fixture. Without override, the userset-Check-after-expiry returns granted (as documented in AUZ-011 Discoveries). With `ConsistencyFullyConsistent` override, SpiceDB evaluates expiration against wall-clock and returns `ErrPermissionDenied`. This is the canonical demonstration of the override's value.

---

## Assumptions

- **A1 [VERIFIED]:** Every `*spicedb.Engine` read-side method already takes `ctx` as the first parameter. Evidence: `pkg/authz/spicedb/crud.go` — `CheckPermission`, `CheckPermissionWithCaveat`, `CheckPermissionUserset`, `LookupResources*`, `LookupSubjects*`, `ReadRelations`, `HasPublicRelation`, `HasPublicSubject` all start with `ctx context.Context`. Refactoring `getConsistencySnapshot()` to take ctx is a mechanical edit at the call site.

- **A2 [EXTERNAL FACT]:** SpiceDB's `Consistency` proto is a oneof of four explicit modes: `MinimumLatency` (default — implicit when nil), `AtLeastAsFresh`, `AtExactSnapshot`, `FullyConsistent`. Per the proto definition, the type is closed; invalid combinations are unrepresentable. Evidence: `go doc github.com/authzed/authzed-go/proto/authzed/api/v1 Consistency`.

- **A3 [VERIFIED]:** `iota`-based `int` constants are positional-stable across Go versions and binary compatibility — adding new constants at the end never breaks existing consumers using the named constants. Evidence: Go spec; standard pattern in `errors` (e.g. `os.O_RDONLY` etc.).

- **A4 [VERIFIED]:** AUZ-011 Discoveries section documented the userset-expiration-under-AtExactSnapshot artifact: writing an expiring userset tuple, sleeping past the TTL, and calling `CheckPermissionUserset` returns granted because the snapshot pins evaluation to the write revision. Evidence: `jobs/AUZ-011-sub-relation-references.md` Discoveries section "AtExactSnapshot pins userset expiration evaluation to the snapshot revision". SPEC-009's `ConsistencyFullyConsistent` override resolves the artifact when callers opt in.

- **A5 [HYPOTHESIS]:** Most production read paths can use `ConsistencyDefault` safely. Read-your-own-writes via the engine's existing token logic covers the common case. Security-sensitive paths (auth checks for actions with side effects, expiration-bound usersets, rebound-permission-after-revocation) opt into `ConsistencyFullyConsistent`. Verification: deferred to production use; the SPEC ships both modes and documents the trade-off.

- **A6 [VERIFIED]:** `context.WithValue` with a private struct key is the idiomatic Go pattern for request-scoped values. The pattern is used in `net/http` (`http.NoBody`), `database/sql`, and standard library auth code. The unexported `consistencyKey struct{}` prevents external collisions.

---

## Unresolved Questions

(none)

---

## Summary

Net change scope:

| File | Change |
|---|---|
| `pkg/authz/authz.go` | Add `ConsistencyMode int` type and `ConsistencyDefault` / `ConsistencyFullyConsistent` constants. Add `WithConsistency(ctx, mode)` and `GetConsistency(ctx)` context helpers. Add unexported `consistencyKey struct{}` for the context value key. |
| `pkg/authz/spicedb/crud.go` | Refactor `getConsistencySnapshot()` to take `ctx context.Context`. Switch on `authz.GetConsistency(ctx)`: `ConsistencyFullyConsistent` returns `Consistency_FullyConsistent`; default falls through to existing recent-token-or-nil logic. Update 6 internal call sites to pass ctx. |
| `example/authzed/extsvc/extsvc_test.go` | Add e2e tests demonstrating: (a) default behavior unchanged (regression check); (b) full-consistency override returns ErrPermissionDenied for expired userset tuple (closes AUZ-011 artifact); (c) full-consistency override returns ErrPermissionDenied for expired direct-subject tuple under fresh-write conditions. |
| `internal/templates/object.go.tmpl` | NO CHANGES. ctx already flows through every generated method. |
| `example/authzed/**/*.gen.go` | NO REGENERATION required. Codegen output byte-identical to v1.7.0. |

E2E tests verify: default-ctx behavior unchanged across all 4 e2e packages (regression sweep); explicit full-consistency override flips userset-expiration Check from granted to denied; explicit override on direct-subject Check produces same denied semantics under expired conditions.

---

## History

(History is owned by `harness history-update` — do not hand-edit.)
