# AUZ-016: Functioned Tuple-to-Userset

| Field      | Value                                              |
|------------|----------------------------------------------------|
| Status     | Done                                                |
| Created    | 2026-05-09                                         |
| Assignee   | danhtran94                                         |
| Source     | docs/spec-011-functioned-tuple-to-userset.md       |
| Blocked by | —                                                  |

<!-- approved -->

---

## Goal

Lift the codegen's adapt-time rejection of `FunctionedTupleToUserset` so schemas using `.any()` / `.all()` arrow function syntax compile. `.any()` is semantically equivalent to the regular arrow `parent->view` — accepting it removes a syntactic-only rejection. `.all()` is the genuinely-new strict-intersection semantic (subject must reach the inner permission via EVERY parent row, not just any one) — used in dual-control / multi-approver / cross-region patterns. Both produce byte-identical generated code; the function value is server-side semantic that SpiceDB enforces at Check time. After this job, the codegen accepts every commonly-used SpiceDB schema construct except the rare `_self` reflexive case.

## Problem

    Current (post-v1.10.0):
      caller declares `permission deploy = approver_pool.all(approved)` in schema
        → adapter `lowerSetOperationChild` errors:
            "permission %q: functioned tuple-to-userset (with self/expiration)
             is not supported"
              → codegen exits before any output is written ✗
      caller cannot use SpiceDB's modern arrow function syntax
        → must rewrite as legacy `parent->view` (loses `.all()` semantic
          entirely; cannot express strict-intersection at the schema level
          without it) ✗

The rejection is symmetric to AUZ-011's pre-v1.5 rejection of sub-relation references — a real SpiceDB feature blocked at adapt time. SpiceDB has emitted `FunctionedTupleToUserset` for any arrow-with-function since v1.40. Schemas that use the syntax (compliance / multi-approver / cross-region patterns) currently can't be consumed by this codegen.

## Solution: Parallel adapter case to TupleToUserset

    After fix:
      caller declares: permission deploy = approver_pool.all(approved)
        → adapter accepts; lowerSetOperationChild handles
          GetFunctionedTupleToUserset() identically to GetTupleToUserset():
            return PermExprArrow{LeftRel: "approver_pool", RightPerm: "approved"}
          → downstream consumers (perm tree resolver, template arrow walker)
            unchanged — they key on PermExprArrow + LeftRel/RightPerm
          → generated Check<Perm> / Lookup<Perm> methods byte-identical to
            the equivalent regular-arrow form ✓
      caller can write SpiceDB's modern arrow syntax without restriction ✓

The function value (`FUNCTION_ANY` / `FUNCTION_ALL`) is read by the adapter but not stored or propagated. Both functions produce identical Go output because the semantic difference is enforced server-side by SpiceDB at Check time.

### Components

**`lowerSetOperationChild`** — adapter switch case extended. New `case c.GetFunctionedTupleToUserset() != nil:` branch parallel to the existing TupleToUserset branch. Extracts `tupleset.relation` and `computed_userset.relation` into `PermExprArrow`.

**Schema fixture additions** — two new permission relations on `extsvc/folder`:
- `any_parent` + `permission any_via = any_parent.any(browse)` — explicit `.any()` form
- `all_parent` + `permission all_via = all_parent.all(browse)` — strict-intersection form

**E2E tests** — verify `.all()` semantic vs `.any()` semantic against live SpiceDB.

### Why not alternatives

| Approach | Verdict |
|---|---|
| **Adapter-only change** (chosen) | Minimal scope. Function value invisible to codegen output; SpiceDB enforces the semantic. ~10 line change. |
| Add new `PermExprFunctionedArrow` kind | Rejected. Downstream consumers would need to handle two arrow kinds; cosmetic complexity for no behavior difference. |
| Surface function in generated Go (e.g. method name suffix) | Rejected. Generated method signatures should reflect the API (Check / Lookup) not the schema's internal representation. |
| Keep rejecting `.all()` only, accept `.any()` | Rejected. Both are valid SpiceDB syntax; rejecting one is an arbitrary half-measure that confuses schema authors. |

## Workstreams

### 1. Adapter — accept FunctionedTupleToUserset

Lift the rejection. Map to `PermExprArrow` like regular `TupleToUserset`.

| #   | Task | File | Status |
|-----|------|------|--------|
| 1.1 | Replace the `case c.GetFunctionedTupleToUserset() != nil:` rejection (`adapter.go:614`) with a parallel branch to `GetTupleToUserset()`: extract `fttu.GetTupleset().GetRelation()` → `LeftRel` and `fttu.GetComputedUserset().GetRelation()` → `RightPerm`; return `PermissionExpr{Kind: PermExprArrow, ...}` | `internal/generator/adapter.go` | [x] |
| 1.2 | Unit test: schema with `permission p = parent.any(view)` adapts cleanly to `PermExprArrow{LeftRel: "parent", RightPerm: "view"}` | `internal/generator/adapter_test.go` | [x] |
| 1.3 | Unit test: schema with `permission p = parent.all(view)` adapts to the same `PermExprArrow` shape (function value not stored) | same | [x] |

