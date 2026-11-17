<!-- approved -->
# AUZ-025: Flatten every branch of nested permission rewrites (v2.1.0)

| Field      | Value                                          |
|------------|------------------------------------------------|
| Status     | Done                                           |
| Created    | 2026-11-18                                     |
| Assignee   | Claude                                         |
| Source     | docs/spec-001-intersection-exclusion-codegen.md |
| Blocked by | —                                              |

## Goal

`lowerSetOperationChild` (`internal/generator/adapter.go`) returns **every** `PermissionExpr` produced by a nested `UsersetRewrite` child instead of only `exprs[0]`. After the fix, a permission such as `(viewer + tenanted_viewer + collab) - nested_banned` produces generated `Check<Perm>Inputs` / `Lookup*` / OPA builtin signatures that carry every subject type, userset field and caveat-context field reachable through any branch of the nested union. This matches what the flat form `viewer + tenanted_viewer + collab` already produces.

The fixture `example/schema.zed` gains such a nested permission on `extsvc/folder`. New e2e tests in `example/authzed/extsvc/extsvc_test.go` prove that SpiceDB grants through the previously dropped branches, and that the generated stub can now express those checks. The release is `v2.1.0` (originally planned as `v2.0.1`; see Discoveries).

## Problem

`internal/generator/adapter.go:639-646` (the `*core.SetOperation_Child_UsersetRewrite` case) recurses into the nested rewrite and then keeps `exprs[0]`:

```go
if len(exprs) > 0 {
    return exprs[0], nil
}
```

The behavior traces to `docs/spec-001-intersection-exclusion-codegen.md:96-99`, which assumed "the child produces exactly one expression per computed userset or arrow". That assumption is false. SpiceDB only emits a nested `UsersetRewrite` when an operator mixes with another operator (e.g. `(a + b) - c`), and such a node always has ≥2 children. The scope note's Success Criterion 3 required a fixture "nested inside other rewrites". That fixture was never created, so the round-trip check never exercised this path.

Reproduced on 2026-11-18 against `v2` HEAD:

- **Lost direct type.** Schema `nested = (viewer + bot + gated) - banned`, with `bot: tst/bot` and `gated: tst/user with tst/in_tenant`. The generated `CheckDocNestedInputs` is `{ User []User }`, while the flat equivalent is `{ User []User; Bot []Bot; Caveats ... }`. `Bot` and the caveat context are both dropped.
- **SpiceDB grants the lost branch.** E2E against a SpiceDB container (`spicedbtest.Start`) with `nested = (viewer + editor) - banned`, `editor: tst/team#member`. `CheckPermissionUserset(..., "tst/team", ["t1"], "member", nil)` returns GRANTED. The generated stub has no field to ask that question.

- **The WS3 fixture reproduces it.** Adding the WS3 permission to a scratch copy of `example/schema.zed` generates `CheckFolderNestedAccessInputs` as `{ User; Group; Role }`. The flat equivalent generates `{ User; Group; Role; TeamAdmin []Team; Caveats CheckFolder<Perm>Caveats }`. After the fix, the nested form must match the flat form.

Impact: fail-closed, not fail-open. The stub cannot express valid checks, and a user granted only through a dropped caveated branch always receives `ConditionalPermissionError` with no stub field for supplying the missing key. `walkPermCaveats` and `walkPermUsersets` read the same `Expressions` slice, and the `--emit-opa` builtins are generated from it, so all three are affected.

## Solution: Return a slice from `lowerSetOperationChild`

    Before:
      lowerSetOperationChild(perm, child) (PermissionExpr, error)
        UsersetRewrite child → lowerUsersetRewrite(...) → exprs[0]   (siblings dropped)

    After:
      lowerSetOperationChild(perm, child) ([]PermissionExpr, error)
        ComputedUserset / TupleToUserset / FunctionedTupleToUserset / XSelf → one-element slice
        UsersetRewrite child → lowerUsersetRewrite(...) → all exprs
      lowerUsersetRewrite appends every returned element (append(out, es...))

