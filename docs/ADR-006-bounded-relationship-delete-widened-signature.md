# [ADR-006] Bounded Relationship Delete: Widening DeleteRelationsMatching over a Sibling Method

| Field      | Value                                                   |
|------------|----------------------------------------------------------|
| Status     | Accepted                                                  |
| Date       | 2026-11-13                                                |
| Deciders   | Danh Tran                                                 |
| Scope      | pkg/authz Engine interface + spicedb engine impl          |
| Depends on | docs/scope-bounded-batch-relationship-delete.md           |

---

## Context

`docs/scope-bounded-batch-relationship-delete.md` adds a bounded/partial relationship-delete capability so an object with millions of tuples doesn't hit the single-transaction timeout that `authz.Engine.DeleteRelationsMatching(ctx, f RelationFilter) error` (`ADR-005`, `OptionalLimit: 0` always) is exposed to today. SpiceDB's `DeleteRelationshipsRequest` already carries the mechanism — `OptionalLimit` + `OptionalAllowPartialDeletions` bound one call and report `DeletionProgress` / `RelationshipsDeletedCount` instead of erroring (per A4) — the only question this ADR resolves is where that capability lands on the `authz.Engine` interface.

`ADR-005`'s own Negative consequence named this exact fork and deferred it — "Adding the loop later means an `opts` parameter on `DeleteRelationsMatching` (or a sibling method), and `OptionalLimit: 0` callers must keep working unchanged." The scope note that opened this ADR (SC2) initially leaned toward the sibling-method reading of that sentence, requiring `DeleteRelationsMatching`'s signature to stay byte-identical — but during this ADR's drafting the author weighed that against the interface's long-term shape and decided the growing-method-count cost (per A6) outweighs the one-time cost of widening the existing method now, while adoption is near zero (per A7). This ADR's Decision supersedes scope SC2 accordingly (see Consequences).

## Options

### Option A — Explicit `DeleteOptions` struct parameter, widened return

```go
// DeleteOptions configures a relationship delete. The zero value matches
// today's unbounded, single-transaction behavior.
type DeleteOptions struct {
    Limit uint32 // 0 = delete everything in one transaction (unbounded); >0 bounds the call
}

// DeletionProgress reports the outcome of a delete.
type DeletionProgress struct {
    Deleted  uint64 // relationships deleted by this call
    Complete bool   // false => more remain; call again with the same filter and opts
}

DeleteRelationsMatching(ctx context.Context, f RelationFilter, opts DeleteOptions) (DeletionProgress, error)
```

`opts` is a required, concrete-typed parameter — not variadic, not a functional-options closure. Every call site writes `authz.DeleteOptions{}` explicitly for today's behavior; nothing is hidden or inferred. `Limit: 0` mapping to "unbounded" is not an arbitrary Go-side convention — it mirrors SpiceDB's own wire-level `OptionalLimit == 0` meaning "unlimited" (per A4), so the zero value is the natural default, not a special case to remember. `DeleteOptions` can grow new fields later without another signature change — this one widening absorbs every future filter-delete variant this family is likely to need. But: this breaks `DeleteRelationsMatching`'s existing signature — every call site gains a required argument and every `Engine` implementer's return type changes — a MAJOR-bump interface break, same cost as the variadic version (Option C) with none of its indirection.

### Option B — New sibling method `DeleteRelationsMatchingBounded`

```go
DeleteRelationsMatchingBounded(ctx context.Context, f RelationFilter, limit uint32) (DeletionProgress, error)
```

Purely additive: `DeleteRelationsMatching` keeps its exact current signature; `*spicedb.Engine` gains one new method. MINOR bump, matches `ADR-003`'s precedent of deferring breaking changes until a concrete need materializes (per A7). But: the interface now carries two delete-by-filter methods instead of one, and every future filter-delete variant — cursor-resume being the next-named candidate in the scope note's Out of Scope — is a candidate for a third, then a fourth. Not chosen: it trades a breaking change that is currently free (per A6) for a permanently, and indefinitely, growing method-per-variant surface.

