<!-- approved -->
# AUZ-027: Userset inputs reach the engine in generated `Lookup<Perm><Resource>Resources`

| Field      | Value                                          |
|------------|------------------------------------------------|
| Status     | Done                                           |
| Created    | 2026-11-18                                     |
| Assignee   | Claude                                         |
| Source     | docs/spec-006-sub-relation-references.md       |
| Blocked by | —                                              |

## Goal

Generated `Lookup<Perm><Resource>Resources(ctx, input Check<Res><Perm>Inputs)` sends a populated userset field to SpiceDB. Examples are `TeamAdmin []Team` for `extsvc/team#admin`, and the `TeamAdmin` field of `nested_access`. Today such a field is accepted and silently ignored.

The request goes through a new `authz.Engine.LookupResourcesUserset` method that sets `SubjectReference.OptionalRelation`. This mirrors how `Check<Perm>` already routes the same fields through `CheckPermissionUserset` (SPEC-006).

`LookupCollabViewFolderResources(ctx, {TeamAdmin: [t]})` returns the folders whose `collab` holds `t#admin`, instead of an empty result with no engine call. The release is `v2.2.0` (Minor: README § Versioning lists new `Engine` methods as Minor).

## Problem

SPEC-006 added userset fields to `Check<Perm>Inputs` and a userset route for `Check<Perm>` (`internal/templates/object.go.tmpl:270`, `permissionInputUsersets`). It did not add a route for the lookup that takes the same struct. In `object.go.tmpl:283-320`, `Lookup<Perm><Resource>Resources` ranges only over `permissionInputTypes` and then returns an empty result. `permissionInputUsersets` is never consulted.

Consequences on `v2.1.0`:

- `LookupCollabViewFolderResources` (`example/authzed/extsvc/folder.gen.go`) has a body of just `return FolderLookupResult{}, nil`, because `collab_view` has no direct input types.
- For `mixed_browse` / `nested_access`, a `TeamAdmin`-only input returns empty without calling the engine.
- `Check<Perm>` with the same input does reach SpiceDB, so Check and Lookup disagree for the same struct value.

The failure is silent: callers get a successful empty result, not an error. It is fail-closed but wrong. It was found by review on #13.

`authz.Engine` has no way to express this request. `LookupResources` / `LookupResourcesWithCaveat` take `(subject Type, byIDs []ID)` and build `SubjectReference{Object}` without `OptionalRelation` (`pkg/authz/spicedb/crud.go`, `LookupResourcesWithCaveat`).

## Solution: sibling engine method, same pattern as `CheckPermissionUserset`

    Before (template):
      for each direct input type T: if len(input.T) > 0 → LookupResources[WithCaveat](T, ids) → return
      return empty                                   ← userset fields never read

    After (template):
      for each direct input type T:  (unchanged, still first)
      for each userset U = (Type, SubRelation): if len(input.<U>) > 0 → LookupResourcesUserset(Type, ids, SubRelation, caveatCtx) → return
      return empty

### Components

**`authz.Engine.LookupResourcesUserset`** (new interface method, `pkg/authz/authz.go`)
- `LookupResourcesUserset(ctx, from Type, match Permission, subject Type, byIDs []ID, subRelation string, caveatParams map[string]any) (LookupResult, error)`
- `caveatParams == nil` means no request-time context, the same convention as `CheckPermissionUserset`.

**`*spicedb.Engine.LookupResourcesUserset`** (`pkg/authz/spicedb/crud.go`)
- Same loop and `Definite` / `Conditional` mapping as `LookupResourcesWithCaveat`. The only difference is `Subject.OptionalRelation: subRelation`.
- The shared stream-to-`LookupResult` body is extracted into one unexported helper used by both methods, so the mapping is not duplicated.

**`Lookup<Perm><Resource>Resources` template** (`internal/templates/object.go.tmpl`)
- After the direct-type loop, add a `range permissionInputUsersets` branch. It passes `caveatCtx` when `hasPermCaveats` is true, and `nil` otherwise.

### Why not alternatives

| Approach | Verdict |
|---|---|
| **New sibling `LookupResourcesUserset` (chosen)** | Additive (Minor); mirrors `CheckPermissionUserset` exactly, so readers find the same shape on both sides |
| Widen `LookupResources` with a `subRelation` parameter | Breaks every caller and `Engine` implementer for one optional string; README would require a major bump |
| Encode the userset in `subject Type` (e.g. `"extsvc/team#admin"`) | Stringly-typed; every engine implementation must parse it back out; breaks the `Type` = object type invariant |
| Return `authz.ErrNoInput` when only userset fields are set | Turns a silent wrong answer into an explicit error, but still cannot answer the question that `Check` already answers |

## Workstreams

### 1. Engine

| # | Task | File | Status |
|---|------|------|--------|
| 1.1 | Add `LookupResourcesUserset` to `authz.Engine`, with a doc comment stating literal-userset semantics and the nil-`caveatParams` convention | `pkg/authz/authz.go` | [x] |
| 1.2 | Extract the `LookupResources` stream-to-`LookupResult` mapping into an unexported helper; make `LookupResourcesWithCaveat` use it | `pkg/authz/spicedb/crud.go` | [x] |
| 1.3 | Implement `LookupResourcesUserset` on `*spicedb.Engine` with the helper and `Subject.OptionalRelation`; nil `caveatParams` sends no `Context` | same | [x] |

**Key details:** 1.1 and 1.3 must land together, because the `var _ authz.Engine = &Engine{}` assertion stops compiling in between.

### 2. Template + regen

