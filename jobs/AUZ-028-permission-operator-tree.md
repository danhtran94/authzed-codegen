<!-- approved -->
# AUZ-028: Permission operator tree, arrow function, caveat types, never-granted field comments

| Field      | Value                                          |
|------------|------------------------------------------------|
| Status     | Done                                           |
| Created    | 2026-11-18                                     |
| Assignee   | Claude                                         |
| Source     | docs/scope-permission-operator-tree.md         |
| Blocked by | —                                              |

## Goal

Implement every Success Criterion of `docs/scope-permission-operator-tree.md` (Accepted on #16):

- `PermissionView.Rewrite *PermissionNode` is the single lowering result, and `Expressions` is derived from it by `flattenPermissionNode`.
- `PermissionExpr.ArrowFunc` records `""` / `"any"` / `"all"`.
- `ParamSpec.CaveatType` carries the declared SpiceDB type, and the stale `ParamSpec` doc comment is fixed.
- A new `grantableInputTypes` analysis computes operator-aware, least-fixpoint grantability.
- `Check<Res><Perm>Inputs` fields outside the grantable set get a `// <Field> is never granted <perm>:` doc comment.

Generated code changes only in comments, so this is a Minor release.

## Problem

See the scope note's Problem section. In short, the views drop operator structure, the arrow function and declared caveat types. As a result, `CheckArticleAuthorOnlyInputs` / `CheckArticleEditorInputs` expose `Group` / `Role` fields that SpiceDB always denies, with no hint to the caller.

## Solution

    Before:
      lowerUsersetRewrite(rw)       → []PermissionExpr (flat, operator discarded)
      lowerSetOperationChild(child) → []PermissionExpr
    After:
      lowerRewriteNode(rw)          → *PermissionNode{Op, Children}           ← single lowering
      lowerChildNode(child)         → *PermissionNode{Leaf} | lowerRewriteNode(...)
      lowerUsersetRewrite / lowerSetOperationChild = flattenPermissionNode(node)  (signatures unchanged)
      PermissionView{Name, Expressions: flatten(Rewrite), Rewrite}

### Components

- **`PermissionNode`, `PermOp*`, `flattenPermissionNode`** (`internal/generator/adapter.go`)
- **`PermissionExpr.ArrowFunc`** (`adapter.go`): set from `FunctionedTupleToUserset.Function`
- **`ParamSpec.CaveatType`, `caveatTypeName`** (`adapter.go`): canonical `name<child, ...>`
- **`grantableInputTypes`** (new `internal/generator/grantable.go`): an **over-approximation** of the subject types SpiceDB may grant, so a "never granted" comment is only emitted when it is certain. Relations count their direct types *and* the members of userset allowed types; identifiers resolve relations or permissions; arrows traverse every namespace the left relation allows (userset entries included) and target a relation *or* a permission; `_self` is the own type. Union = ∪, intersection = ∩, exclusion = first child. Least fixpoint by iteration from ∅ over every `(type, relation|permission)` key. *(Revised after review of #18: the first version reused the input-field resolver's leaf rules, which under-approximate and caused false comments under `&`; see Discoveries.)*
- **FuncMap `neverGranted`** (`internal/generator/generator.go`) and **template comment** (`internal/templates/object.go.tmpl`, `Check<Res><Perm>Inputs` struct only)

### Why not alternatives

| Approach | Verdict |
|---|---|
| **Tree is the lowering; flat list derived (chosen)** | One lowering path; existing helpers and tests keep their signatures |
| Lower twice (flat + tree) | Two sources of truth for the same schema, which the scope explicitly forbids (SC2) |
| Template walks `Rewrite` itself | Fixpoint logic in `text/template` is unreadable; violates SC8 |

## Workstreams

### 1. Views (adapter)

| # | Task | File | Status |
|---|------|------|--------|
| 1.1 | Add `PermOp`, `PermissionNode`, `flattenPermissionNode`; add `lowerRewriteNode` / `lowerChildNode`; make `lowerUsersetRewrite` / `lowerSetOperationChild` thin flatten wrappers; set `PermissionView.Rewrite` | `internal/generator/adapter.go` | [x] |
| 1.2 | `PermissionExpr.ArrowFunc` from `FunctionedTupleToUserset` (`FUNCTION_ANY` → `"any"`, `FUNCTION_ALL` → `"all"`) | same | [x] |
| 1.3 | `ParamSpec.CaveatType` via `caveatTypeName`; fix the `ParamSpec` doc comment | same | [x] |

### 2. Analysis + template

| # | Task | File | Status |
|---|------|------|--------|
| 2.1 | `grantableInputTypes(defs []*DefinitionView) map[string][]string` (over-approximating leaf rules, see Components) | `internal/generator/grantable.go` | [x] |
| 2.2 | FuncMap `neverGranted(objectType, perm, inputType) bool` = in `permissionInputTypes` and not in grantable | `internal/generator/generator.go` | [x] |
| 2.3 | Emit `// <Field> is never granted <perm>: …` above such fields in `Check<Res><Perm>Inputs` | `internal/templates/object.go.tmpl` | [x] |
| 2.4 | Regenerate; confirm no generated `func` / `type` line changes (`git diff example/authzed` piped to `grep -E '^[-+](func\|type) '` is empty) and only comment lines are added | `example/authzed/**/*.gen.go` | [x] |

### 3. Tests

| # | Task | File | Status |
|---|------|------|--------|
| 3.1 | Tree test `(a + b) - c` → `Exclusion[Union[a, b], c]`; `ArrowFunc` assertions in the two functioned-arrow tests | `internal/generator/adapter_test.go` | [x] |
| 3.2 | `CaveatType` for every caveat param in `example/schema.zed` (incl. `client_ip == "ipaddress"`, `rows == "list<list<string>>"`) | same | [x] |
| 3.3 | Fixture: the never-granted set (`permissionInputTypes` minus grantable) is exactly `article/author_only` and `article/editor` → `{group, role}`; `folder/ancestor_or_self` includes `extsvc/folder`; `all_via` == `any_via`. Synthetic: `&` / `-` / `+` rules, and no false comment for a userset operand, an arrow over a userset-typed relation, or an arrow targeting a relation | `internal/generator/generator_test.go` (new) | [x] |
| 3.4 | E2E: `TestArticle_{AuthorOnly,Editor}_NeverGrantedFields_Denied`, which are negative checks: group and role hold `browse` on the parent, yet checks through `Group` / `Role` return `authz.ErrPermissionDenied`. Plus `TestArticle_AuthorOnly_ExclusionOnUser` / `TestArticle_Editor_IntersectionOnUser`, which show the operators' effect on `User` | `example/authzed/extsvc/extsvc_test.go` | [x] |
| 3.5 | `TestGrantable_AgreesWithSpiceDB`: cross-check against a SpiceDB testcontainer. Every subject SpiceDB grants (userset operand, arrow via a userset-typed relation, arrow into a relation, exclusion) has its type in `grantableInputTypes` and no comment, and the only commented set is the genuinely disjoint `allowed & groups` | `internal/generator/grantable_e2e_test.go` | [x] |

### 4. Verification

| # | Task | Status |
|---|------|--------|
| 4.1 | `go build`, `go vet`, `gofmt -l .`, `go mod tidy` clean; round-trip zero-diff | [x] |
| 4.2 | CI-pinned `golangci-lint` v2.15.0, `govulncheck` v1.9.0 (linux/amd64) clean | [x] |
| 4.3 | Full `go test ./... -count=1` | [x] |
| 4.4 | SC8: `grep -n "\.Rewrite\|ArrowFunc\|CaveatType" internal/templates/*.tmpl` returns nothing | [x] |

### 5. Documentation

| # | Task | File | Status |
|---|------|------|--------|
| 5.1 | `## [2.3.0] - Unreleased`: Added (views, comments) | `CHANGELOG.md` | [x] |
| 5.2 | Fix the stale "Codegen scope" list (caveats / expiration / sub-relations / `_self` / functioned arrows are supported) and mention the views' tree | `.claude/CLAUDE.md` | [x] |

## Design Decisions

### Comment only `Check<Res><Perm>Inputs` fields

That struct is the one callers populate. `Lookup*Resources` takes the same struct, so the comment covers both. Per the scope's Out of Scope, `Lookup<Perm><Type>Subjects` methods and caveat fields are left untouched.

## What Stays Unchanged

- Every generated `func` / `type` declaration, and every `pkg/authz` API.
- `resolvePermissionExpressionTypes`, `resolveTransitive`, `walkPermCaveats`, `walkPermUsersets`, which keep consuming `Expressions`.

## Implementation Order

    1. WS1 (views)              ← analysis reads Rewrite; flatten must keep Expressions identical
    2. WS2 (analysis/template)  ← needs Rewrite from WS1
    3. WS3 (tests)              ← exercise WS1 + WS2 output
    4. WS4 (verification)
    5. WS5 (docs)

## Notes

- Owner approval: the scope was Accepted on #16, and the owner said "làm đi / tiếp tục" (2026-11-18) before stepping away. The PR is opened for review and **not** merged until the owner returns.

## Discoveries & Decisions During Implementation

### [Implementer] Flatten leaves the fixture output byte-identical

After WS1 alone, regenerating `example/authzed` produced **zero diff**. That establishes that every file emitted for the fixture is unchanged. It does not by itself prove `Expressions` equality for arbitrary schemas: the structural argument is that `flattenPermissionNode` is a pre-order walk that visits leaves in the same order the old recursive loop appended them, and the AUZ-025 lowering tests still pass unmodified through the `lowerUsersetRewrite` / `lowerSetOperationChild` flatten views.

### [Implementer] Generated diff: 4 comments, plus gofmt realignment

The only generated change is `extsvc/article.gen.go`: one comment on each of `Group` and `Role` in `CheckArticleEditorInputs` and `CheckArticleAuthorOnlyInputs`. This matches scope SC6, and `folder.gen.go` gets none. The diff also shows `User  []User` → `User []User`: inserting a comment line splits gofmt's field-alignment block. That change is whitespace only, with no `func` / `type` line changes.

### [Implementer] What the e2e tests establish

The `*_NeverGrantedFields_Denied` tests are **negative checks** for the documented fields. Group and role hold `browse` on the parent, yet checks through `Group` / `Role` are denied. They would also pass if the permission were plain `author`, so they do not isolate the operator. The `*_ExclusionOnUser` / `*_IntersectionOnUser` tests do: an author who can browse the parent is denied `author_only` but granted `editor`, and an author who cannot is granted `author_only` but denied `editor`.

### [Implementer] Test helper for the fixture schema

`internal/generator/fixture_test.go` (`adaptFixtureSchema`) compiles `example/schema.zed` the same way `cmd/authzed-codegen` does. The `CaveatType` and grantability tests use it, so they assert against the real fixture rather than hand-built protos.

### [Reviewer] Verification summary

`go build`, `go vet`, `gofmt -l .` (empty), `go mod tidy` (no diff), round-trip (zero diff), SC8 grep (empty), `golangci-lint` v2.15.0 (0 issues), `govulncheck` v1.9.0 linux/amd64 (none), and `go test ./... -count=1` (all pass, including the 2 new e2e tests against SpiceDB).

### [Reviewer] CodeRabbit on #18: under-approximating leaves caused false comments under `&`

The first version reused the input-field resolver's leaf rules. That resolver lists *fields to emit*, so it ignores userset allowed types (`relationFromView` drops them), arrows whose target is a relation, and arrow traversal through userset-typed relations. Under union that only loses fields. Under intersection, a missing leaf type empties the operand and marks a genuinely grantable type "never granted". Example: `view = allowed & shared` with `shared: team#member` would comment `User` even though SpiceDB grants members of the team.

The analysis now over-approximates (see Components). A type is excluded only when no rule can produce it, and the three reviewed shapes are pinned as unit tests (`TestGrantable_NoFalseNeverGranted`). The fixture's never-granted set is unchanged: exactly the four article fields. The comment text was also corrected for intersections with three or more operands ("misses at least one operand … that every grant must satisfy").

### [Reviewer] SpiceDB confirms the review scenarios (owner asked, 2026-11-18)

The scenarios were first checked with a scratch test against a SpiceDB container. SpiceDB **grants** a user through `allowed & shared` (`shared: team#member`), `allowed & parent_us->browse` (`parent_us: team#member`), and `allowed & parent->viewer` (`viewer` a relation), and denies a user who is only `allowed`. The pre-review generator (`4106b22`) emitted a false "never granted" comment on `User` for all three; `700e67c` emits none.

This also settles a previously unverified question: SpiceDB arrows **do** walk userset-typed subjects. The scratch check is now permanent as task 3.5 (`TestGrantable_AgreesWithSpiceDB`), which asserts SpiceDB's verdict and the analysis's agreement with it.

### [Owner action] Not merged

Per the Notes, PR left open for the owner's review.
