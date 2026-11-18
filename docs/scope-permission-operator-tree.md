# Scope: Permission operator tree, arrow function and declared caveat types in the generator views

| Field   | Value      |
|---------|------------|
| Status  | Accepted   |
| Created | 2026-11-18 |
| Author  | Danh Tran  |

---

## Problem

The generator views in `internal/generator/adapter.go` flatten away three facts the schema states. The current templates never needed them; the experimental `studio` package (ADR-007) does, and so do generated doc comments.

**1. Operator structure.** `PermissionView.Expressions` is a flat `[]PermissionExpr` (`adapter.go:101-111`). `lowerUsersetRewrite` treats union, intersection and exclusion identically: it picks whichever `SetOperation` is set and lowers its children into one list (per A1). Since AUZ-025, nested rewrites contribute every branch, but the operator that combined them is still discarded. Nothing downstream can tell `a + b` from `a & b` or `a - b`.

**2. Arrow function.** `FunctionedTupleToUserset` (`parent.any(view)` / `parent.all(view)`) lowers to the same `PermExprArrow` as a plain `parent->view`, so `FUNCTION_ALL` is lost (per A2).

**3. Declared caveat types.** `ParamSpec` carries only `Name` and `GoType` (`adapter.go:96-99`). `GoType` is Go syntax for templates (`*string`, `[]any`), not the SpiceDB type. `ipaddress` becomes `*string`; `map<T>` and lists of non-scalar elements become `any` / `[]any` (per A3). The `ParamSpec` doc comment (`adapter.go:91-95`) is stale: it claims `duration` and `timestamp` fall back to `any`, but `caveatTypeToGo` has emitted `*time.Duration` / `*time.Time` since AUZ-018 (per A3).

**Visible consequence today: generated input structs carry fields that SpiceDB can never grant, with nothing telling the caller.** The resolver computes `Check<Perm>Inputs` fields as the union of every branch, whatever the operator. On the existing fixture:

- `extsvc/article.author_only = author - parent->browse` generates `CheckArticleAuthorOnlyInputs{User, Group, Role}`.
- `extsvc/article.editor = author & parent->browse` generates `CheckArticleEditorInputs{User, Group, Role}`.

In both, `author` allows only `extsvc/user` (per A4). `Group` and `Role` are reachable only through the excluded side of `-`, or through a single arm of `&`, so every check through those fields is denied.

Removing such fields would break callers' code, a generated-signature change outside the correctness-fix exception. This scope therefore **documents** them and does not remove them.

## Success Criteria

1. `PermissionView` gains `Rewrite *PermissionNode` (`internal/generator/adapter.go`). `PermissionNode` has `Op` (one of `PermOpUnion`, `PermOpIntersection`, `PermOpExclusion`), `Children []*PermissionNode`, and `Leaf *PermissionExpr`; exactly one of `Leaf` or (`Op` + `Children`) is set. Verifiable: a unit test in `internal/generator/adapter_test.go` lowers `(a + b) - c` and asserts the tree `Exclusion[Union[a, b], c]`, with children in schema order.

2. `PermissionView.Expressions` is derived by flattening `Rewrite` (pre-order, leaves only), not produced by a separate lowering path. Verifiable: `grep -n "func flattenPermissionNode" internal/generator/adapter.go` returns one match. After the change, `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed` produces no change to any `func`/`type` line in `example/authzed/**/*.gen.go` (`git diff example/authzed | grep -E '^[-+](func|type) '` is empty).

3. `PermissionExpr` gains `ArrowFunc string`: `""` for `->`, `"any"` for `.any()`, `"all"` for `.all()`. Verifiable: the existing `TestLowerSetOperationChild_FunctionedTUSeAll_MapsToPermExprArrow` additionally asserts `ArrowFunc == "all"`, and the `.any()` test asserts `"any"`.