### Option C — Variadic functional options, widened return

```go
type DeleteOption func(*deleteOpts)
func WithLimit(n uint32) DeleteOption

DeleteRelationsMatching(ctx context.Context, f RelationFilter, opts ...DeleteOption) (DeletionProgress, error)
```

Call sites that want today's behavior write nothing extra — `opts` defaults to empty. But: this is exactly the "clean but magic" shape rejected during this ADR's drafting — the method's declared signature gives no visible indication of what `WithLimit` does or what else could be passed; the actual behavior (bounded vs. unbounded) dispatches on opaque runtime state inside unexported closures, for what is structurally a single optional integer. It still breaks the signature's return arity, so it buys reduced call-site friction at the cost of discoverability, not at a lower breaking-change cost than Option A.

### Option D — Sentinel error on the existing method, variadic limit parameter

```go
DeleteRelationsMatching(ctx context.Context, f RelationFilter, limit ...uint32) error

var ErrDeletionIncomplete = errors.New("deletion incomplete, call again")
```

Keeps the return type as `error`-only — no return-arity break — and reuses this codebase's own precedent for signaling extra outcome state through a typed/sentinel error rather than a second return value (per A5). But: a variadic parameter is still part of a method's declared signature — every `Engine` implementer must add it to keep implementing the interface (per A2), which still breaks the signature; and the sentinel-error signal can carry "complete or not" but never a deleted count, discarding information the wire response already provides for free.

## Options Comparison

| Driver | A: Explicit struct opts | B: Sibling method | C: Variadic functional opts | D: Sentinel error + variadic |
|--------|--------------------------|--------------------|-------------------------------|-------------------------------|
| Breaks `DeleteRelationsMatching` signature | Yes — param + return | No | Yes — return arity | Yes — param list |
| Call-site visibility of behavior | Fully explicit | Fully explicit (own method) | Hidden in closures | Hidden in sentinel check |
| New interface methods | Zero | One | Zero | Zero |
| Reports deleted count | Yes | Yes | Yes | No |
| Extensible without a future signature change | Yes — new struct fields | No — new sibling method again | Yes — new option funcs | No |
| Semver bump required | MAJOR | MINOR | MAJOR | MAJOR |

## Decision

`authz.Engine.DeleteRelationsMatching`'s signature widens to `DeleteRelationsMatching(ctx context.Context, f RelationFilter, opts DeleteOptions) (DeletionProgress, error)`, where `DeleteOptions` is a required, concrete-typed (non-variadic) parameter whose zero value reproduces today's unbounded behavior.

## Consequences

**Consequences Positive**

- `DeleteOptions{}`'s zero value reproduces today's unbounded, single-transaction delete exactly — the method's *behavior* for every existing use case is unchanged, even though the call sites' source needs the new argument.
- `DeleteOptions.Limit == 0` meaning unbounded is the same value, for the same reason, as SpiceDB's own wire-level `OptionalLimit == 0` (per A4) — the Go-level default and the wire-level default are one convention, not two a reader has to reconcile.
- `*spicedb.Engine`'s implementation sets `OptionalLimit` from `opts.Limit` and `OptionalAllowPartialDeletions: opts.Limit > 0`, and never sets `OptionalCursor` — consistent with the CockroachDB-only constraint named in the scope note.
- The interface keeps exactly one delete-by-filter method, permanently — no second (or third, or fourth) method name for a caller to discover or choose between as this family of needs grows; `DeleteOptions` is where that growth lands instead.
- `DeletionProgress`'s field names mirror the wire response's `DeletionProgress` enum and `RelationshipsDeletedCount` field, so a reader tracing from the generated code to the wire protocol finds matching terminology at every layer.
- The method reuses `RelationFilter.IsEmpty()` / `ErrEmptyRelationFilter` rather than re-implementing the guard, so the all-empty-filter rejection behaves identically before and after this change.

**Consequences Negative**

