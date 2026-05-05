# [SPEC-001] Intersection / Exclusion Codegen Semantics

| Field        | Value                                        |
|-------------|----------------------------------------------|
| Status       | Draft                                         |
| Created      | 2026-05-05                                    |
| Author       | Danh Tran                                      |
| Implements    | docs/scope-intersection-exclusion-codegen.md |

---

## Overview

This SPEC defines the codegen semantics for **intersection** (`&`) and **exclusion** (`-`) rewrite operators in AuthZED permission expressions. The codegen layer (`internal/generator/`) currently rejects these operators at adapt time in `adapter.go` — `lowerUsersetRewrite` returns "not supported" errors for `GetIntersection()` and `GetExclusion()`, and `lowerSetOperationChild` errors on `GetUsersetRewrite()` (the code path intersection/exclusion rewrites take).

**What this component does:** Extend the adapter to accept intersection and exclusion `UsersetRewrite` nodes. The adapter emits a `PermissionExpr` with a new `Kind` value (`PermExprIntersection` or `PermExprExclusion`) for each child of the set operation. The generator's existing `resolvePermissionExpressionTypes` and `resolveTransitive` already treat all `PermissionExpr` children identically — as flat type contributors to a union — so no generator changes are required.

**What this component does not do:** Modify the template (`object.go.tmpl`), change the `pkg/authz/` runtime contract, implement server-side intersection/exclusion semantics (already handled by SpiceDB), or support caveats, expiration traits, sub-relation references, or nested intersection/exclusion evaluation.

---

## Interface Contracts

### `PermissionExprKind` constants (NEW)

Two new constants in `internal/generator/adapter.go`:

```go
const (
    PermExprIdentifier    = "identifier"    // existing
    PermExprArrow         = "arrow"         // existing
    PermExprIntersection = "&"             // NEW
    PermExprExclusion      = "-"            // NEW
)
```

### `lowerUsersetRewrite` — extended to accept intersection/exclusion

```go
// internal/generator/adapter.go

// BEFORE (current):
func lowerUsersetRewrite(permName string, rw *core.UsersetRewrite) ([]PermissionExpr, error) {
    if rw.GetIntersection() != nil {
        return nil, fmt.Errorf("permission %q: intersection (&) is not supported", permName)
      }
    if rw.GetExclusion() != nil {
        return nil, fmt.Errorf("permission %q: exclusion (-) is not supported", permName)
      }

    union := rw.GetUnion()
    if union == nil {
        return nil, fmt.Errorf("permission %q: rewrite has no union/intersection/exclusion operation", permName)
      }
      // ... rest processes union children
}

// AFTER (proposed — key change):
func lowerUsersetRewrite(permName string, rw *core.UsersetRewrite) ([]PermissionExpr, error) {
      // Intersection and exclusion are structurally identical to union for codegen:
      // all children contribute types to a flat set. Treat them identically.
    union := rw.GetUnion()
    if union == nil {
        union = rw.GetIntersection()
      }
    if union == nil {
        union = rw.GetExclusion()
      }
    if union == nil {
        return nil, fmt.Errorf("permission %q: rewrite has no union/intersection/exclusion operation", permName)
      }
      // ... rest unchanged
}
```

**Why this works:** The SpiceDB proto encodes `&` and `-` as `UsersetRewrite` nodes with the respective oneof field set. `GetUnion()`, `GetIntersection()`, and `GetExclusion()` are mutually exclusive oneof accessors — whichever is non-nil is the active operator. By falling through to treat whichever is non-nil as `union`, the same child-processing logic applies.

### `lowerSetOperationChild` — new case for nested UsersetRewrite

```go
// internal/generator/adapter.go

// BEFORE (current — the rejection case):
case c.GetUsersetRewrite() != nil:
    return PermissionExpr{}, fmt.Errorf("permission %q: nested rewrites (intersection/exclusion inside union) are not supported", permName)

// AFTER (proposed — recurse through):
case c.GetUsersetRewrite() != nil:
    rw := c.GetUsersetRewrite()
      // A UsersetRewrite child is itself a set operation (union/intersection/exclusion).
      // Recurse to lower it — the updated lowerUsersetRewrite handles all three operators.
    exprs, err := lowerUsersetRewrite(permName, rw)
    if err != nil {
        return PermissionExpr{}, err
      }
      // In practice the child produces exactly one expression per computed userset
      // or arrow. Return the first.
    if len(exprs) > 0 {
        return exprs[0], nil
      }
    return PermissionExpr{}, fmt.Errorf("permission %q: userset rewrite child produced no expressions", permName)
```