### Components

**`lowerSetOperationChild`** (`internal/generator/adapter.go`)
- Return type widens to `[]PermissionExpr`. Leaf cases return one-element slices.
- The `UsersetRewrite` case returns the full recursive result. The "produced no expressions" error stays for the empty case.
- The comment block in this case cites this job and drops the false "exactly one expression" claim.

**`lowerUsersetRewrite`** (`internal/generator/adapter.go`)
- The loop body changes from `out = append(out, e)` to `out = append(out, es...)`.

### Why not alternatives

| Approach | Verdict |
|---|---|
| **Widen `lowerSetOperationChild` to return a slice (chosen)** | One code path for every child kind; no unreachable case left behind |
| Special-case `UsersetRewrite` inside `lowerUsersetRewrite`'s loop, keep the single-value signature | Leaves a dead `UsersetRewrite` case in `lowerSetOperationChild`, or two places that handle nesting |
| Operator-aware tree (`PermissionNode`) | Needed by `studio` (ADR-007) but a separate, additive change. Bundling it would turn a PATCH bug fix into a MINOR feature release. |

## Workstreams

### 1. Adapter fix

The fix lives in the lowering step, so every downstream consumer (resolver, walkers, templates, OPA emitter) is corrected without touching any of them.

| # | Task | File | Status |
|---|------|------|--------|
| 1.1 | Change `lowerSetOperationChild` to return `([]PermissionExpr, error)`: one-element slices for leaf kinds, the full `lowerUsersetRewrite` result for `UsersetRewrite`, and a rewritten comment for that case | `internal/generator/adapter.go` | [x] |
| 1.2 | Update `lowerUsersetRewrite`'s loop to `out = append(out, es...)` | same | [x] |

**Key details:** The signature change and the caller update belong in the same edit, since an intermediate state does not compile. Duplicate expressions (e.g. `(viewer + editor) - viewer`) need no handling here: `resolvePermissionExpressionTypes` already de-duplicates via its `uniq` key (`internal/generator/generator.go:564`), and the walkers track `visited`.

### 2. Unit tests

| # | Task | File | Status |
|---|------|------|--------|
| 2.1 | Update the three existing `TestLowerSetOperationChild_*` tests to assert `len(got) == 1` and inspect `got[0]` | `internal/generator/adapter_test.go` | [x] |
| 2.2 | Add `TestLowerSetOperationChild_NestedUnion_ReturnsAllBranches`: a `UsersetRewrite` child holding a union of 3 children (identifier, identifier, arrow) returns 3 expressions in order | same | [x] |
| 2.3 | Add `TestLowerUsersetRewrite_ExclusionOverNestedUnion_FlattensAll`: exclusion whose first child is a nested 2-child union and whose second child is an identifier returns 3 expressions | same | [x] |

### 3. Fixture

| # | Task | File | Status |
|---|------|------|--------|
| 3.1 | On `extsvc/folder`, add `relation nested_banned: extsvc/user` and `permission nested_access = (viewer + tenanted_viewer + collab) - nested_banned`, with a comment citing AUZ-025. This covers 3 direct types plus a caveated branch plus a userset branch, all behind a non-first position in a nested union. | `example/schema.zed` | [x] |
| 3.2 | Regenerate: `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed` | `example/authzed/**/*.gen.go` | [x] |
| 3.3 | Confirm the diff is additive only: new `nested_banned` / `nested_access` members on `Folder` and their OPA builtins. Every other generated file must be byte-identical (the fixture had no nested rewrites before this job). | same | [x] |

### 4. E2E tests

