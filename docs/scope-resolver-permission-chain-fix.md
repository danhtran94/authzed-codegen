# Scope: Resolver permission-chain type propagation

| Field   | Value      |
|---------|------------|
| Status  | Accepted   |
| Created | 2026-05-03 |
| Author  | Danh Tran  |

---

## Problem

`internal/generator/generator.go:GetPermissionTree` builds a per-definition map (`relationResolver`) keyed by `prefix/name/permission` and valued as the list of allowed types contributed by that permission. The map is populated only from permission entries whose `Kind == "relation"` — entries whose `Kind == "permission"` (an arrow expression like `parent->browse` resolved through another definition's permission) are skipped (per A1).

When a permission references another permission by name (e.g. `permission admin = view + edit`), the dependent permission inherits only the **direct relation** contributions of the target — never the contributions that flowed through arrows. The arrow contribution path is silently dropped.

Two committed fixtures demonstrate the bug:

- `example/authzed/extsvc/document.gen.go` — `permission admin = view + edit` produces `type CheckDocumentAdminInputs struct { User []User; Group []Group }`. The schema-correct field set is `{User, Group, Role}`. The missing `Role` is reachable through `view = parent->browse` (an arrow contribution to `extsvc/folder.browse`, which has type `extsvc/role`).
- `example/authzed/bookingsvc/employee.gen.go` — `permission view = manage + viewer` produces `type CheckEmployeeViewInputs struct { User []User }`. The schema-correct field set is `{User, Brand}`. The missing `Brand` is reachable through `manage = account + belongs_brand->manage` (an arrow contribution to `bookingsvc/brand`).

Runtime impact is silent under-permitting: a caller passing a `Brand` to `CheckEmployeeView` hits `authz.ErrNoInput` from the generated stub before the call ever reaches SpiceDB, even though SpiceDB itself would resolve the permission correctly server-side. The schema and the engine agree; the generated input struct disagrees with both.

The defect predates the AUZ-001 parser migration — it is in the resolver / tree-builder, not the parser. AUZ-001 inherited the behavior unchanged (per A2).

## Success Criteria

1. After the fix lands and `go run ./cmd/authzed-codegen --output example/authzed example/schema.zed` regenerates, `example/authzed/extsvc/document.gen.go` declares `type CheckDocumentAdminInputs struct` with field `Role []Role` present (in addition to `User` and `Group`). Verifiable: `grep -A 4 "type CheckDocumentAdminInputs struct" example/authzed/extsvc/document.gen.go` shows exactly the three field lines `User []User`, `Group []Group`, `Role []Role` (in any order).
2. After regeneration, `example/authzed/bookingsvc/employee.gen.go` declares `type CheckEmployeeViewInputs struct` with field `Brand []Brand` present (in addition to `User`). Verifiable: `grep -A 3 "type CheckEmployeeViewInputs struct" example/authzed/bookingsvc/employee.gen.go` shows exactly two field lines including `Brand []Brand`.
3. A schema with a self-referential permission (`permission x = x + y` in a hand-crafted `.zed` file) causes `authzed-codegen` to exit non-zero; stderr contains the literal string `cycle detected`. Verifiable by writing the schema to `/tmp/cycle.zed`, running `go run ./cmd/authzed-codegen --output /tmp/out /tmp/cycle.zed`, and checking `$? != 0` plus `grep "cycle detected"` against the captured stderr.
4. `go build ./example/...` exits 0 against every regenerated `.gen.go` file in `example/authzed/{bookingsvc,menusvc,extsvc}/`.
5. `go vet ./...` exits 0 against the repository after the fix lands.
6. The full set of files that diff after regeneration is committed in the same job-doc commit as the resolver change. Verifiable: the commit's diff includes both `internal/generator/generator.go` and at least the two files named in SC1 / SC2; the post-commit working tree is clean.

## Out of Scope

- **Codegen for intersection (`&`), exclusion (`-`), wildcard relations, caveats, expiration traits, or sub-relation references.** Reason: each is a separate ADR-track item; this scope is resolver-only and does not change `adapter.go` rejection rules.
- **Any change to `pkg/authz/` or `pkg/authz/spicedb/`.** Reason: the bug is in build-time codegen; the runtime engine receives the correct schema and behaves correctly. No runtime contract changes.
- **Adding a `*_test.go` test harness.** Reason: per A2, the round-trip diff against committed fixtures remains the regression bar; introducing a unit-test framework is a separate scope item that should be planned with its own ADR.
- **Performance optimization of `GetPermissionTree`.** Reason: codegen runs at build time on schemas with O(10s) of definitions; complexity is not load-bearing.
- **Validating SpiceDB-side semantics.** Reason: cycle detection in the resolver is for the codegen's own termination, not for rejecting schemas SpiceDB would accept. SpiceDB validates cycles at evaluation time (per A3); the codegen only needs to terminate.

## Risks

- **Eager resolution can recurse infinitely on cyclic permission references.** Mitigation: add cycle detection during `GetPermissionTree` traversal. Track visited `(definition, permission)` pairs in a `map[string]bool` keyed by `prefix/name/permission`; on revisit return a sentinel error rather than recursing. The SC3 cycle test confirms this.
- **The fix changes the regression baseline for at least 2 committed `.gen.go` files (and likely more — any permission that transitively references an arrow chain through another permission will gain types).** Mitigation: regenerate and recommit all affected files in the same job-doc commit. The job's Discoveries section enumerates which files diff and why each diff is intentional. SC6 enforces this co-commit discipline.
- **A schema author may have been relying on the under-resolved `CheckXInputs` shape to suppress fields they did not want exposed in their service layer.** Mitigation: document the change in `README.md`'s "Schema parser" section as a behavior fix; mention it in the corresponding job doc's Goal so reviewers can flag any downstream callers. Out-of-scope but worth surfacing during review.
- **Re-shaping `relationResolver` to carry transitive types may interact with the existing `seen` map in `addTree` (line ~155 of `generator.go`).** Mitigation: preserve the existing dedup semantics in `addTree` — the only behavioral change is which types reach the `addTree` call, not how `addTree` itself merges them.

## Assumptions

- **A1 [VERIFIED]:** `GetPermissionTree` only indexes `Kind == "relation"` permission entries in `relationResolver`. Evidence: `internal/generator/generator.go` first definition loop, gated by `if p.Kind == "relation"` inside the populator block (~line 170 of the post-AUZ-001 file).
- **A2 [VERIFIED]:** The defect predates AUZ-001 — the resolver code in the post-migration `generator.go` is bit-for-bit identical to the pre-migration version (only the input element type changed from `*ast.DefinitionNode` to `*DefinitionView`). Evidence: AUZ-001 job doc Discoveries entry "Resolver became a flat for-loop" notes that the per-permission switch-on-Kind logic was preserved verbatim. The pre-migration `bookingsvc/employee.gen.go` (committed before any of my changes, regenerated by the old AST) shipped with `CheckEmployeeViewInputs { User []User }` — same wrong output as today.
- **A3 [EXTERNAL FACT]:** SpiceDB's `pkg/schemadsl/compiler.Compile` accepts schemas with self-referential or cyclic permissions; cycle detection happens at evaluation time on the engine side, not at compile time. Evidence: SpiceDB v1.52.0 source at `pkg/schemadsl/compiler/translator.go` does not validate against permission cycles; the `dispatch` package in spicedb runtime owns evaluation-time cycle handling. Implication: the resolver fix must own its own cycle detection.
- **A4 [HYPOTHESIS]:** No `*_test.go` files exist in the repository; the round-trip diff against `example/authzed/` is the only regression mechanism today. Evidence: `find . -name "*_test.go"` returns empty as of commit `bb37dab`. Verification: re-run before the fix lands.

## History

_Binary-owned by `harness history-update`. Do not hand-edit._
