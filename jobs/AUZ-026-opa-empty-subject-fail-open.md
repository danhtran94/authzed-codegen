<!-- approved -->
# AUZ-026: OPA builtins and engine Check fail closed on empty subject ID lists

| Field      | Value                                          |
|------------|------------------------------------------------|
| Status     | Done                                           |
| Created    | 2026-11-18                                     |
| Assignee   | Claude                                         |
| Source     | docs/spec-013-opa-go-builtins-codegen.md       |
| Blocked by | —                                              |

## Goal

`<pkg>.check_<res>_<perm>({"<type>": []}, id, ctx)` returns `false`, not `true`. A subject key whose ID list is empty is treated as absent, which matches the typed `Check<Perm>` method and spec-013's "every present key, non-empty subject slices" semantics.

As defense in depth, `*spicedb.Engine.CheckPermission`, `CheckPermissionWithCaveat` and `CheckPermissionUserset` return `authz.ErrNoInput` for an empty `audIDs` slice instead of `nil` (granted).

E2E tests against SpiceDB pin both behaviors. Ships in `v2.1.0` together with AUZ-025.

## Problem

Reproduced on 2026-11-18 against `main` with a folder that has **no tuples**:

```
extsvc.check_folder_browse({"extsvc/user": []},              "f", {}) -> allow = true      ← fail-open
extsvc.check_folder_browse({"extsvc/user": ["zz-stranger"]}, "f", {}) -> allow = undefined (denied)
```

The chain:

- `subjectEntries` (`internal/templates/opa.go.tmpl:225`) appends one entry per key, even when `idsFromTerm` returns zero IDs. The `len(entries) == 0` guard (`opa.go.tmpl:36`) therefore does not fire.
- The callback calls `engine.CheckPermission(..., e.IDs)` with zero IDs. The engine's `for _, id := range audIDs` loop body never runs, and the call returns `nil` (`pkg/authz/spicedb/crud.go:483`; the same pattern is at `:376` and `:650`).
- `checkOneGranted(nil)` maps `nil` to granted, so the builtin returns `true`.

A policy such as `allow if extsvc.check_folder_browse({"extsvc/user": input.user_ids}, ...)` grants access when `input.user_ids == []`. The code has been present since AUZ-022 (shipped in `v1.14.0`). Both the per-instance (`SpiceDBBuiltins`) and global (`RegisterSpiceDBBuiltinsGlobal`) registrations share the `checkImpl` / `subjectEntries` code (`opa.go.tmpl:170`, `:204`).

Typed `Check<Perm>` methods are **not** affected. They return `authz.ErrNoInput` when every slice is empty and guard each engine call with `if len(input.X) > 0`. Direct callers of the three engine `Check*` methods are affected.

Lookup builtins fail closed today (an empty `byIDs` yields no results), but they have a related correctness bug. With `{"a": [], "b": ["x"]}`, the empty key sorts first, wins "first present key", and returns `[]`.

## Solution

    Before:
      subjectEntries: {"extsvc/user": []} → [{user, []}]   → guard skipped → engine loop over 0 IDs → nil → true
    After:
      subjectEntries: {"extsvc/user": []} → []             → guard fires   → false (check) / [] (lookup)
      engine Check*:  audIDs == []        → authz.ErrNoInput (never reached from builtins after the filter)

### Components

**`subjectEntries`** (`internal/templates/opa.go.tmpl`)
- Skip keys whose ID list is empty. A single change point covers the check and lookup callbacks in both registration variants.

**Engine `Check*`** (`pkg/authz/spicedb/crud.go`)
- `CheckPermission`, `CheckPermissionWithCaveat`, `CheckPermissionUserset`: `if len(audIDs) == 0 { return authz.ErrNoInput }` as the first statement.

**`authz.Engine` interface doc** (`pkg/authz/authz.go`)
- State the empty-`audIDs` contract on the three `Check*` methods so other `Engine` implementations follow it.

### Why not alternatives

| Approach | Verdict |
|---|---|
| **Filter empty keys in `subjectEntries` + engine guard (chosen)** | One template change point; semantics match the typed method and spec-013; engine guard protects direct callers |
| Reject the whole call if *any* key is empty | Stricter than the typed method, which skips empty slices; would make `{"user": [], "group": ["g"]}` behave differently in Rego and Go |
| Engine guard only | Turns the builtin's `true` into a fatal Rego eval error (`checkOneGranted` treats `ErrNoInput` as fatal), not a policy `false` |
| Template filter only | Leaves `Engine.CheckPermission(..., nil)` returning granted for hand-written callers |

## Workstreams

### 1. Engine guard

| # | Task | File | Status |
|---|------|------|--------|
| 1.1 | Return `authz.ErrNoInput` on `len(audIDs) == 0` at the top of `CheckPermission`, `CheckPermissionWithCaveat`, `CheckPermissionUserset` | `pkg/authz/spicedb/crud.go` | [x] |
| 1.2 | Document the empty-`audIDs` contract on the three interface methods | `pkg/authz/authz.go` | [x] |

### 2. Template filter

| # | Task | File | Status |
|---|------|------|--------|
| 2.1 | In `subjectEntries`, skip keys whose `idsFromTerm` result is empty; update the helper's doc comment | `internal/templates/opa.go.tmpl` | [x] |
| 2.2 | Regenerate: `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed` | `example/authzed/**/opa.gen.go` | [x] |
| 2.3 | Confirm the diff touches only the `subjectEntries` body in each `opa.gen.go` | same | [x] |

