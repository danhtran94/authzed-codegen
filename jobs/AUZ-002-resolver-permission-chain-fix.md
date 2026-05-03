# AUZ-002: Fix resolver permission-chain type propagation

<!-- approved -->

| Field      | Value                                                  |
|------------|--------------------------------------------------------|
| Status     | Done                                                   |
| Created    | 2026-05-03                                             |
| Assignee   | Danh Tran                                              |
| Source     | docs/scope-resolver-permission-chain-fix.md            |
| Blocked by | —                                                      |

## Goal

Replace the two-pass `relationResolver` + `addTree` algorithm in `internal/generator/generator.go:GetPermissionTree` with a single recursive memoized resolver that walks both relation **and** permission contributions, with cycle detection. After this job, `extsvc/document.admin` includes `Role`, `bookingsvc/employee.view` includes `Brand`, every other affected fixture is regenerated and committed in the same change, and a self-referential schema (`permission x = x + y`) terminates with a non-zero exit and `cycle detected` in stderr.

## Problem

Current behavior — arrow contributions inside a referenced permission are dropped:

    permission view = parent->browse        // contributes via arrow → folder.browse
    permission edit = owner + parent->browse
    permission admin = view + edit          // ← fails to inherit Role from view's arrow

    GetPermissionTree pass 1:
      relationResolver["document/view"]  = nil       ✗  (view has no relation contributions)
      relationResolver["document/edit"]  = [User, Group]  (only owner, not parent->browse)
      relationResolver["document/admin"] = nil

    GetPermissionTree pass 2 for admin:
      addTree("admin", relationResolver["document/view"])  → adds nothing
      addTree("admin", relationResolver["document/edit"])  → adds [User, Group]
      tree["document/admin"] = [User, Group]   ← Role missing

## Solution: Recursive memoized resolver with cycle detection

    After fix:
      resolveTransitive(def, perm, visited):
        if cached → return cached            ← memoization
        if visited[key] → cycle error         ← cycle detection
        push visited[key]
        for each entry in perm:
          if Kind=="relation":   types += entry.Types
          if Kind=="permission": types += resolveTransitive(entry.Types[0], entry.Value, visited)
        pop visited[key]
        cache & return dedup(types)

      tree[def/perm] = resolveTransitive(def, perm, {}) for every (def, perm)

### Components

**`resolveTransitive(definition, permission, visited)`** — new recursive function in `generator.go`
- Returns the full type set reachable from a permission, walking both relation entries and permission references
- Memoizes resolved (definition, permission) pairs in a separate cache map
- Tracks current recursion stack in `visited` to detect cycles; pops on exit so diamond-shaped DAGs don't false-positive

**`GetPermissionTree() (map[string][]string, error)`** — signature change
- Returns error when a cycle is detected; error message contains `cycle detected at <prefix>/<name>/<permission>`
- Caller (`GenerateObjectSource`) propagates up to `main.go`, which panics → non-zero exit

### Why not alternatives

| Approach | Verdict |
|---|---|
| **Recursive memoized eager-resolve (chosen)** | One pass; memoization keeps it linear in (definitions × permissions); cycle detection is a single visited-set check |
| Two-pass with iterative fixed-point | Also correct but harder to write cycle detection — needs a separate dependency-graph walk |
| Lazy resolve at template call time | Defers complexity into template funcs; harder to test; unclear ownership of cycle detection |

## Workstreams

### 1. Resolver rewrite

Replace the relationResolver + addTree algorithm with a single recursive resolver. Build stays green because the only consumer (`GenerateObjectSource`) is updated in the same workstream.

| # | Task | File | Status |
|---|------|------|--------|
| 1.1 | Change `GetPermissionTree` signature from `() map[string][]string` to `() (map[string][]string, error)` | `internal/generator/generator.go` | [x] |
| 1.2 | Implement `resolveTransitive(d DefinitionsByTypes, defType, perm string, visited, cache map[string]bool) ([]string, error)` — recursive walker with cycle detection on `visited` and memoization on `cache` | same | [x] |
| 1.3 | Replace the existing `relationResolver` + `addTree` body of `GetPermissionTree` with a single loop calling `resolveTransitive` per `(defType, perm)` | same | [x] |
| 1.4 | Update `GenerateObjectSource` to capture the error from `GetPermissionTree` and return it; `main.go` already panics on errors | same | [x] |
| 1.5 | Remove the now-unused `addTree` closure (no other callers) | same | [x] |