**Key details:** Per SPEC-011 A1 — downstream consumers (`collectPermCaveats`, `collectPermUsersets`, template arrow walker) key on `PermExprArrow` + `LeftRel/RightPerm`; no propagation needed. Per A2 — `compiler.Compile()` rejects `FUNCTION_UNSPECIFIED`; codegen never sees invalid function values.

### 2. Schema fixture additions

Add minimal fixture exercising both function forms. Living on `extsvc/folder` keeps it co-located with existing arrow fixtures.

| #   | Task | File | Status |
|-----|------|------|--------|
| 2.1 | Add `relation any_parent: extsvc/folder` + `permission any_via = any_parent.any(browse)` to `extsvc/folder` definition | `example/schema.zed` | [x] |
| 2.2 | Add `relation all_parent: extsvc/folder` + `permission all_via = all_parent.all(browse)` to same definition | same | [x] |
| 2.3 | Add combination fixture: `relation gated_parent: extsvc/folder with extsvc/tenant_match` + `permission gated_all_via = gated_parent.all(browse)` — exercises `.all()` reaching a caveated LeftRel; verifies caveat collection extends to functioned arrows | same | [x] |
| 2.4 | Add mixed-expression fixture: `relation direct_member: extsvc/user` + `permission mixed_all = direct_member + all_parent.all(browse)` — exercises functioned arrow combined with a regular identifier in the same permission expression | same | [x] |
| 2.5 | Run codegen — `go run ./cmd/authzed-codegen --output example/authzed example/schema.zed` — commit regenerated `folder.gen.go` (new methods for `any_via` / `all_via` / `gated_all_via` / `mixed_all`) | `example/authzed/extsvc/folder.gen.go` | [x] |

**Key details:** Per SPEC-011 A3 — no `use` flag required for `.any()` / `.all()` syntax. Per C3 — generated `Check<Perm>` / `Lookup<Perm>` methods for `any_via` / `all_via` are byte-identical structurally to the equivalent regular-arrow form (only the permission name differs).

### 3. E2E tests — verify `.all()` strict intersection

Test `.all()` strict-intersection vs `.any()` union semantics against live SpiceDB.

| #   | Task | Status |
|-----|------|--------|
| 3.1 | E2E: `.any()` single-parent grant — write 1 `any_parent` tuple granting browse to u1; Check `any_via` with u1 → granted (regression check; identical to regular-arrow behavior) — `example/authzed/extsvc/extsvc_test.go` | [x] |
| 3.2 | E2E: `.all()` two-parent both grant — write 2 `all_parent` tuples; both grant browse to u1 (via viewer relations on each parent); Check `all_via` with u1 → granted | [x] |
| 3.3 | E2E: `.all()` two-parent only one grants — write 2 `all_parent` tuples; ONLY ONE parent grants browse to u1; Check `all_via` with u1 → denied (proves strict intersection) | [x] |
| 3.4 | E2E: `.all()` zero parents — folder has no `all_parent` tuples; Check `all_via` with u1 → denied (vacuous case; no parents = no grant) | [x] |
| 3.5 | E2E combination: `.all()` + caveat — `gated_all_via` permission with caveated LeftRel; `CheckGatedAllViaInputs` exposes the `Caveats.TenantMatch` sub-struct (caveat collection works through functioned arrows); Check with matching tenant + correctly-granted parents → granted | [x] |
| 3.6 | E2E combination: `.all()` + caveat false → deny — same `gated_all_via` setup but Check supplies wrong tenant; SpiceDB evaluates caveat false → denied (orthogonal to the .all() semantic; both must hold) | [x] |
| 3.7 | E2E combination: mixed expression — `mixed_all` permission combining direct identifier with `.all()` arrow; write `direct_member` tuple for u1 (only) → Check grants via the direct path, even when `.all()` arrow side has zero parents | [x] |
| 3.8 | E2E combination: mixed expression `.all()` path — write `direct_member` for u2 (NOT u1); set up `all_parent` tuples for f1 with both granting browse to u1; Check `mixed_all` with u1 → granted via the .all() path even though direct path doesn't apply | [x] |
| 3.9 | E2E: regression sweep — full e2e suite passes after WS1+WS2 — `go test ./pkg/authz/spicedb/... ./example/authzed/...` | [x] |

**Key details:** `.all()` evaluates against SpiceDB's permission evaluator at Check time; the codegen's role is just to feed the right wire request. Tests assert SpiceDB's behavior, not codegen-output shape.

### 4. Documentation + release prep