### 3. Tests (e2e against SpiceDB)

| # | Task | File | Status |
|---|------|------|--------|
| 3.1 | `TestEngine_Check_EmptyIDs_ReturnsErrNoInput`: all three `Check*` methods return `authz.ErrNoInput` for `nil` and `[]authz.ID{}` | `pkg/authz/spicedb/spicedb_test.go` | [x] |
| 3.2 | `TestOPA_CheckFolderBrowse_EmptySubjectIDs_Denies`: `{"extsvc/user": []}` on a folder with no tuples → `false` | `example/authzed/extsvc/extsvc_opa_test.go` | [x] |
| 3.3 | `TestOPA_CheckFolderBrowse_EmptyKeyIgnored`: `{"extsvc/group": [], "extsvc/user": ["u"]}` where only the user is a viewer → `true` (empty key treated as absent, matching typed `CheckBrowse`) | same | [x] |
| 3.4 | `TestOPA_LookupFolderBrowse_EmptyKeyIgnored`: `{"extsvc/group": [], "extsvc/user": ["u"]}`. `extsvc/group` sorts first, so before the fix the empty key wins "first present key" and returns `[]`; after the fix the user's folder is returned. | same | [x] |

### 4. Verification

| # | Task | Status |
|---|------|--------|
| 4.1 | `go build ./...`, `go vet ./...`, `gofmt -l .`, `go mod tidy` clean | [x] |
| 4.2 | Round-trip regeneration is zero-diff | [x] |
| 4.3 | CI-pinned `golangci-lint` v2.15.0 and `govulncheck` v1.1.4 clean | [x] |
| 4.4 | Full `go test ./... -count=1` passes, including e2e | [x] |

### 5. Documentation

| # | Task | File | Status |
|---|------|------|--------|
| 5.1 | `## [2.1.0] - Unreleased` → `### Security` entry: affected versions (`v1.14.0`–`v2.0.0`), trigger, fix, and an upgrade instruction to regenerate `opa.gen.go` | `CHANGELOG.md` | [x] |

## Design Decisions

### Empty key = absent key

spec-013 (`docs/spec-013-opa-go-builtins-codegen.md:150-152`) defines builtin Check semantics as AND across present keys, matching the typed method, "which loops over non-empty subject slices". The typed method skips empty slices. Filtering empty keys aligns the builtin with that written contract; the previous behavior was an implementation divergence, not a design choice.

### `ErrNoInput`, not `ErrPermissionDenied`, from the engine

An empty subject list is a caller error, not a policy outcome. `authz.ErrNoInput` already carries that meaning for typed `Check<Perm>` methods. Callers that treat any non-nil error as deny are fail-closed either way.

## What Stays Unchanged

- Typed `Check<Perm>` / `Lookup*` methods and their signatures: not affected; no template change in `object.go.tmpl`.
- Builtin signatures, arity and the AND-across-keys / first-present-key-for-lookup rules.
- `idsFromTerm`, `checkOneGranted`, `termToStructpb`.
- Engine `Lookup*`, `Create*`, `Delete*`, `Read*` methods.

## Implementation Order

    1. Workstream 1 (engine guard)      ← independent of the template; lands first so 3.1 has something to pin
    2. Workstream 2 (template + regen)  ← generated opa.gen.go must exist before 3.2–3.4 compile against it
    3. Workstream 3 (tests)             ← exercises WS1 + WS2 output
    4. Workstream 4 (verification)      ← gates close over WS1–3
    5. Workstream 5 (CHANGELOG)         ← describes the verified fix

## Notes

- Run 3.2 against the pre-fix template first: it must fail (`allow == true`). That proves the test catches the regression.
- `RegisterSpiceDBBuiltinsGlobal` can only be registered once per process (see `TestOPA_GlobalRegistration`). The global variant is covered structurally, because it renders the same `checkImpl` / `subjectEntries` template code, rather than by a second global-registration test.
- After merge, consider a GitHub Security Advisory for `v1.14.0`–`v2.0.0` (owner decision; outside this job).

## Discoveries & Decisions During Implementation

### [Implementer] Regression proven before the fix

Tests were written first and run against the unfixed code. `TestOPA_CheckFolderBrowse_EmptySubjectIDs_Denies` failed with `expected: false, actual: true`, and `TestOPA_LookupFolderBrowse_EmptyKeyIgnored` failed with `actual: []interface {}{}`. `TestEngine_Check_EmptyIDs_ReturnsErrNoInput` failed six times (3 methods × `nil` / `[]`) with `got nil`. `TestOPA_CheckFolderBrowse_EmptyKeyIgnored` passed both before and after the fix. It pins the intended "empty key = absent" semantics; before the fix that result was only incidentally correct, because the empty group entry was vacuously granted.

### [Implementer] README documents the builtin subject contract

`README.md`'s OPA section states the AND-across-present-keys rule, so it gained one sentence on empty id lists. The job doc had not listed this task; it was added during implementation.

### [Reviewer] Verification summary

`go build`, `go vet`, `gofmt -l .` (empty), `go mod tidy` (no diff), regeneration round-trip (zero diff), CI-pinned `golangci-lint` v2.15.0 (0 issues) and `govulncheck` v1.1.4 (none), and `go test ./... -count=1` (all packages pass, including the 4 new tests against the SpiceDB testcontainer). The generated diff is limited to the `subjectEntries` body and doc comment in the three `opa.gen.go` files.
