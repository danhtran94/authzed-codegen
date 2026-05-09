# [SPEC-011] Functioned Tuple-to-Userset

| Field      | Value                                                |
|------------|------------------------------------------------------|
| Status     | Accepted                                             |
| Created    | 2026-05-09                                           |
| Author     | Danh Tran                                            |
| Implements | (lifts ADR-001 rejection of `FunctionedTupleToUserset`) |

---

## Overview

This SPEC accepts SpiceDB's functioned tuple-to-userset wire construct — schemas using arrow function syntax `.any(rel)` or `.all(rel)` instead of the legacy `parent->rel` form. Today the adapter rejects `FunctionedTupleToUserset` at adapt time (`internal/generator/adapter.go:614`); SPEC-011 lifts the rejection by handling the proto type identically to `TupleToUserset`. The function value (`FUNCTION_ANY` / `FUNCTION_ALL`) is server-side semantic — SpiceDB enforces strict-intersection (`.all()`) vs union (`.any()`) at evaluation time. The codegen's responsibility is to walk the arrow's structure (LeftRel + RightPerm) for input-type resolution; the function semantic is invisible to the generated method signatures.

**What this component does:** Extend `lowerSetOperationChild` in `internal/generator/adapter.go` to handle `FunctionedTupleToUserset` as a sibling case to the existing `TupleToUserset` branch. Map `tupleset.relation` → `LeftRel` and `computed_userset.relation` → `RightPerm`. Function value (`FUNCTION_ANY` / `FUNCTION_ALL`) is read but not propagated — the codegen produces identical Go output regardless of the function. Add fixture relations exercising both `.any()` and `.all()` to `example/schema.zed`. Verify SpiceDB's strict-intersection semantic for `.all()` via e2e tests (write tuples to multiple parents, omit one approval, assert deny; add the missing approval, assert grant).

**What this component does not do:** Differentiate `.any()` vs `.all()` in generated code — both produce identical method signatures because the semantic is server-side at Check time. Add new caller-facing API surface — existing `Check<Perm>` and `Lookup<Perm>...` methods cover functioned arrows transparently. Modify the template — adapter-only change. Surface the function name in any runtime type — callers can introspect via SpiceDB's `ReflectSchema` if needed (out of scope here). Validate function correctness — `compiler.Compile()` catches invalid functions before codegen runs. Handle `FUNCTION_UNSPECIFIED` (the protobuf default) — SpiceDB validates this and rejects schemas that emit it.

---

## Interface Contracts

### Adapter — `internal/generator/adapter.go`

The existing rejection in `lowerSetOperationChild`:

```go
case c.GetFunctionedTupleToUserset() != nil:
    return PermissionExpr{}, fmt.Errorf("permission %q: functioned tuple-to-userset (with self/expiration) is not supported", permName)
```

becomes a parallel case to `GetTupleToUserset()`:

```go
case c.GetFunctionedTupleToUserset() != nil:
    fttu := c.GetFunctionedTupleToUserset()
    return PermissionExpr{
        Kind:      PermExprArrow,
        LeftRel:   fttu.GetTupleset().GetRelation(),
        RightPerm: fttu.GetComputedUserset().GetRelation(),
    }, nil
```

Reuses the existing `PermExprArrow` kind. Per A1 — the codegen's downstream consumers (input-type resolver, template arrow walker, `collectPermCaveats`, `collectPermUsersets`) all key on `PermExprArrow` without inspecting the wire-level proto subtype. No changes propagate beyond this single switch case.

### Generated code — no change

`Check<Perm>` / `Lookup<Perm>...` / generated input structs treat functioned arrows identically to regular arrows. The function semantic (`FUNCTION_ANY` aggregation vs `FUNCTION_ALL` aggregation) is enforced by SpiceDB at Check time when evaluating against the deployed schema. From the caller's API perspective, `parent.all(member)` and `parent.any(member)` produce the same `Check<Perm>Inputs` shape and the same gRPC call.

### Runtime + Engine — no change

No new interface methods, no new runtime types. The Engine's `CheckPermission` already supports any schema construct; the function semantic resolves server-side.

### Fixture additions — `example/schema.zed`

Two new relations on a definition exercising both functions. Recommend adding to `extsvc/folder` (existing fixture target):