**Key details:**
- Cycle key is `prefix/name/permission` (e.g. `extsvc/document/admin`).
- Cycle error format: `fmt.Errorf("cycle detected at %s", key)` — SC3 grep checks for the literal `cycle detected` substring.
- Memoization cache stores the deduplicated type slice. The `visited` set is the current recursion stack; `cache` survives across all top-level calls.
- `resolveTransitive` must dedupe its return value before caching — every entry should appear at most once per `(defType, perm)`.
- The current `addTree`'s seen map keyed by `treename/type` is replaced by the per-resolveTransitive dedup; same observable behavior, no double-emit.

### 2. Cycle detection verification

Hand-craft a cyclic schema; confirm the resolver terminates with the expected error. No committed test file (per scope: no `*_test.go` framework added).

| # | Task | File | Status |
|---|------|------|--------|
| 2.1 | Write `/tmp/auz-002-cycle.zed` with `definition test/x { permission p = p + q; permission q = p }`; run `go run ./cmd/authzed-codegen --output /tmp/auz-002-out /tmp/auz-002-cycle.zed`; confirm exit non-zero AND stderr contains `cycle detected` | (transient — not committed) | [x] |

**Key details:**
- The schema is intentionally not added to `example/schema.zed` — the example fixture must always succeed; cycle is a rejection-path test only.
- Capture both stdout and stderr; the panic message lands on stderr.

### 3. Regenerate fixtures

Per scope SC6, all `.gen.go` files that diff after the resolver fix must be committed in the same change as the resolver code. Inspect each diff and confirm it is additive (new fields, no removals).

| # | Task | File | Status |
|---|------|------|--------|
| 3.1 | Run `go run ./cmd/authzed-codegen --output example/authzed example/schema.zed` | (codegen run) | [x] |
| 3.2 | `git diff --stat example/authzed/` — capture the file list; confirm `extsvc/document.gen.go` and `bookingsvc/employee.gen.go` are present (SC1, SC2) | (verification step) | [x] |
| 3.3 | For each changed file, inspect the diff and confirm: only field additions inside `Check<X>Inputs` structs and inside the deduped `relationExpressionTypes`-driven blocks; no removals, no struct deletions | (verification step) | [x] |
| 3.4 | Run `go build ./example/...` — every regenerated file must compile (SC4) | (verification step) | [x] |

**Key details:**
- Likely affected files (predicted from schema analysis): `bookingsvc/booking.gen.go` (write inherits Brand via creator->manage chain), `bookingsvc/employee.gen.go` (view gains Brand), `extsvc/document.gen.go` (admin gains Role). Other files may diff if their permissions transitively traverse arrow chains; record the actual list during execution.
- Any regression — a field that disappears, a struct that breaks — is a bug in the resolver, not an expected diff. Halt and investigate.

### 4. Documentation

Per scope risk #3, surface the behavior change so downstream callers understand what changed.

| # | Task | File | Status |
|---|------|------|--------|
| 4.1 | Update `README.md` "TODOs" section: remove the resolver-bug TODO; add a one-line note under "Schema parser" describing what `Check<Permission>Inputs` now contains (full transitively-reachable input types) | `README.md` | [x] |

### 5. Verification

| # | Task | Status |
|---|------|--------|
| 5.1 | `go build ./...` exits 0 | [x] |
| 5.2 | `go vet ./...` exits 0 (SC5) | [x] |
| 5.3 | Round-trip: `go run ./cmd/authzed-codegen --output example/authzed example/schema.zed && git diff --quiet example/authzed/` exits 0 (after the regeneration commit, the working tree is clean) | [x] |
| 5.4 | SC1 grep check: `grep -A 4 "type CheckDocumentAdminInputs struct" example/authzed/extsvc/document.gen.go` shows the three fields `User`, `Group`, `Role` | [x] |
| 5.5 | SC2 grep check: `grep -A 3 "type CheckEmployeeViewInputs struct" example/authzed/bookingsvc/employee.gen.go` shows the two fields `User` and `Employee` (scope predicted `Brand` but a re-trace during execution confirmed the correct chain — see Discoveries) | [x] |

## Design Decisions

### Recursive memoized eager-resolve over lazy-walk
Codegen runs at build time on schemas with O(10s) of definitions; performance is not load-bearing. Eager resolution computes the full type set once per (definition, permission) and caches it; the resolver never recomputes. Lazy-walk would defer resolution into template funcs, splitting cycle-detection ownership across the resolver and the template — harder to reason about, harder to surface a useful error from. Rules out the alternative of computing types on every template `permissionInputTypes` call.

### Cycle detection in the resolver, not in the parser
SpiceDB's `compiler.Compile` accepts cyclic permission definitions (per scope A3) — cycle handling lives at evaluation time on the engine side. The codegen has no engine; if it doesn't detect cycles itself, it infinite-loops. The chosen approach (visited-set on the recursion stack) is the minimum viable mechanism.