| # | Task | File | Status |
|---|------|------|--------|
| 4.1 | `TestFolder_CheckNestedAccess_UsersetBranch`: write `folder#collab@team#admin`, assert `CheckNestedAccess` with the generated team-admin userset field returns true | `example/authzed/extsvc/extsvc_test.go` | [x] |
| 4.2 | `TestFolder_CheckNestedAccess_CaveatedBranch`: write `tenanted_viewer` with `tenant_match`, assert `CheckNestedAccess` returns true with the matching tenant in the generated `Caveats` field. Follow the pattern of `TestFolder_CheckTenantedBrowse_MatchTenant` (`extsvc_test.go:419`). | same | [x] |
| 4.3 | `TestFolder_CheckNestedAccess_Banned`: a user who is both `viewer` and `nested_banned` is denied, which proves the exclusion still applies server-side | same | [x] |
| 4.4 | `TestOPA_CheckFolderNestedAccess_CaveatedBranch`: the same caveated grant through the OPA builtin. Follow the pattern of `TestOPA_CheckFolderTenantedBrowse_WithCaveat_Match` (`extsvc_opa_test.go:52`). | `example/authzed/extsvc/extsvc_opa_test.go` | [x] |

### 5. Verification

| # | Task | Status |
|---|------|--------|
| 5.1 | `go build ./...`, `go vet ./...`, `gofmt -l .` all clean | [x] |
| 5.2 | `golangci-lint run ./...` and `govulncheck ./...` clean | [x] |
| 5.3 | Round-trip: a second regeneration into `example/authzed` leaves `git diff --quiet example/authzed/` true (zero diff against the 3.2 output) | [x] |
| 5.4 | Full `go test ./...` passes, including the e2e testcontainers suites | [x] |

### 6. Documentation

| # | Task | File | Status |
|---|------|------|--------|
| 6.1 | New `[2.1.0]` entry (originally `[2.0.1]`) under `### Fixed`: nested permission rewrites dropped every branch after the first; generated input structs now include the missing types, usersets and caveat fields (additive) | `CHANGELOG.md` | [x] |
| 6.2 | Append an erratum note to the `lowerSetOperationChild` section stating that the "return the first" assumption was false and pointing to AUZ-025. Do not rewrite the historical spec body. | `docs/spec-001-intersection-exclusion-codegen.md` | [x] |

## Design Decisions

### Flattening matches existing top-level semantics, including over-inclusion

`lowerUsersetRewrite` already treats intersection and exclusion as a union of all children (`adapter.go:584-594`). A top-level `viewer - banned` therefore already contributes `banned`'s types to the inputs. This job applies the same rule one level down and does not change it. Over-inclusion is fail-closed: SpiceDB still evaluates the operator and denies. Making input types operator-exact (intersection = type intersection, exclusion = left side only) would remove fields from generated structs, which is a breaking change and belongs to the operator-tree work, not to this PATCH.

### ~~PATCH, not MINOR~~ → MINOR under a correctness-fix exception (revised after review)

Generated code for schemas without nested rewrites is byte-identical. For schemas with nested rewrites, generated structs only gain fields. However, `Lookup<Perm><Type>Subjects` gains a required `caveats` parameter when a caveat is reachable only through a later nested branch. That is a generated-signature break, released as MINOR under the README's correctness-fix exception (see Discoveries). The public `pkg/authz` API is untouched.

## What Stays Unchanged

- `PermissionView` / `PermissionExpr` shapes. Adding an operator tree and `ArrowFunc` is a separate job (ADR-007 follow-up).
- `ParamSpec` and `caveatTypeToGo`.
- `resolvePermissionExpressionTypes`, `walkPermCaveats`, `walkPermUsersets`, and every template under `internal/templates/`. They already handle arbitrary-length `Expressions`.
- `pkg/authz` and `pkg/authz/spicedb`.
- Existing fixture permissions and their generated code.

## Implementation Order

    1. Workstream 1 (adapter fix)        ← signature change + caller must compile together
    2. Workstream 2 (unit tests)         ← existing tests stop compiling after WS1's signature change
    3. Workstream 3 (fixture + regen)    ← generated nested_access members only appear correctly after WS1
    4. Workstream 4 (e2e tests)          ← call generated methods/fields that WS3 produces
    5. Workstream 5 (verification)       ← gates close over WS1-4
    6. Workstream 6 (docs)               ← CHANGELOG describes the verified, finished fix