- This decision supersedes `docs/scope-bounded-batch-relationship-delete.md`'s SC2 ("byte-identical... no existing call site or `Engine` implementer needs to change") and every other Success Criterion written against the non-breaking reading of the fork — SC1's grep target, SC4's framing, SC10's MINOR/`[1.16.0]` CHANGELOG framing all need revision to match a MAJOR bump before any job doc executes against the scope note.
- Every existing call site of `DeleteRelationsMatching` — today, none outside this repo are known (per A6) — must add `authz.DeleteOptions{}` as a third argument and handle a second return value; a caller who regenerates against a newer codegen binary without updating their own `pkg/authz` import gets a compile error, not a silent behavior change, which is the intended failure mode but is still a one-time required edit everywhere the method is called.
- This is the first deliberate breaking change to the `authz.Engine` interface in this project's history, and it departs from `ADR-003`'s established precedent of deferring such changes until a concrete need materializes (per A7) — every prior ADR (`ADR-001` through `ADR-005`) chose an additive path when one existed. It requires a MAJOR version bump and, per Go's semantic import versioning (per A3), a `/v2` suffix on the module's import path, paid now against a cost (interface-surface growth) that is currently hypothetical rather than observed.

## Assumptions

- **A1 [VERIFIED]:** The scope note, as originally drafted, required `DeleteRelationsMatching`'s signature to stay byte-identical — "no existing call site or `Engine` implementer needs to change to keep compiling." Evidence: `docs/scope-bounded-batch-relationship-delete.md`, Success Criterion 2 (superseded by this ADR's Decision).
- **A2 [EXTERNAL FACT]:** A Go type satisfies an interface only if its method set contains a method with the exact same name, parameter types (including variadic-ness), and return types as the interface's declared method — any signature change on either side breaks the assignment. Evidence: the Go language specification, "Interface types."
- **A3 [EXTERNAL FACT]:** Go's module system requires a new major-version import path suffix (e.g. `.../v2`) when a module makes an incompatible (MAJOR) change, per semantic import versioning. Evidence: https://go.dev/ref/mod#major-version-suffixes.
- **A4 [VERIFIED]:** `DeleteRelationshipsRequest.OptionalLimit == 0` means unlimited deletion; `OptionalLimit > 0` with `OptionalAllowPartialDeletions` bounds a call and reports via `DeletionProgress`/`RelationshipsDeletedCount`; `OptionalCursor`-based resume is supported only by SpiceDB's CockroachDB datastore backend. Evidence: `docs/scope-bounded-batch-relationship-delete.md` Assumptions A1-A3.
- **A5 [VERIFIED]:** This codebase already has a precedent for signaling extra outcome state through a typed/sentinel error rather than widening a return type — `authz.ConditionalPermissionError` / `authz.ErrConditionalPermission` on `CheckPermission`. Evidence: `pkg/authz/authz.go` (the `ConditionalPermissionError` type and its `Is` method).
- **A6 [VERIFIED]:** As of this ADR's drafting, this module has no confirmed external Go-module consumers. Evidence: `gh api repos/danhtran94/authzed-codegen` (6 stargazers, 2 forks) and a GitHub code search for `go.mod` files referencing "authzed-codegen" returning exactly one result — this repository's own `go.mod` — both queried live during this session (2026-11-13).
- **A7 [VERIFIED]:** `ADR-003` already faced an analogous fork for the wildcard read-side API (simple sibling method now vs. a richer return shape to avoid a hypothetical future breaking change) and chose the sibling-method path, explicitly accepting a possible future breaking change as a named Negative consequence rather than pre-paying it. This ADR's Decision weighs that precedent against the growing-interface-surface cost and reaches the opposite conclusion for this fork. Evidence: `docs/ADR-003-wildcard-read-side.md`, Decision, Consequences Negative (the `(bool, error)` → `(WildcardInfo, error)` migration risk), and Assumption A4 ("cheap dead code that can be removed without a breaking change").

## History

| Ticket    | Title                                                                               | Created | Status   |
|-----------|-------------------------------------------------------------------------------------|---------|----------|
| (initial) | Bounded Relationship Delete: Widening DeleteRelationsMatching over a Sibling Method |         | Accepted |
