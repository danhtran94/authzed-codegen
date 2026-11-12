<!-- approved -->
# AUZ-024: Bounded relationship delete (first MAJOR release, v2)

| Field      | Value                                                        |
|------------|---------------------------------------------------------------|
| Status     | Done                                                            |
| Created    | 2026-11-13                                                     |
| Assignee   | Claude                                                          |
| Source     | docs/ADR-006-bounded-relationship-delete-widened-signature.md  |
| Blocked by | —                                                                |

## Goal

Widen `authz.Engine.DeleteRelationsMatching` from `(ctx, f RelationFilter) error` to `(ctx, f RelationFilter, opts DeleteOptions) (DeletionProgress, error)` per `ADR-006`, so a caller can bound a filter-delete to N relationships per call instead of hitting SpiceDB's single-transaction timeout on huge objects. This is the project's first breaking change: the module path moves to `github.com/danhtran94/authzed-codegen/v2`, every internal import updates, `internal/templates/object.go.tmpl`'s three `DeleteRelationsMatching` call sites gain the new argument, and `example/authzed/**/*.gen.go` regenerates across ~100 call sites.

## Problem

`pkg/authz/authz.go:229`'s `DeleteRelationsMatching(ctx, f RelationFilter) error` always issues `OptionalLimit: 0` — one unbounded transaction. An object with millions of tuples hits SpiceDB's single-transaction timeout, and nothing in `authz.Engine` lets a caller bound the call or learn whether a partial delete completed (`docs/scope-bounded-batch-relationship-delete.md`, Problem).

## Solution: Widen the existing method, don't add a sibling

`ADR-006` rejected a new sibling method (`DeleteRelationsMatchingBounded`) because every future filter-delete variant would then be a candidate for a third, fourth method on the same interface — this widening absorbs that growth into `DeleteOptions` instead, at the one-time cost of a breaking change while this module has zero confirmed external consumers (`ADR-006` A6).

    Before:
      DeleteRelationsMatching(ctx, f) error
        → OptionalLimit: 0 always, one transaction, no progress signal

    After:
      DeleteRelationsMatching(ctx, f, opts DeleteOptions) (DeletionProgress, error)
        → opts.Limit == 0 (zero value): OptionalLimit: 0, same as before
        → opts.Limit  > 0: OptionalLimit: opts.Limit, OptionalAllowPartialDeletions: true
        → DeletionProgress{Deleted, Complete} reports the outcome either way
        → OptionalCursor is never set (CockroachDB-only upstream; not implemented, per scope Out of Scope)

### Components

