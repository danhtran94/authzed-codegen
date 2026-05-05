# Scope: Codegen for intersection / exclusion rewrites

| Field    | Value          |
|----------|----------------|
| Status   | Draft          |
| Created  | 2026-05-05     |
| Author   | Danh Tran      |

---

## Problem

The SpiceDB proto (`UsersetRewrite` in `github.com/authzed/spicedb/pkg/proto/core/v1`) models permission rewrites as a tree where each node is **union** (`+`), **intersection** (`&`), or **exclusion** (`-`). A schema like `permission admin = viewer & active` compiles to a `UsersetRewrite` whose `RewriteOperation` is `GetIntersection()`, not `GetUnion()`.

`internal/generator/adapter.go` rejects intersection and exclusion at adapt time:

- `lowerUsersetRewrite`: returns error for `rw.GetIntersection()` and `rw.GetExclusion()` — "not supported"
- `lowerSetOperationChild`: returns error for `c.GetUsersetRewrite()` — "nested rewrites not supported"

Both of these are the same code path that intersection/exclusion take. The SpiceDB encoder represents `a & b` as a top-level intersection node whose child is a union node. The adapter hits `GetUsersetRewrite()` → error.

The codegen layer (generator.go) does not distinguish operators either — `resolvePermissionExpressionTypes` treats every child as a flat type contributor. `resolveTransitive` walks all children identically regardless of whether the original operator was `+`, `&`, or `-`.

**Result:** AuthZED schemas using `&` or `-` in permission expressions cannot be codegen'd against. `authzed-codegen` exits with a schema-relative error instead of producing `.gen.go` output.

---

## Success Criteria

1. `authzed-codegen` compiles against a hand-crafted `.zed` file containing an intersection permission (`permission p = a & b`) and an exclusion permission (`permission p = a - b`) without emitting a "not supported" error. Verifiable: write the schema to `/tmp/test-intercl.zed`, run `go run ./cmd/authzed-codegen --output /tmp/out /tmp/test-intercl.zed`, check `$? == 0`.
2. The generated `CheckXInputs` struct for an intersection permission contains the union of input types from both operands. Verifiable: `grep -A 10 "type Check[A-Z]*Inputs struct" /tmp/out/.../*.gen.go` shows field lines for all subject types reachable through either operand.
3. A new test fixture schema (e.g. `example/schema-intercl.zed`) with at least one intersection permission and one exclusion permission exists under `example/`. The fixture exercises intersection and exclusion at the top-level permission AND nested inside other rewrites.
4. A spec doc (`docs/SPEC-NNN-intersection-exclusion-codegen.md`) is authored defining the codegen semantics for intersection/exclusion: how `CheckXInputs` shapes are derived, how the template emits checks for intersection/exclusion permissions (same as union — pass all candidate types, engine resolves server-side), and invariant guarantees.
5. `go build ./...` and `go vet ./...` pass after adding the test fixture.

---

## Out of Scope

- **Runtime behavior changes in `pkg/authz/` or `pkg/authz/spicedb/`.** Reason: the engine already supports `&` and `-` server-side via SpiceDB; codegen only generates input structs and check stubs.
- **Template changes for intersection/exclusion.** Reason: the generated `CheckXInputs` stub is the same regardless of operator — all candidate types are accepted, the engine resolves the set operation server-side.
- **Caveats, expiration traits, sub-relation references.** Reason: separate ADR-track items already defined in ADR-001.
- **A job doc (AUZ-NNN) for implementation.** Reason: this scope is followed by a SPEC + the actual implementation job; this scope note exists solely to define boundaries.

---

## Risks

- **Intersection/exclusion semantics differ from union in edge cases (e.g. empty intersection of non-empty sets).** Mitigation: codegen does not evaluate the set — it generates input structs with all reachable types. The SpiceDB engine resolves intersection/exclusion semantics server-side. Low risk.
- **The SPEC becomes large because intersection/exclusion have nuanced semantics (nested ops, interaction with wildcards, interaction with arrows through intersection/exclusion boundaries).** Mitigation: scope the SPEC to codegen's perspective only — input type derivation and template emission. Defer runtime semantics to a separate ADR.
- **Adding a test fixture under `example/` grows the example surface and may trigger round-trip diffs in existing fixtures.** Mitigation: the new test fixture is a separate file (`example/schema-intercl.zed`), not merged into `example/schema.zed`. Existing fixtures are untouched.

---

## Assumptions

- **A1 [VERIFIED]:** `UsersetRewrite` in `github.com/authzed/spicedb/pkg/proto/core/v1` has three oneof variants: `GetUnion()`, `GetIntersection()`, `GetExclusion()`, each returning `*SetOperation`. Evidence: `go doc` on `corev1.UsersetRewrite` confirms all three accessor methods exist.
- **A2 [VERIFIED]:** The adapter's `lowerUsersetRewrite` returns errors for `GetIntersection()` and `GetExclusion()`. Evidence: `internal/generator/adapter.go` lines 130–134.
- **A3 [VERIFIED]:** The adapter's `lowerSetOperationChild` returns an error for `c.GetUsersetRewrite()`, which is the code path intersection/exclusion take (a `UsersetRewrite` whose `RewriteOperation` is intersection/exclusion contains a `Child` whose `Child` field is another `UsersetRewrite`). Evidence: `internal/generator/adapter.go` lines 173–174.
- **A4 [HYPOTHESIS]:** The generator's `resolvePermissionExpressionTypes` and `resolveTransitive` do not distinguish between union, intersection, and exclusion at the permission resolution level — all children contribute types to a flat set. Verification: review `internal/generator/generator.go` `resolvePermissionExpressionTypes` and `resolveTransitive` before the SPEC is authored.
- **A5 [EXTERNAL FACT]:** SpiceDB represents `a & b` as a top-level intersection node whose child is a nested `UsersetRewrite` (typically a union). Evidence: SpiceDB encoder logic in `authzed/spicedb/pkg/enc` transforms AST into proto; intersection nodes become `UsersetRewrite{Intersection: ...}`.

---
