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
- **`grantableInputTypes`** (new `internal/generator/grantable.go`): same leaf rules as `resolvePermissionExpressionTypes` / `resolveTransitive` (relation → direct non-userset types; permission identifier → recurse on own type; arrow → recurse on each referenced type that has the right permission; `_self` → own type). Union = ∪, intersection = ∩, exclusion = first child. Least fixpoint by iteration from ∅ over all `(type, perm)` keys.
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
| 2.1 | `grantableInputTypes(defs []*DefinitionView, byTypes DefinitionsByTypes) map[string][]string` | `internal/generator/grantable.go` | [x] |
| 2.2 | FuncMap `neverGranted(objectType, perm, inputType) bool` = in `permissionInputTypes` and not in grantable | `internal/generator/generator.go` | [x] |
| 2.3 | Emit `// <Field> is never granted <perm>: …` above such fields in `Check<Res><Perm>Inputs` | `internal/templates/object.go.tmpl` | [x] |
| 2.4 | Regenerate; confirm `git diff example/authzed | grep -E '^[-+](func|type) '` is empty and only comment lines are added | `example/authzed/**/*.gen.go` | [x] |

### 3. Tests

| # | Task | File | Status |
|---|------|------|--------|
| 3.1 | Tree test `(a + b) - c` → `Exclusion[Union[a, b], c]`; `ArrowFunc` assertions in the two functioned-arrow tests | `internal/generator/adapter_test.go` | [x] |
| 3.2 | `CaveatType` for every caveat param in `example/schema.zed` (incl. `client_ip == "ipaddress"`, `rows == "list<list<string>>"`) | same | [x] |
| 3.3 | `grantableInputTypes` ⊆ `permissionInputTypes` for every permission in the fixture; `article/author_only` and `article/editor` == `[extsvc/user]`; `folder/ancestor_or_self` includes `extsvc/folder`; `all_via` == `any_via` | `internal/generator/generator_test.go` (new) | [x] |
| 3.4 | E2E: `TestArticle_AuthorOnly_NeverGrantedFields_Denied`, `TestArticle_Editor_NeverGrantedFields_Denied`: grant `browse` on the parent folder to a group and a role; checks through `Group` / `Role` return `authz.ErrPermissionDenied` | `example/authzed/extsvc/extsvc_test.go` | [x] |

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

### [Implementer] Flatten equals the old lowering, byte for byte

After WS1 alone, regenerating `example/authzed` produced **zero diff**. `flattenPermissionNode(Rewrite)` reproduces the pre-AUZ-028 `Expressions` exactly for every permission in the fixture, so the resolver, walkers, templates and OPA emitter are provably unaffected by the tree refactor. `lowerUsersetRewrite` / `lowerSetOperationChild` keep their signatures as flatten views, so the AUZ-025 tests run unmodified.

### [Implementer] Generated diff: 4 comments, plus gofmt realignment

The only generated change is `extsvc/article.gen.go`: one comment on each of `Group` and `Role` in `CheckArticleEditorInputs` and `CheckArticleAuthorOnlyInputs`. This matches scope SC6, and `folder.gen.go` gets none. The diff also shows `User  []User` → `User []User`: inserting a comment line splits gofmt's field-alignment block. That change is whitespace only, with no `func` / `type` line changes.

### [Implementer] E2E proves the operator, not missing data, denies the check

Each never-granted test first asserts `folder.CheckBrowse` is **true** for the group and role, then asserts `CheckAuthorOnly` / `CheckEditor` through those fields returns `authz.ErrPermissionDenied`. The denial is therefore attributable to `-` / `&`, not to an absent relationship.

### [Implementer] Test helper for the fixture schema

`internal/generator/fixture_test.go` (`adaptFixtureSchema`) compiles `example/schema.zed` the same way `cmd/authzed-codegen` does. The `CaveatType` and grantability tests use it, so they assert against the real fixture rather than hand-built protos.

### [Reviewer] Verification summary

`go build`, `go vet`, `gofmt -l .` (empty), `go mod tidy` (no diff), round-trip (zero diff), SC8 grep (empty), `golangci-lint` v2.15.0 (0 issues), `govulncheck` v1.9.0 linux/amd64 (none), and `go test ./... -count=1` (all pass, including the 2 new e2e tests against SpiceDB).

### [Owner action] Not merged

Per the Notes, PR left open for the owner's review.