**`authz.DeleteOptions`** (new type, `pkg/authz/authz.go`)
- `Limit uint32` — 0 means unbounded (today's behavior)

**`authz.DeletionProgress`** (new type, `pkg/authz/authz.go`)
- `Deleted uint64`, `Complete bool` — mirrors the wire response's `RelationshipsDeletedCount` / `DeletionProgress`

**`authz.Engine.DeleteRelationsMatching`** (widened interface method, `pkg/authz/authz.go`)
- Breaking signature change — see Solution above

**`*spicedb.Engine.DeleteRelationsMatching`** (widened implementation, `pkg/authz/spicedb/crud.go`)
- Builds the wire request from `opts`, never sets `OptionalCursor`, maps the response into `DeletionProgress`

**`internal/templates/object.go.tmpl`** (3 call sites)
- `Purge<Rel>Relations`, `PurgeRelations`, `PurgeRelationsAsSubject` each pass `authz.DeleteOptions{}` (zero value — stay unbounded, per scope Out of Scope) and discard the `DeletionProgress` return

### Why not alternatives

| Approach | Verdict |
|---|---|
| **Widen `DeleteRelationsMatching` in place (chosen)** | One method, permanently; breaking change paid once now while adoption is zero |
| New sibling `DeleteRelationsMatchingBounded` | Non-breaking, but the interface grows a method per future filter-delete variant — rejected per `ADR-006` |
| Variadic functional options (`opts ...DeleteOption`) | Avoids widening the param list visibly, but hides dispatch behind opaque closures for a single integer — rejected as "magic" |
| Sentinel error + variadic limit | Keeps `error`-only return, but still breaks the signature (variadic param) and drops the deleted-count signal entirely — rejected |

## Workstreams

### 1. `pkg/authz/` — types and interface

One sentence: adds `DeleteOptions`/`DeletionProgress` and widens the `Engine` interface — nothing here compiles against `*spicedb.Engine` until Workstream 2 lands in the same change.

| # | Task | File | Status |
|---|------|------|--------|
| 1.1 | Add `DeleteOptions` struct (`Limit uint32`, 0 = unbounded) | `pkg/authz/authz.go` | [x] |
| 1.2 | Add `DeletionProgress` struct (`Deleted uint64`, `Complete bool`) | `pkg/authz/authz.go` | [x] |
| 1.3 | Widen `Engine.DeleteRelationsMatching` to `(ctx, f RelationFilter, opts DeleteOptions) (DeletionProgress, error)`; rewrite its doc comment per `ADR-006` Consequences (zero-value semantics, no cursor) | same | [x] |

**Key details:** `DeleteOptions{}` must reproduce today's exact wire behavior (`OptionalLimit: 0`) — this is the load-bearing compatibility guarantee from `ADR-006`'s Consequences Positive, even though the Go signature itself is breaking.

### 2. `pkg/authz/spicedb/` — implementation

One sentence: makes `*spicedb.Engine` satisfy the widened interface; the `var _ authz.Engine = &Engine{}` assertion in this file is the build-time proof this workstream is complete and correct.

| # | Task | File | Status |
|---|------|------|--------|
| 2.1 | Update `DeleteRelationsMatching`: set `OptionalLimit: opts.Limit`, `OptionalAllowPartialDeletions: opts.Limit > 0`, never set `OptionalCursor`; keep the existing `f.IsEmpty()` / `ErrEmptyRelationFilter` guard unchanged | `pkg/authz/spicedb/crud.go` | [x] |
| 2.2 | Map the wire response into `authz.DeletionProgress{Deleted: res.RelationshipsDeletedCount, Complete: res.DeletionProgress != v1.DeleteRelationshipsResponse_DELETION_PROGRESS_PARTIAL}` | same | [x] |

**Key details:** `res.DeletionProgress` is `UNSPECIFIED` on servers/paths that never set it (e.g. older behavior) — treating "not PARTIAL" as complete (rather than requiring the literal `COMPLETE` value) is the defensive default that keeps `opts.Limit == 0` callers behaving exactly as before.

### 3. `internal/templates/` — generated call sites

One sentence: makes the codegen's own output compile against the widened interface — this is the step scope SC8/Risk flagged as easy to miss.

| # | Task | File | Status |
|---|------|------|--------|
| 3.1 | Update all 3 `DeleteRelationsMatching` call sites (lines ~137, 151, 166) to pass `authz.DeleteOptions{}` and discard the `DeletionProgress` return, keeping each generated method's external signature as plain `error` | `internal/templates/object.go.tmpl` | [x] |

**Key details:** `grep -c "DeleteRelationsMatching(ctx" internal/templates/object.go.tmpl` must stay `3`; `grep -c "DeleteOptions{}" internal/templates/object.go.tmpl` must become `3` (scope SC8).

### 4. Module path — first MAJOR bump

One sentence: moves the module to `/v2` per Go's semantic import versioning so a future `v2.0.0` tag is resolvable at all; touches every file with an internal import, so it runs after Workstreams 1-3 are correct, in one atomic pass.

| # | Task | File | Status |
|---|------|------|--------|
| 4.1 | Change module path to `github.com/danhtran94/authzed-codegen/v2` | `go.mod` | [x] |
| 4.2 | Update every internal import (`pkg/authz`, `internal/*`, `cmd/*`) to the `/v2` path | all `.go` files importing this module | [x] |
| 4.3 | Update `go install` instructions | `README.md` | [x] |

**Key details:** Do this as one atomic edit (grep-and-replace the old module path across the tree, then build) — an intermediate state where only some files use `/v2` does not compile, matching the HARNESS-013 precedent for atomic multi-file edits.

### 5. Regenerate fixtures

| # | Task | File | Status |
|---|------|------|--------|
| 5.1 | Regenerate: `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed` | `example/authzed/**/*.gen.go` | [x] |
| 5.2 | Confirm the only diff is the mechanical `authz.DeleteOptions{}` addition at existing `DeleteRelationsMatching` call sites (no other behavioral change) | same | [x] |

### 6. Testing

| # | Task | Status |
|---|------|--------|
| 6.1 | New e2e test: write more tuples than a chosen limit, call `DeleteRelationsMatching` with `DeleteOptions{Limit: n}`, assert `Deleted == n` and `Complete == false`; loop until `Complete == true` | [x] |
| 6.2 | `go build ./...`, `go vet ./...`, `gofmt -l .` all clean | [x] |
| 6.3 | `golangci-lint run ./...` and `govulncheck ./...` clean | [x] |
| 6.4 | Full `go test ./...` passes, including e2e-against-SpiceDB testcontainers | [x] |

### 7. Documentation

| # | Task | Status |
|---|------|--------|
| 7.1 | `CHANGELOG.md`: new `[2.0.0]` entry — breaking signature change, `/v2` import path | [x] |
| 7.2 | `README.md`: "Relationship Cleanup" section gains a subsection on bounded delete; states explicitly that cursor-resume is CockroachDB-only and not implemented | [x] |

## Design Decisions

### `DeleteOptions{}` zero value must be behavior-identical to the pre-ADR-006 call

The entire "this is a safe breaking change" argument in `ADR-006` rests on `opts.Limit == 0` producing the exact same wire request as today. Task 2.1's `OptionalAllowPartialDeletions: opts.Limit > 0` is the specific line that must hold this invariant — reviewed explicitly in Workstream 2, not assumed.

### No cursor, ever

`OptionalCursor` is never set by this implementation (per scope Out of Scope — CockroachDB-only upstream, no e2e coverage in this repo). Task 2.1 is a hard constraint, not an oversight to fix later.

## What Stays Unchanged

- The generated `Purge<Rel>Relations` / `PurgeRelations` / `PurgeRelationsAsSubject` methods' own external signatures (still plain `error`) and their unbounded behavior (`DeleteOptions{}`).
- `DeleteRelations` (targeted per-ID delete) and the generated `Delete<Rel>Relations` — untouched, unrelated to this fork.
- Cursor-resume, a loop-until-complete helper, `OptionalPreconditions`, and any new `RelationshipFilter` fields — all explicitly out of scope per the scope note.
- `RelationFilter` and `ErrEmptyRelationFilter` — reused as-is.

## Implementation Order

    1. Workstream 1 (pkg/authz/ types)        ← must land with WS2 in one change (interface + impl together)
    2. Workstream 2 (spicedb/ implementation)  ← pairs with WS1; var _ authz.Engine assertion proves correctness
    3. Workstream 3 (template call sites)      ← depends on WS1/2's final signature
    4. Workstream 4 (module path /v2)          ← depends on WS1-3 being correct; touches every file once
    5. Workstream 5 (regenerate fixtures)       ← depends on WS3 + WS4
    6. Workstream 6 (testing)                   ← runs throughout, gates close
    7. Workstream 7 (docs)                      ← last, describes the finished state

## Notes

- This is the project's first MAJOR version. Future breaking changes should batch with this one if any are already known — none are, per `ADR-006` Consequences.
- `pkg/authz/spicedb/crud.go`'s `var _ authz.Engine = &Engine{}` assertion is the single most useful check in this job — if Workstream 1 and 2 ever drift, this line stops compiling immediately.

## Discoveries & Decisions During Implementation

### [Implementer] `DeleteRelationsMatching` had hand-written callers the job doc didn't name

Workstream 3 only planned for `internal/templates/object.go.tmpl`'s 3 call sites, but `example/authzed/extsvc/extsvc_purge_test.go` (AUZ-023's hand-written e2e tests) calls `DeleteRelationsMatching` directly 5 times to exercise the raw primitive. These don't regenerate — they needed manual edits (`_, err := ...` + `authz.DeleteOptions{}`) alongside the template change, caught only because `go build` on the whole tree surfaced them before WS3 was marked done. Scope note A8 already named the template's 3 call sites and the ~100 generated ones; it did not anticipate hand-written direct callers outside the template. Pattern for next time: `grep -rn "DeleteRelationsMatching(ctx" --include="*.go" .` (not scoped to templates) is the complete call-site inventory, not just the template grep.