CHANGELOG, README, version bump.

| #   | Task | Status |
|-----|------|--------|
| 4.1 | Add `[1.11.0]` entry to `CHANGELOG.md` documenting `FunctionedTupleToUserset` acceptance, no API surface change, schema fixture additions, and the `.all()` use case — `CHANGELOG.md` | [x] |
| 4.2 | Update `README.md` Schema Support table — add row for `Functioned arrows (.any() / .all())` marked ✓ — `README.md` | [x] |
| 4.3 | Tag `v1.11.0` after merge; create GitHub release with notes calling out the dual-control / multi-approver use case | [x] |

## Design Decisions

### Adapter-only change; no template / runtime touch
Function value is server-side semantic. SpiceDB's evaluator enforces `.all()` strict-intersection vs `.any()` union when processing CheckPermission requests. The codegen's job is to walk the arrow's structure for input-type resolution; the generated `Check<Perm>` / `Lookup<Perm>` methods don't differ between regular arrows, `.any()` arrows, and `.all()` arrows.

### Reuse `PermExprArrow` kind
A new `PermExprFunctionedArrow` kind would force every downstream consumer (`collectPermCaveats`, `collectPermUsersets`, template arrow walker) to handle two cases for the same wire-level operation. The function value is invisible to all of them; one kind suffices.

### Function value not stored
The codegen's `PermissionExpr` struct doesn't gain a `Function` field. Callers wanting to introspect "is this permission strict-intersection?" use SpiceDB's `ReflectSchema` (out of scope here). The codegen's role is type-safe Go bindings; the schema's evaluation semantic is SpiceDB's domain.

### `.any()` accepted even though semantically redundant
Schemas using `.any()` syntax explicitly exercise the same codegen path as regular arrows. Rejecting `.any()` would force authors to rewrite as `parent->view` for the codegen to accept their schema — arbitrary friction. Accepting both forms means schemas authored with the modern syntax round-trip cleanly.

## What Stays Unchanged

- All existing `Engine.*` method signatures
- `internal/templates/object.go.tmpl` — no template change
- `internal/generator/generator.go` — no helper additions
- Per-namespace generated `.gen.go` files (except `folder.gen.go` which gains methods for the two new permissions)
- Codegen idempotency — schemas without functioned TTU regenerate byte-identical to v1.10.0
- All existing fixtures — no modifications
- `pkg/authz/spicedbtest/` test harness — unchanged
- README sections on Caveats / Expiration / Sub-relation References / Conditional Permission / Consistency / Schema Drift / Versioning

## Implementation Order

    1. WS1 Adapter            ← single switch case + unit tests
    2. WS2 Schema fixture     ← depends on WS1 (regenerated output requires accepting adapter)
    3. WS3 E2E tests           ← depends on WS2 (fixture in place)
    4. WS4 Docs + release      ← last; depends on test pass

WS1 + WS2 land in one commit (atomic — adapter accepts AND fixture uses the construct). WS3 follows. WS4 closes.

## Notes

- Round-trip the example fixture before declaring any generator change done. Per `.claude/CLAUDE.md`.
- Full e2e suite must pass: `go test ./pkg/authz/spicedb/... ./example/authzed/...`.
- Version bump is `1.11.0` (minor). Pure additive — accepts new schema construct; no breaking changes.
- Per v1.10 versioning policy — additive features go through minor bumps.
- `harness validate-pr-checklist` will hard-block a push with `Status=Done` while any task row is `[ ]`.

## Discoveries & Decisions During Implementation

### [Implementer] No discoveries this session

WS1-WS4 proceeded exactly as planned. SPEC-011's design picks held: adapter-only change in 12 lines (vs the 1-line rejection it replaced); zero template change; per-namespace `.gen.go` files for definitions without functioned-TTU usage stayed byte-identical to v1.10.0. The 2 unit tests for `.any()` / `.all()` mapping to `PermExprArrow` passed first-try; the 8 e2e tests (including 4 combination scenarios per the user's request) passed first-try.

The combination tests confirmed `walkPermCaveats` arrow-handling extends to `FunctionedTupleToUserset` without modification — `gated_all_via` permission generated `CheckFolderGatedAllViaCaveats` correctly because the existing arrow walker keys on `PermExprArrow` + `LeftRel/RightPerm`, not on the wire-level proto subtype (per SPEC-011 A1). No surprise; no fix needed.

### [Implementer] Schema fixture additions exercised the existing tree resolver

The mixed-expression fixture (`mixed_all = direct_member + all_parent.all(browse)`) verified that the union operator + identifier + functioned arrow compose correctly. The Check method body for `mixed_all` routes through the standard "iterate input types, call CheckPermission per type" pattern — the `.all()` semantic doesn't surface as new Go control flow; it's purely server-side. This was the key validation that v1.11 doesn't accidentally need a template change.
