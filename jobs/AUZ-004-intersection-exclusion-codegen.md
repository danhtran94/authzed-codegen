# AUZ-004: intersection / exclusion codegen support

| Field       | Value                                          |
|------------|------------------------------------------------|
| Status      | Completed                                      |
| Created     | 2026-05-05                                       |
| Assignee    | TBD                                              |
| Source      | docs/spec-001-intersection-exclusion-codegen.md   |
| Depends on  | —                                              |
| Implements  | docs/scope-intersection-exclusion-codegen.md     |

---

## Goal

Extend the SpiceDB adapter in `internal/generator/adapter.go` to accept intersection (`&`) and exclusion (`-`) rewrite operators in AuthZED permission expressions. The adapter currently rejects these operators with "not supported" errors. After this job:

1. `authzed-codegen` compiles against `.zed` schemas containing `&` and `-` in permission expressions without emitting errors.
2. The generated `CheckXInputs` structs for intersection/exclusion permissions contain the union of input types from all operands.
3. `go build ./...` and `go vet ./...` pass.
4. No existing `.gen.go` fixture diffs (the example schema does not currently use `&` or `-`).

---

## Tasks

### Task 1: Add `PermExprIntersection` and `PermExprExclusion` constants

- **File:** `internal/generator/adapter.go`
- **Action:** Add two new `const` entries to the existing `PermExpr*` block:
  ```go
  PermExprIntersection = "&"
  PermExprExclusion     = "-"
  ```
- **Workstream:** adapter
- **Verification:** `go build ./...` passes after adding the constants (they are currently unused but defined).

### Task 2: Rewrite `lowerUsersetRewrite` — remove rejections, fall through

- **File:** `internal/generator/adapter.go`
- **Action:** Replace the two `if` blocks that reject `GetIntersection()` and `GetExclusion()` with fall-through logic that treats whichever operator is active as the common `union` variable:
  ```go
  union := rw.GetUnion()
  if union == nil {
      union = rw.GetIntersection()
  }
  if union == nil {
      union = rw.GetExclusion()
  }
  if union == nil {
      return nil, fmt.Errorf("...")
  }
  ```
- **Workstream:** adapter
- **Verification:** Compile passes; no new errors introduced.
- **Dependency:** depends on Task 1 (constants must exist before the adapter emits them, though the fall-through logic itself doesn't emit the new kinds).

### Task 3: Extend `lowerSetOperationChild` — handle UsersetRewrite children

- **File:** `internal/generator/adapter.go`
- **Action:** Replace the `case c.GetUsersetRewrite() != nil` error-return with a recursive call to `lowerUsersetRewrite`:
  ```go
  case c.GetUsersetRewrite() != nil:
      rw := c.GetUsersetRewrite()
      exprs, err := lowerUsersetRewrite(permName, rw)
      if err != nil {
          return PermissionExpr{}, err
      }
      if len(exprs) > 0 {
          return exprs[0], nil
      }
      return PermissionExpr{}, fmt.Errorf("...")
  ```
- **Workstream:** adapter
- **Verification:** Compile passes.
- **Dependency:** depends on Task 2 (the `lowerUsersetRewrite` signature must accept the intersection/exclusion branch).

### Task 4: Build verification

- **File:** `go.mod`, `go.sum` (no code changes expected beyond adapter.go)
- **Action:** Run `go build ./...`, `go vet ./...`. Verify zero errors.
- **Workstream:** verification
- **Verification:** `go build ./...` exits 0, `go vet ./...` exits 0 with no issues.
- **Dependency:** depends on Tasks 1–3.