4. `ParamSpec` gains `CaveatType string`, the declared SpiceDB type in canonical form (`"ipaddress"`, `"list<timestamp>"`, `"map<int>"`, `"list<list<string>>"`). The stale doc comment at `adapter.go:91-95` is corrected. Verifiable: a unit test in `internal/generator/adapter_test.go` asserts `CaveatType` for every caveat parameter in `example/schema.zed`, including `extsvc/from_subnet.client_ip == "ipaddress"` and `extsvc/matrix_check.rows == "list<list<string>>"`.

5. A new function `grantableInputTypes(objectType, perm string) []string` (`internal/generator/generator.go`) walks `Rewrite` with operator-aware rules:
   - union = set union of children;
   - intersection = set intersection of children;
   - exclusion = left child only;
   - leaves resolve exactly as the existing resolver does.

   > **Erratum (AUZ-028 review, 2026-11-18):** reusing the resolver's leaf rules under-approximates, because the resolver ignores usersets and relation-targeting arrows. Under `&` that produces false "never granted" comments. The implementation instead uses over-approximating leaf rules (`internal/generator/grantable.go` doc comment), so the subset invariant above no longer holds by construction. SC6 is enforced by asserting the exact never-granted set on the fixture.

   Recursion through arrows and permission references is a least fixpoint. Verifiable: a unit test in `internal/generator/generator_test.go` asserts that for every permission in `example/schema.zed`, `grantableInputTypes ⊆ permissionInputTypes`. It also asserts `grantableInputTypes("extsvc/article", "author_only") == ["extsvc/user"]` and the same for `"editor"`.

6. For each input type in `permissionInputTypes \ grantableInputTypes`, `internal/templates/object.go.tmpl` emits a doc comment on that field in `Check<Res><Perm>Inputs`. The comment begins with the literal `// <Field> is never granted <perm>:`. Verifiable: `grep -n "is never granted author_only:" example/authzed/extsvc/article.gen.go` returns exactly 2 matches (`Group`, `Role`), and the same for `editor`. `grep -c "is never granted" example/authzed/extsvc/folder.gen.go` returns 0, since no folder permission has such a field today (per A4).

7. E2E proof that every commented fixture field really is never granted: `TestArticle_AuthorOnly_NeverGrantedFields_Denied` and `TestArticle_Editor_NeverGrantedFields_Denied` in `example/authzed/extsvc/extsvc_test.go` write tuples that grant `browse` on the parent folder to a group and a role, then assert `CheckAuthorOnly` / `CheckEditor` with those `Group` / `Role` inputs return `authz.ErrPermissionDenied`.

8. Templates do not read the new fields except for the comment in SC6. Verifiable: `grep -n "\.Rewrite\|ArrowFunc\|CaveatType" internal/templates/*.tmpl` returns no matches, because SC6 receives the never-granted set through a FuncMap helper, not by walking the tree in the template.

9. `go build ./...`, `go vet ./...` and `go test ./... -count=1` exit 0. The round-trip `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed && git diff --quiet example/authzed/` exits 0 after the regenerated fixture is committed.

## Out of Scope

- **Removing or narrowing never-granted fields from input structs.** That breaks callers' code, so it needs a major bump or its own ADR. This scope only documents the fields.
- **Caveat fields on excluded branches.** A caveat on the excluded side still affects the result (a conditional exclusion makes the outcome conditional), so `Caveats` sub-struct fields are not "never used" and get no comment.
- **`Lookup<Perm><Type>Subjects` methods for never-granted types.** They keep being generated unchanged. Commenting or suppressing them is a follow-up if needed.
- **OPA builtin declarations and runtime behavior.** No change.
- **Any `studio/` package code.** This scope only extends the views `studio` will read (ADR-007).
- **Userset-aware `Lookup<Perm><Resource>Resources` (AUZ-027).** Separate job.
- **Changing `GoType` mappings**, e.g. `ipaddress` → `netip.Addr`. That changes generated signatures; `CaveatType` is additive information only.

## Risks