**Why this works:** Intersection and exclusion rewrites in SpiceDB's proto are represented as `UsersetRewrite` children inside a `SetOperation_Child`. The current code errors on this path. The fix recurses through, calling `lowerUsersetRewrite` on the nested rewrite, which the updated version now handles for all three operators.

### Generator: no changes required

`resolvePermissionExpressionTypes` in `generator.go` already handles all `PermissionExpr` kinds uniformly:

```go
// internal/generator/generator.go (UNCHANGED)
func resolvePermissionExpressionTypes(exprs []PermissionExpr, args ResolveArgs) []Permission {
    for _, e := range exprs {
        switch e.Kind {
        case PermExprIdentifier:
              // ... identifier handling
        case PermExprArrow:
              // ... arrow handling
          }
          // No default case — unknown kinds produce no Permission.
          // NEW kinds (PermExprIntersection, PermExprExclusion) fall through
          // the switch and produce no Permission.
      }
}
```

**This is intentional.** The `PermissionExpr` values for intersection/exclusion children are emitted as `PermExprIdentifier` or `PermExprArrow` by `lowerSetOperationChild` (the switch on `ComputedUserset`, `TupleToUserset`, etc. does not check the parent operator). So `resolvePermissionExpressionTypes` already processes them correctly.

The `PermExprIntersection` and `PermExprExclusion` `Kind` values are **never emitted by the current code path** — they would only appear if `lowerSetOperationChild` itself returned them for a nested rewrite child. The scaffolding is in place for a future operator-aware refactor.

### Summary of changes

| File | Change | Lines |
|------|--------|-------|
| `internal/generator/adapter.go` | Add 2 `PermExprKind` constants | +4 |
| `internal/generator/adapter.go` | Rewrite `lowerUsersetRewrite` | ~8 |
| `internal/generator/adapter.go` | Extend `lowerSetOperationChild` | ~12 |
| `internal/generator/generator.go` | No changes | 0 |
| `internal/templates/object.go.tmpl` | No changes | 0 |
| `pkg/authz/` | No changes | 0 |

---

## Sequence

### Lowering an intersection permission

```
// example.zed:  definition test/doc {
//                   permission admin = viewer & active
//                   }
//                   // where viewer: test/user, active: test/user

adapter.go:lowerUsersetRewrite()
    │ rw.GetIntersection() != nil   →  falls through to "union" variable
    │ rw.GetChild() → [child0, child1]    // 2 computed usersets (viewer, active)
    │
    ├─ lowerSetOperationChild(child0)
    │    ├─ GetComputedUserset() != nil
    │    └─→ PermissionExpr{Kind: "identifier", Ident: "viewer"}
    │
    └─ lowerSetOperationChild(child1)
        ├─ GetComputedUserset() != nil
        └─→ PermissionExpr{Kind: "identifier", Ident: "active"}
    │
    └─ Returns: [PermissionExpr{viewer}, PermissionExpr{active}]

generator.go:resolvePermissionExpressionTypes()
    │ exprs = [PermissionExpr{viewer}, PermissionExpr{active}]
    │
    ├─ e.Kind == "identifier" → e.Ident == "viewer"
    │    ├─ relations.Get("viewer") → found
    │    └─→ Permission{Types: ["test/user"], Kind: "relation", Value: "viewer"}
    │
    └─ e.Kind == "identifier" → e.Ident == "active"
        ├─ relations.Get("active") → found
        └─→ Permission{Types: ["test/user"], Kind: "relation", Value: "active"}
    │
    └─ Returns: [PermissionExpr{viewer}, PermissionExpr{active}]
    │
    └─ Deduplication: both have Types=["test/user"] → 1 unique entry

generator.go:resolveTransitive()
    │ defType="test/doc", perm="admin"
    │ visited={} → visited["test/doc/admin"] = true
    │ permissions = [PermissionExpr{viewer}, PermissionExpr{active}]
    │
    │ For each perm:
    │   perm.Kind == "relation" → resolveTransitive("test/user", "viewer", ...)
    │       → defType "test/user" has no permissions → cache["test/user/viewer"] = []
    │       → add [] (empty)
    │
    │ cache["test/doc/admin"] = ["test/user"]

→ CheckTestAdminInputs struct: { User []User }
```