### [Implementer] IDE/gopls diagnostics lagged real compiler state by several edits

After the module path rename (WS4), the editor surfaced `BrokenImport` and stale-signature errors against files that `go build ./...` had already confirmed correct — gopls's index hadn't caught up with the mid-session `module` directive change. Treated the live `go build`/`go vet` output as authoritative throughout rather than the IDE diagnostics; no actual code defect behind any of those messages.

### [Implementer] Round-trip determinism check needs matching output-dir basenames

Re-confirmed a session-wide pattern: `SchemaView.PackageName` derives from `filepath.Base(g.OutputPath)` (`internal/generator/generator.go`), so comparing two regeneration runs into differently-named temp directories always shows a 1-line `schema.gen.go` package-name diff that looks like a determinism bug but isn't. The correct check regenerates into two directories sharing the same basename (e.g. `/tmp/a/authzed` and `/tmp/b/authzed`) — confirmed byte-identical output with the fix path applied.

### [Reviewer] No issues found

Full sweep after all workstreams: `go build`, `go vet`, `gofmt -l .`, `golangci-lint run ./...` (0 issues), `govulncheck ./...` (no vulnerabilities), `go test ./...` (all packages pass, including the two new bounded-delete e2e tests and the previously-existing AUZ-023 purge suite against the widened signature).