```hcl
// AUZ-016 — Functioned TTU `.any()`. Equivalent semantic to the regular
// arrow `parent->view`; exercises the explicit syntax form.
relation any_parent: extsvc/folder
permission any_via = any_parent.any(browse)

// AUZ-016 — Functioned TTU `.all()`. Strict-intersection semantic:
// grants only when EVERY tuple in `all_parent` independently grants
// `browse`. Tested by writing two parents and omitting one's grant.
relation all_parent: extsvc/folder
permission all_via = all_parent.all(browse)
```

Per A2 — both functions are valid SpiceDB syntax without `use`-flag gating; the parser accepts them when the function name appears after a relation reference.

---

## Sequence

Wire flow at codegen time:

```
schema.zed contains:
    permission all_via = all_parent.all(browse)
         │
         ▼
SpiceDB compiler:
    parses → emits SetOperation_Child{
        ChildType: *SetOperation_Child_FunctionedTupleToUserset{
            FunctionedTupleToUserset: {
                Function: FUNCTION_ALL,
                Tupleset: {Relation: "all_parent"},
                ComputedUserset: {Relation: "browse"},
            },
        },
    }

         │
         ▼
codegen lowerSetOperationChild (NEW handler):
    case c.GetFunctionedTupleToUserset() != nil:
      fttu := c.GetFunctionedTupleToUserset()
      → returns PermissionExpr{
          Kind:      PermExprArrow,
          LeftRel:   "all_parent",
          RightPerm: "browse",
      }
    (Function value is read but not stored — codegen output identical
     regardless of ANY/ALL)
```

Wire flow at Check time (caller perspective):

```
caller code (no change vs regular arrow):
    ok, err := folder.CheckAllVia(ctx, CheckFolderAllViaInputs{
        User: []extsvc.User{"u1"},
    })
         │
         ▼
generated method body (unchanged):
    └─► engine.CheckPermission(ctx, ..., TypeUser, []ID{"u1"})

         │
         ▼
SpiceDB evaluator:
    folder:f1 #all_via
        ├─► resolves all_via expression: all_parent.all(browse)
        ├─► reads ALL all_parent tuples for f1: [folder:p1, folder:p2, folder:p3]
        ├─► for EACH parent, evaluates parent#browse with subject=u1
        ├─► all 3 must return HAS_PERMISSION → returns granted
        └─► if ANY parent denies u1 → returns NO_PERMISSION
```

The `.any()` form follows the same flow but with union (HAS_PERMISSION if at least one parent grants) — semantically identical to the existing regular-arrow case.

---

## Errors

| Error class | Trigger | Layer |
|---|---|---|
| (REMOVED) `"functioned tuple-to-userset (with self/expiration) is not supported"` | Schema uses `.any()` or `.all()` syntax. Currently rejected at adapt time; SPEC-011 lifts. | Adapter |
| Pre-codegen schema rejection | Schema declares `.any()` / `.all()` with invalid function (`FUNCTION_UNSPECIFIED`) — caught by `compiler.Compile()` before codegen runs. | Pre-codegen (SpiceDB compiler) |

No new error classes.

---

## Constraints

- **C1.** Function value (`FUNCTION_ANY` / `FUNCTION_ALL`) is read by `lowerSetOperationChild` but not propagated. Per the design — codegen output is identical regardless. The function semantic is enforced server-side by SpiceDB.

- **C2.** `PermissionExpr` shape is unchanged. The existing `PermExprArrow` kind covers both `TupleToUserset` and `FunctionedTupleToUserset`. No new kind added; downstream consumers (`collectPermCaveats`, `collectPermUsersets`, template arrow walking) work without modification.

- **C3.** Generated `Check<Perm>` / `Lookup<Perm>...` method signatures and bodies are byte-identical to the equivalent regular-arrow form. A schema author switching from `parent->view` to `parent.any(view)` produces a no-op codegen diff (modulo any whitespace in the source).

- **C4.** `FUNCTION_UNSPECIFIED` (proto default value) is not handled by the codegen. Per A2 — SpiceDB's compiler validates the function value before the codegen sees it; an unspecified function would fail `compiler.Compile()`.

- **C5.** Round-trip idempotency stable. Existing schemas (no functioned TTU usage) regenerate byte-identical to v1.10.0. New fixture relations using `.any()` / `.all()` regenerate byte-identical on a second pass.