### Signature change to `GetPermissionTree` rather than panic-on-cycle
Returning an error keeps the failure path explicit and lets the caller decide how to surface it. `main.go` already panics on errors from `GenerateObjectSource`, so the user-facing behavior (non-zero exit, message on stderr) is unchanged. Rules out the alternative of panicking inside `GetPermissionTree` — would work but hides the failure mode from the type system.

## What Stays Unchanged

- `internal/generator/adapter.go` — proto → view conversion is untouched
- `internal/templates/object.go.tmpl` — template iterates over `permissionInputTypes` output unchanged; the new resolver returns the same shape (`map[string][]string`)
- `internal/utilstr/` — string-mangling helpers
- `pkg/authz/` and `pkg/authz/spicedb/` — no runtime change (per scope out-of-scope item #2)
- `cmd/authzed-codegen/main.go` — already panics on errors; no signature change needed
- `example/schema.zed` — input fixture stays as-is; only generated output diffs
- `internal/generator/generator.go` — `Generator`, `NewGenerator`, `AddObjectTemplate`, `Permission`, `Relation`, `DefinitionsByTypes`, `ParseDefinitions`, `resolvePermissionExpressionTypes` are all untouched. Only `GetPermissionTree` changes signature and body.

## Implementation Order

    1. WS1 Resolver rewrite     ← single atomic edit; build green after
    2. WS2 Cycle test           ← depends on WS1; one-off shell command
    3. WS3 Regenerate fixtures  ← depends on WS1
    4. WS4 README               ← depends on WS3 (so the doc reflects actual fixture diff)
    5. WS5 Final verification   ← depends on WS3 + WS4

WS1 is one atomic edit (the function is small; intermediate states leave the file uncompilable). WS2 and WS3 are independent verifications; can be parallel after WS1.

## Notes

- Cycle key format: `<prefix>/<name>/<permission>` (e.g. `extsvc/document/admin`). The `cycle detected at <key>` error format is checked by SC3.
- `addTree` closure has a single caller (`GetPermissionTree`'s pass 2 loop). After WS1, both are gone; no orphan code left behind.
- The pre-existing `Permissions.IsEmpty()` and `relationResolver` early-return for empty-permission definitions (`tree[t] = []string{}`) needs to be preserved in the new code path — every definition must have an entry in the returned map even if it has no permissions.
- Per scope A4 — no `*_test.go` files added. The cycle test (WS2.1) is verified via shell, results captured in Discoveries, no committed artifact.

## Discoveries & Decisions During Implementation

### [Implementer] Scope SC2 named the wrong field — Employee, not Brand
The scope predicted that `bookingsvc/employee.view` would gain `Brand` after the fix. The actual added field is `Employee`. Re-tracing the chain: `view = manage + viewer`; `manage = account + belongs_brand->manage`; `belongs_brand->manage` resolves to `brand.manage = manager + admin` whose input types are `Employee` (the manager relation) and `User` (the admin relation). The `Brand` is the resource being checked, not an input type — so it was never expected to appear in `Check<X>Inputs`. The chain *was* dropping types pre-fix; just not the type the scope claimed. Job's SC2 row updated to reflect actual behavior; the bug-fix value (the chain now propagates) is unchanged.

### [Implementer] Fixture diff includes a one-time normalization in `bookingsvc/brand.gen.go`
The pre-fix `relationResolver` was a `map[string][]string`; populating order was non-deterministic per build. The committed `bookingsvc/brand.gen.go` happened to be generated with one specific Go map-iteration seed, producing `Employee, User` field order in `CheckBrandCreateBookingInputs`'s callers — but the new resolver walks the `Permissions` slice in source order (deterministic), producing the opposite field order. The diff is purely reordering — same field SET, no additions, no removals. Worth noting for future regeneration audits: the new order is locked in and should remain stable across builds.

### [Implementer] `addTree` closure was only inside `GetPermissionTree`; cleanly removed
Task 1.5 confirmed `addTree` had a single caller (the second pass loop inside the old `GetPermissionTree`). Replacing the body removed both. No orphan code; no other reference grep needed.

### [Implementer] Cycle test required a 5+ char object name and 3+ char permission names
SpiceDB validates object types against `^([a-z][a-z0-9_]{1,62}[a-z0-9]/)*[a-z][a-z0-9_]{1,62}[a-z0-9]$` and permissions against `^[a-z][a-z0-9_]{1,62}[a-z0-9]$` (minimum 3 chars). The first two cycle-test schemas (`test/x` with `permission p`) failed at compile time with regex errors; final working schema used `testsvc/doc` with `permission perm_a`. The cycle detection itself fired correctly: `panic: cycle detected at testsvc/doc/perm_a` on exit code 2. Worth remembering when hand-crafting test schemas.