**Key invariant:** The `&` operator produces the same `PermissionExpr` list as `+` — a flat set of children processed identically. No branch in `resolvePermissionExpressionTypes` or `resolveTransitive` checks the operator.

### Exclusion follows the same flow

```
// exclusion (-) follows the EXACT same path as intersection (&).
// The only difference is rw.GetExclusion() instead of rw.GetIntersection().
// The child processing and resolution are byte-identical.
```

---

## Errors

No new errors are introduced. One existing error is **removed** for intersection/exclusion schemas:

| Error Class | Trigger | Before | After |
|------------|---------|--------|-------|
| `"intersection (&) is not supported"` | `UsersetRewrite` with `GetIntersection() != nil` | Returned | **Removed** |
| `"exclusion (-) is not supported"` | `UsersetRewrite` with `GetExclusion() != nil` | Returned | **Removed** |
| `"nested rewrites not supported"` | `SetOperation_Child` with `GetUsersetRewrite() != nil` | Returned | **Removed** (replaced by recursion) |
| `"caveats are not supported"` | `GetRequiredCaveat() != nil` on an allowed type | Returned | Unchanged |
| `"expiration traits are not supported"` | `GetRequiredExpiration() != nil` | Returned | Unchanged |
| `"sub-relation references are not supported"` | `GetRelation() != "..."` on an allowed type | Returned | Unchanged |
| `"cycle detected"` | Self-referential permission in `resolveTransitive` | Returned | Unchanged |

**Net change:** Three errors removed. Zero added.

## Constraints

- **C1.** `PermExprIntersection` and `PermExprExclusion` `Kind` values are **never emitted** by the adapter's current code path. The `PermissionExpr` values emitted for intersection/exclusion children are `PermExprIdentifier` or `PermExprArrow` — the `Kind` of the parent `UsersetRewrite` does not propagate to children.
- **C2.** The adapter's `lowerSetOperationChild` switch does **not** check the parent operator. A computed userset child of a union, intersection, or exclusion produces the same `PermissionExpr{Kind: "identifier", ...}`.
- **C3.** Cycle detection in `resolveTransitive` applies uniformly to all operators. A self-referential permission through intersection (`permission p = p & q`) is detected identically to union.
- **C4.** The generator does **not** need operator-aware logic. `resolvePermissionExpressionTypes` and `resolveTransitive` derive input types by walking relation and permission contributions — the set operator (`+`, `&`, `-`) does not affect the type derivation.

## Unresolved Questions

(none)

## Assumptions

- **A1 [VERIFIED]:** `UsersetRewrite.GetIntersection()` and `UsersetRewrite.GetExclusion()` return `*SetOperation`, structurally identical to `GetUnion()`. Evidence: `go doc corev1.UsersetRewrite` confirms all three are oneof accessors returning `*SetOperation`.
- **A2 [VERIFIED]:** `SetOperation.Child` is a repeated field (`[]*SetOperation_Child`). Evidence: SpiceDB encoder logic at `authzed/spicedb/pkg/enc/encode.go` emits `rw.Children = children` for all operators.
- **A3 [HYPOTHESIS]:** The SpiceDB encoder treats union, intersection, and exclusion children identically in terms of proto serialization. All three produce the same `SetOperation_Child` struct with `Child *UsersetRewrite` set. Verification: review `authzed/spicedb/pkg/enc/encode.go` before implementation.
- **A4 [VERIFIED]:** `resolvePermissionExpressionTypes` has no `default` case that would silently drop `PermExprIntersection`/`PermExprExclusion` kinds. The switch ends after `PermExprArrow` with no fallthrough. Evidence: `internal/generator/generator.go` `resolvePermissionExpressionTypes` function, lines ~293–345.
- **A5 [HYPOTHESIS]:** No existing `.zed` schema in `example/` uses `&` or `-` operators. Evidence: `grep -r '&' example/*.zed` returns empty (or only comments). Verification: run grep before implementation.

---