## Notes

- Write the WS4 e2e tests first and run them against the pre-fix generator: they should fail to compile, because the userset and caveat fields don't exist yet. That demonstrates the regression the fixture guards against.
- Exact generated field names (e.g. the team-admin userset field, the `Caveats` struct name) come from the WS3 output. Read them from `example/authzed/extsvc/folder.gen.go` rather than guessing.

## Discoveries & Decisions During Implementation

### [Implementer] Ran WS3 + WS4 before WS1 to prove the regression

Per the Notes, the fixture permission was added and regenerated with the **unfixed** generator first. The WS4 e2e tests were then written against that output, and `go vet ./example/authzed/extsvc/` failed with `unknown field TeamAdmin in struct literal of type extsvc.CheckFolderNestedAccessInputs`. That is the regression the fixture now guards. After WS1, regeneration produced `CheckFolderNestedAccessInputs{User, Group, Role, TeamAdmin, Caveats}`, matching the flat form.

### [Implementer] Generated diff is additive; `schema.gen.go` changes as expected

`git diff example/authzed` touches only `extsvc/folder.gen.go` and `extsvc/opa.gen.go` (additions only), plus `schema.gen.go`. In `schema.gen.go`, the only removed lines are the old `SchemaText` / `SchemaDigest` constants, which embed the edited `schema.zed`. Every other generated file is byte-identical. This confirms the pre-existing fixture had no nested rewrites: `elite_access`, `scored_minus_token` and `extsvc/article`'s `&` / `-` permissions are all top-level operators.

### [Implementer] Locally installed lint/vuln tools were built with go1.26

`golangci-lint` and `govulncheck` on `PATH` refused to load the module (`go.mod` targets go 1.27.4). Both were rebuilt into a throwaway `GOBIN` at the exact versions pinned in `.github/workflows/security.yml` (`govulncheck@v1.1.4`, `golangci-lint/v2@v2.15.0`) and run from there: `No vulnerabilities found.` and `0 issues.` The global binaries were left untouched. Reinstalling them with the current toolchain fixes this for future sessions.

### [Reviewer] Verification summary

`go build ./...`, `go vet ./...`, `gofmt -l .` (empty), `go mod tidy` (no diff), regeneration round-trip (zero diff on a second regeneration), CI-pinned `golangci-lint` (0 issues) and `govulncheck` (none), and `go test ./... -count=1` all pass. The full run includes the four new `*NestedAccess*` e2e tests, confirmed executed (not skipped) against the SpiceDB testcontainer.

### [Reviewer] CodeRabbit on #13: one generated signature is not additive

The "only gains fields" claim held for every struct, but not for `Lookup<Perm><Type>Subjects`. `object.go.tmpl:323` appends a `caveats` parameter when `hasPermCaveats` is true. A schema whose caveat sits only in a nested branch after the first flips that predicate from false to true, so the regenerated method gains a required argument. Verified on 2026-11-18 by generating `access = (viewer + gated) - banned` with `origin/main` and with this branch. Only `LookupAccessUserSubjects(ctx)` → `(ctx, caveats CheckDocAccessCaveats)` differs; `CheckAccess`, `LookupAccessDocResources`, the wildcard lookup and the OPA builtin declarations are identical.

That breaks README § Versioning ("generated method signatures require a major bump"). The owner chose a documented correctness-fix exception over a `/v3` module move. README § Versioning gains the exception, CHANGELOG gains a `### Breaking (correctness fix)` section with the exact trigger and migration, and the release becomes `v2.1.0`, shipped together with AUZ-026. The original "PATCH, not MINOR" decision above is superseded.

The other two CodeRabbit findings on #13 are pre-existing and out of scope here. The OPA empty-subject fail-open became AUZ-026 (#14). The userset-blind `Lookup<Perm><Resource>Resources` template is tracked as AUZ-027.