- **C6.** Function-aware caller introspection is out of scope. Callers wanting to know whether a permission uses `.all()` semantics (e.g. for UI display purposes) call SpiceDB's `ReflectSchema` directly. The codegen does not surface this metadata.

---

## Assumptions

- **A1 [VERIFIED]:** Downstream consumers of `PermissionExpr` key only on the `Kind` field, not on wire-level proto subtype. Evidence: `collectPermCaveats` (`adapter.go:241`) and `collectPermUsersets` (`adapter.go:249`) both `switch e.Kind` over `PermExprIdentifier` / `PermExprArrow` and read `LeftRel` / `RightPerm`. The template's permission-input-type resolver (`generator.go:402`) does the same. None of these inspect the proto-level oneof.

- **A2 [EXTERNAL FACT]:** SpiceDB's compiler emits `FunctionedTupleToUserset` for any arrow with a function call (`.any()` / `.all()`); for arrows without functions, it emits the legacy `TupleToUserset`. The compiler validates the function name; `FUNCTION_UNSPECIFIED` is rejected at compile time. Evidence: `~/go/pkg/mod/github.com/authzed/spicedb@v1.52.0/pkg/schemadsl/compiler/translator.go:650-658`.

- **A3 [EXTERNAL FACT]:** `.any()` and `.all()` are first-class arrow function syntax in SpiceDB v1.40+; not gated behind a `use` flag. Evidence: SpiceDB parser test `~/go/pkg/mod/github.com/authzed/spicedb@v1.52.0/pkg/schemadsl/parser/tests/arrowops.zed` exercises both without any `use` directive at the schema level.

- **A4 [EXTERNAL FACT]:** SpiceDB's evaluator enforces `FUNCTION_ALL` as strict-intersection across the userset rows. Evidence: SpiceDB documentation on permission evaluation; AUZ-014's empirical observation that schema-level semantics are enforced at evaluation time regardless of consistency mode (per AUZ-014 Discoveries — wall-clock expiration filtered server-side).

- **A5 [VERIFIED]:** Existing `example/schema.zed` does not use functioned TTU syntax (`.any()` / `.all()`). Confirmed by `grep '.any\|.all' example/schema.zed` — no matches. Adding new fixture relations doesn't conflict with existing tests.

- **A6 [HYPOTHESIS]:** No existing schema in this codebase emits `FunctionedTupleToUserset` proto from a regular arrow expression. Verification: regenerate the example fixture; compare output byte-identical to v1.10.0. Confirmed at WS3.

---

## Unresolved Questions

(none)

---

## Summary

Net change scope:

| File | Change |
|---|---|
| `internal/generator/adapter.go` | Replace the rejection in `lowerSetOperationChild` for `GetFunctionedTupleToUserset()` with a parallel case to `GetTupleToUserset()` — extract `tupleset.relation` and `computed_userset.relation` into `PermExprArrow`. Function value read but not propagated. |
| `internal/templates/object.go.tmpl` | NO CHANGES. Generated method signatures and bodies are byte-identical for regular vs functioned arrows. |
| `example/schema.zed` | Add 2 fixture relations on `extsvc/folder`: `any_via` (using `.any(browse)`) and `all_via` (using `.all(browse)`). Define corresponding parent relations (`any_parent`, `all_parent` of type `folder`). |
| `example/authzed/extsvc/folder.gen.go` | Regenerated — new `Check<Perm>` / `Lookup<Perm>...` methods for the new permissions. Existing methods byte-identical to v1.10.0. |
| `example/authzed/extsvc/extsvc_test.go` | Add e2e tests covering `.any()` (single-parent grant succeeds; equivalent to regular arrow) and `.all()` (two-parent setup; both must approve, omitting one denies). |

E2E tests verify SpiceDB's `.all()` strict-intersection semantic against live SpiceDB:
1. Write 2 parent folders, both granting browse to user u1 → Check `all_via` for u1 → granted
2. Same setup but only 1 parent grants → Check `all_via` for u1 → denied
3. Write 1 parent granting browse to u1 → Check `any_via` for u1 → granted (sanity)

---

## History

(History is owned by `harness history-update` — do not hand-edit.)