| # | Task | File | Status |
|---|------|------|--------|
| 2.1 | Add the `permissionInputUsersets` branch to `Lookup<Perm><Resource>Resources` after the direct-type loop | `internal/templates/object.go.tmpl` | [x] |
| 2.2 | Regenerate: `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed` | `example/authzed/**/*.gen.go` | [x] |
| 2.3 | Confirm the diff only adds userset branches inside `Lookup*Resources` bodies; no `func` / `type` line changes | same | [x] |

### 3. E2E tests (SpiceDB testcontainer)

| # | Task | File | Status |
|---|------|------|--------|
| 3.1 | `TestEngine_LookupResourcesUserset_LiteralMatch`: write `folder#collab@team:t#admin`; lookup with `(extsvc/team, [t], "admin")` returns the folder; with `(…, [other], "admin")` returns empty | `pkg/authz/spicedb/spicedb_test.go` | [x] |
| 3.2 | `TestFolder_LookupCollabView_UsersetInput`: `LookupCollabViewFolderResources(ctx, {TeamAdmin: [t]})` returns the granted folder (was empty) | `example/authzed/extsvc/extsvc_test.go` | [x] |
| 3.3 | `TestFolder_LookupGatedCollabView_UsersetWithCaveat`: caveated userset; matching tenant → `Definite`; nil caveats → `Conditional` with `MissingKeys == ["tenant"]` | same | [x] |
| 3.4 | `TestFolder_LookupMixedBrowse_DirectTypeWinsOverUserset`: both `User` and `TeamAdmin` set → result equals the `User`-only lookup (precedence unchanged) | same | [x] |

### 4. Verification

| # | Task | Status |
|---|------|--------|
| 4.1 | `go build ./...`, `go vet ./...`, `gofmt -l .`, `go mod tidy` clean | [x] |
| 4.2 | Round-trip regeneration zero-diff | [x] |
| 4.3 | CI-pinned `golangci-lint` v2.15.0 and `govulncheck` v1.9.0 (linux/amd64) clean | [x] |
| 4.4 | Full `go test ./... -count=1` passes, including e2e | [x] |

### 5. Documentation

| # | Task | File | Status |
|---|------|------|--------|
| 5.1 | `## [2.2.0] - Unreleased`: `### Added` (`Engine.LookupResourcesUserset`) and `### Fixed` (userset inputs ignored by `Lookup*Resources`). Note that custom `Engine` implementations must add the method. | `CHANGELOG.md` | [x] |
| 5.2 | README sub-relation section: lookup with a userset input now works and matches the literal userset reference | `README.md` | [x] |

## Design Decisions

### Direct input types keep precedence over usersets

`Lookup*Resources` has always returned on the first non-empty input field. Userset branches are appended after the direct-type loop, so any caller that populates a direct type gets exactly the same result as on `v2.1.0`. Only inputs where every direct type is empty change behavior, from an empty result to a real answer.

### Literal-userset semantics

SPEC-006 A2 defines userset-as-subject `Check` as matching the literal userset reference, without expanding membership. `LookupResourcesUserset` documents and tests the same semantics (3.1), so `Check` and `Lookup` agree for the same input.

## What Stays Unchanged

- Every existing `Engine` method signature, including `LookupResources` and `LookupResourcesWithCaveat`.
- Generated `func` / `type` declarations. Only `Lookup*Resources` bodies change.
- OPA lookup builtins. Their subject keys are object types only, and userset subjects there are a separate decision.
- `Lookup<Perm><Type>Subjects`.

## Implementation Order

    1. Workstream 1 (engine)          ← interface + impl must compile together; template calls the new method
    2. Workstream 2 (template, regen) ← generated code calls Engine.LookupResourcesUserset from WS1
    3. Workstream 3 (e2e)             ← exercises WS1 directly and WS2's generated bodies
    4. Workstream 4 (verification)    ← gates close over WS1–3
    5. Workstream 5 (docs)            ← describes the verified behavior

## Notes

- Run 3.2 against `v2.1.0` codegen first: it must fail with an empty result. That proves the test catches the bug.
- Adding a method to `authz.Engine` breaks third-party `Engine` implementations at compile time. README § Versioning classes new `Engine` methods as Minor (same as `v1.15.0`'s `DeleteRelationsMatching`), and CHANGELOG 5.1 must say so explicitly.

## Discoveries & Decisions During Implementation

### [Implementer] Regression proven before the fix

The WS3 e2e tests were written first and run against the `v2.1.0` template. `TestFolder_LookupCollabView_UsersetInput` and `TestFolder_LookupGatedCollabView_UsersetWithCaveat` failed with `actual: []extsvc.Folder(nil)`. `TestFolder_LookupMixedBrowse_DirectTypeWinsOverUserset` passed both before and after the fix, because it pins the unchanged precedence. `TestEngine_LookupResourcesUserset_LiteralMatch` could not exist before WS1.

### [Implementer] Helper extraction instead of a copied body

`LookupResourcesWithCaveat`'s body became `lookupResources(..., subRelation string, ...)`. The only new line is `OptionalRelation: subRelation`, and an empty string is the proto's unset value, so the plain-subject path sends the same wire request as before. Both public methods are one-line delegations.

### [Reviewer] Verification summary

`go build`, `go vet`, `gofmt -l .` (empty), `go mod tidy` (no diff), regeneration round-trip (zero diff), CI-pinned `golangci-lint` v2.15.0 (0 issues), `govulncheck` v1.9.0 with `GOOS=linux GOARCH=amd64` (none), and `go test ./... -count=1` all pass, including the 4 new tests against the SpiceDB testcontainer. The generated diff touches only `extsvc/folder.gen.go` and adds userset branches inside `Lookup*Resources` bodies; no `func` / `type` line changes.