- **The operator-aware resolver diverges from the existing resolver on leaf rules, so a comment wrongly marks a grantable field.** Mitigation: `grantableInputTypes` reuses the existing leaf resolution (`resolvePermissionExpressionTypes`' identifier / arrow / `_self` cases) and replaces only the combination step. SC5's subset test fails on any type the new function invents. SC7's e2e test proves each commented fixture field is actually denied by SpiceDB.
- **A false "never granted" comment on a user schema shape the fixture doesn't cover.** Mitigation: the comment states the structural reason, not just the verdict. Add property-style unit tests for wildcard (`type:*`), userset (`team#admin`), `_self` and `.all()` leaves under each operator before the template change lands.
- **The tree grows the views for a non-template consumer** (ADR-007 exit condition 3). Mitigation: SC8 keeps templates off the tree; if a later template needs `Rewrite`, re-evaluate ADR-007's exit conditions first.
- **Fixpoint non-termination on recursive permissions** (`ancestor_or_self = self + parent_for_self->ancestor_or_self`). Mitigation: iterate over the finite set of `(objectType, perm)` pairs until no set changes; every operator rule is monotone, so this terminates (per A5). Add a unit test on `extsvc/folder.ancestor_or_self`.

## Assumptions

- **A1 [VERIFIED]:** `lowerUsersetRewrite` selects `GetUnion()`, else `GetIntersection()`, else `GetExclusion()`, and lowers children identically. Evidence: `internal/generator/adapter.go` `lowerUsersetRewrite` ("Intersection and exclusion are structurally identical to union for codegen").
- **A2 [VERIFIED]:** `*core.SetOperation_Child_FunctionedTupleToUserset` lowers to `PermissionExpr{Kind: PermExprArrow, LeftRel, RightPerm}` without the function value. Evidence: `internal/generator/adapter.go` `lowerSetOperationChild`, FunctionedTupleToUserset case.
- **A3 [VERIFIED]:** `caveatTypeToGo` maps `duration` → `*time.Duration`, `timestamp` → `*time.Time`, `ipaddress` → `*string`, and `map` / unmapped → `any`; `caveatTypeToGoElem` maps non-scalar list elements to `any`. The `ParamSpec` doc comment contradicts the first two. Evidence: `internal/generator/adapter.go:396-470`, `:91-95`.
- **A4 [VERIFIED]:** On `v2.1.0`, `CheckArticleAuthorOnlyInputs` and `CheckArticleEditorInputs` are `{User, Group, Role}`; `extsvc/article.author` allows only `extsvc/user`; `CheckFolderEliteAccessInputs` and `CheckFolderScoredMinusTokenInputs` are `{User, Caveats}`. Evidence: `example/authzed/extsvc/article.gen.go`, `folder.gen.go`, `example/schema.zed` (`extsvc/article`). Inspected 2026-11-18.
- **A5 [EXTERNAL FACT]:** A monotone function on a finite powerset lattice reaches its least fixpoint by iteration from the empty set (Knaster–Tarski / Kleene). Union, intersection and "left only" are monotone in every argument. Evidence: https://en.wikipedia.org/wiki/Kleene_fixed-point_theorem.
- **A6 [HYPOTHESIS]:** At the type level, `.all(perm)` can grant exactly the same subject types as `.any(perm)`; the difference is data-level, because every parent must grant. Verification deferred to the SC5 unit test on `extsvc/folder.all_via` plus an e2e assertion in the job doc.
- **A7 [HYPOTHESIS]:** The least fixpoint of the operator-aware rules equals the set of subject types SpiceDB can grant for some relationship data. Verification: SC7 e2e tests for the fixture's never-granted fields; broader schemas are covered only by the Risks mitigations.

## History

| Ticket    | Title                                                                                     | Created    | Status   |
|-----------|-------------------------------------------------------------------------------------------|------------|----------|
| (initial) | Permission operator tree, arrow function and declared caveat types in the generator views | 2026-11-18 | Accepted |
