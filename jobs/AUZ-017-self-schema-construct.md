# AUZ-017: `_self` Schema Construct

| Field      | Value                                              |
|------------|----------------------------------------------------|
| Status     | Done                                                |
| Created    | 2026-05-09                                         |
| Assignee   | danhtran94                                         |
| Source     | docs/spec-012-self-schema-construct.md             |
| Blocked by | —                                                  |

<!-- approved -->

---

## Goal

Lift the codegen's adapt-time rejection of `XSelf` so schemas using `use self` + the `self` keyword in permission expressions compile. `_self` is SpiceDB's identity-match construct — `permission p = self` grants only when the subject is the same OBJECT as the resource (same type, same ID, no sub-relation). Most useful in recursive permission patterns (`permission ancestor_or_self = self + parent->ancestor_or_self`) for tree-shaped data: the `self` leg provides the base case for "X or any X-reachable object via the recursion." After this job, the codegen accepts every commonly-used SpiceDB schema construct.

## Problem

    Current (post-v1.11.0):
      caller declares schema with `use self` + `permission p = self`
        → adapter `lowerSetOperationChild` errors:
            "permission %q: self child is not supported"
              → codegen exits before any output is written ✗
      caller cannot write recursive permission patterns cleanly:
        permission ancestor_or_self = self + parent->ancestor_or_self
        ✗ no idiomatic way to express "X or any X's ancestor"

The rejection has been in place since AUZ-001 (parser migration). SpiceDB has supported `_self` for years; schemas using it (recursive tree walks, identity assertions) currently can't be consumed by this codegen.

## Solution: New `PermExprSelf` kind threading OwnType into permission inputs

    After fix:
      caller declares: permission ancestor_or_self = self + parent->ancestor_or_self
        → adapter accepts; lowerSetOperationChild handles GetXSelf() →
            PermissionExpr{Kind: PermExprSelf}
          → tree resolver case PermExprSelf appends Permission{
              Types: [args.ObjectType], Kind: "single", Value: "self"}
            → permissionInputTypes for self-reaching permissions includes OwnType
              → template emits `<ResourceType> []<ResourceType>` field on
                 Check<Perm>Inputs and routes through CheckPermission with
                 subject = OwnType ✓
      caller writes:
        folderB.CheckAncestorOrSelf(ctx, input{Folder: []Folder{folderA}})
        → granted iff folderA == folderB (self leg) OR folderA is ancestor

The OwnType propagates via the existing tree resolver — `seen` dedup collapses duplicate types from self + a same-type relation. No new Engine method; `CheckPermission` already accepts arbitrary subject types.

### Components

**`PermExprSelf`** — new `PermExprKind` constant in `adapter.go`. Empty payload (`LeftRel` / `RightPerm` / `Ident` all empty); kind alone identifies the construct.

**`lowerSetOperationChild` `case GetXSelf()`** — replaces the rejection with `return PermissionExpr{Kind: PermExprSelf}, nil`.

**`resolvePermissionExpressionTypes` `case PermExprSelf`** — appends `Permission{Types: []string{args.ObjectType}, Kind: "single", Value: "self"}`. The `"single"` kind flows through `resolveTransitive` without recursion; the OwnType lands directly in the permission's input types.

**Schema fixture additions** — `use self` directive + recursive permission on `extsvc/folder`:
- `relation parent_for_self: extsvc/folder`
- `permission ancestor_or_self = self + parent_for_self->ancestor_or_self`

### Why not alternatives

| Approach | Verdict |
|---|---|
| **New `PermExprSelf` kind + tree resolver case** (chosen) | Minimal scope; reuses existing `permissionInputTypes` → template-emission pipeline. ~15 lines across adapter + generator. |
| Treat `_self` as a synthetic identifier referencing OwnType | Rejected. Indistinguishable from a real relation/permission of the same name; would conflate with relation-name collisions. |
| Add new Engine method for self-checks | Rejected. SpiceDB's `CheckPermission` already accepts arbitrary subject types — `_self` is server-side semantic, not a wire-level distinction. |
| Surface "this input is the self-leg" via codegen comment | Out of scope. Field-naming collisions handled by existing dedup; explicit annotation adds noise without value. |

## Workstreams

### 1. Adapter — accept `XSelf`

| #   | Task | File | Status |
|-----|------|------|--------|
| 1.1 | Add `PermExprSelf = "self"` constant alongside `PermExprIdentifier` and `PermExprArrow` | `internal/generator/adapter.go` | [x] |
| 1.2 | Replace `case c.GetXSelf() != nil:` rejection with type-assertion-based handler returning `PermissionExpr{Kind: PermExprSelf}` (Get-accessor doesn't work — see Discoveries) | same | [x] |
| 1.3 | Unit test: schema with `permission p = self` adapts to `PermissionExpr{Kind: PermExprSelf}` (empty LeftRel / RightPerm / Ident) | `internal/generator/adapter_test.go` | [x] |

**Key details:** Per SPEC-012 C1 — `PermExprSelf` carries no payload. Per A4 — `compiler.Compile()` validates `use self` directive presence; codegen never sees `_self` in invalid contexts.

### 2. Tree resolver — propagate OwnType for self-reaching permissions

| #   | Task | File | Status |
|-----|------|------|--------|
| 2.1 | Extend `resolvePermissionExpressionTypes` switch with `case PermExprSelf:` appending `Permission{Types: []string{args.ObjectType}, Kind: "single", Value: "self"}` | `internal/generator/generator.go` | [x] |
| 2.2 | Relax cycle detection in `resolveTransitive` — recursive permissions (the canonical `_self` use case) are legitimate; return empty types on revisit instead of erroring | same | [x] |

**Key details:** Per SPEC-012 A1 / A2 — `args.ObjectType` is the definition's namespace string; `seen` dedup in `resolveTransitive` collapses duplicate types. `Kind: "single"` ensures the OwnType is added directly without recursion (vs `Kind: "permission"` which recurses).

### 3. Template — verify no template change needed

| #   | Task | File | Status |
|-----|------|------|--------|
| 3.1 | Run codegen on a self-using schema; verify generated `Check<Perm>Inputs` gains `<ResourceType> []<ResourceType>` field automatically via existing `permissionInputTypes` iteration | `internal/templates/object.go.tmpl` | [x] |
| 3.2 | Verify generated `Lookup<Perm>...` methods iterate the OwnType correctly (no special-casing needed) | same | [x] |

**Key details:** SPEC-012 promised zero template change. WS3 is verification only — if a template change emerges, escalate to a discovery in §Discoveries and adjust scope.

### 4. Schema fixture — `use self` + recursive permission

| #   | Task | File | Status |
|-----|------|------|--------|
| 4.1 | Add `use self` directive at the top of `example/schema.zed` (alongside the existing `use expiration`) | `example/schema.zed` | [x] |
| 4.2 | Add `relation parent_for_self: extsvc/folder` + `permission ancestor_or_self = self + parent_for_self->ancestor_or_self` to the `extsvc/folder` definition | same | [x] |
| 4.3 | Run codegen — `go run ./cmd/authzed-codegen --output example/authzed example/schema.zed` — commit regenerated `folder.gen.go` | `example/authzed/extsvc/folder.gen.go` | [x] |

**Key details:** Per SPEC-012 A5 — no existing fixture uses `_self`. Per A6 — adding the directive is additive; existing schemas regenerate byte-identical for definitions that don't use the keyword.

### 5. E2E tests — identity match + recursive walk + edge cases

| #   | Task | Status |
|-----|------|--------|
| 5.1 | E2E: identity match — `folder.CheckAncestorOrSelf(ctx, input{Folder: []Folder{folder}})` (same folder as both receiver and input) → granted via the self leg — `example/authzed/extsvc/extsvc_test.go` | [x] |
| 5.2 | E2E: identity mismatch — `folderB.CheckAncestorOrSelf(ctx, input{Folder: []Folder{folderA}})` with NO parent chain → denied | [x] |
| 5.3 | E2E: recursive ancestor walk — build chain `folderC.parent = folderB`, `folderB.parent = folderA`; `folderC.CheckAncestorOrSelf(ctx, input{Folder: []Folder{folderA}})` → granted via 3-level recursive walk | [x] |
| 5.4 | E2E: outside-chain deny — folderX not in the parent chain → recursive walk doesn't reach it → denied | [x] |
| 5.5 | E2E: LookupResources tree walk — build chain folderA → folderB → folderC; `LookupAncestorOrSelfFolderResources(ctx, input{Folder: []Folder{folderA}})` returns `[folderA, folderB, folderC]` (the input folder + descendants) | [x] |
| 5.6 | E2E: regression sweep — full e2e suite passes after WS1+WS4 — `go test ./pkg/authz/spicedb/... ./example/authzed/...` | [x] |

### 6. Documentation + release prep

| #   | Task | Status |
|-----|------|--------|
| 6.1 | Add `[1.12.0]` entry to `CHANGELOG.md` documenting `_self` acceptance, the recursive-permission use case, and the empty-oneof-wrapper discovery — `CHANGELOG.md` | [x] |
| 6.2 | Update `README.md` Schema Support table — flip `_this`, `_self`, `with self` row to mark `_self` as ✓ (leaving `_this`, `_nil`, `with self` as still-rejected) — `README.md` | [x] |
| 6.3 | Tag `v1.12.0` after merge; create GitHub release with notes calling out the recursive-permission pattern | [x] |

## Design Decisions

### New `PermExprKind` constant
`PermExprSelf` parallels the existing `PermExprIdentifier` / `PermExprArrow` kinds. The dedicated kind makes downstream consumers (resolver, walkers) trivially identifiable. Treating `_self` as a synthetic identifier was rejected — would conflate with real relations/permissions of the same name.

### `Kind: "single"` for the synthetic Permission entry
The tree resolver's `Kind` discriminator: `"permission"` recurses, all other kinds add types directly. `_self` doesn't recurse (it's an identity match, not a reference to another permission). Using `"single"` matches the existing pattern from `relationFromView`.

### No new Engine method
SpiceDB's `CheckPermission(ctx, dest, has, subjectType, ids)` accepts any subject type. `_self` is server-side identity match — when subjectType matches resourceType and ID matches, SpiceDB's `checkSelf` grants. No wire-level distinction; codegen routes through the existing path.

### No template change
The template iterates `permissionInputTypes` to emit `Check<Perm>Inputs` fields. Since `_self` adds OwnType to the input types via the resolver, the template emits the right field automatically. Verification in WS3.

### Field-name dedup with same-type relations
A permission like `p = self + parent_for_self->p` (parent_for_self typed as OwnType) produces TWO sources of OwnType in the input types. The existing `seen` dedup collapses them. The single resulting `<TypeName>` field semantically covers both roles.

## What Stays Unchanged

- All existing `Engine.*` method signatures
- `pkg/authz/spicedb/crud.go` — no engine impl changes
- `internal/templates/object.go.tmpl` — no template change
- `internal/generator/adapter.go`'s rejections of `_this` / `_nil` / functioned tuple-to-userset — only `_self` is lifted by AUZ-017
- Per-namespace generated `.gen.go` files for definitions without `_self` — byte-identical to v1.11.0
- Codegen idempotency invariant — `git diff --quiet example/authzed/` zero-diff for non-self schemas
- README sections on Caveats / Expiration / Sub-relation References / Conditional Permission / Consistency / Schema Drift / Versioning
- The four other rejected constructs (`_this`, `_nil`, etc.) — only `_self` is lifted

## Implementation Order

    1. WS1 Adapter         ← single switch case + unit test
    2. WS2 Tree resolver   ← depends on WS1 (PermExprSelf must exist)
    3. WS3 Template verify ← regen with self-using fixture; verify no template change needed
    4. WS4 Schema fixture  ← adds `use self` + recursive permission
    5. WS5 E2E tests        ← depends on WS4 (fixture in place)
    6. WS6 Docs + release   ← last; depends on test pass

WS1 + WS2 land as one commit (atomic — kind constant and resolver case must land together). WS3 + WS4 land together (template-change verification + fixture regen). WS5 follows. WS6 closes.

## Notes

- Round-trip the example fixture before declaring any generator change done. Per `.claude/CLAUDE.md`.
- Full e2e suite must pass: `go test ./pkg/authz/spicedb/... ./example/authzed/...`.
- Version bump is `1.12.0` (minor) — pure additive; existing schemas unchanged.
- Per v1.10 versioning policy — additive features go through minor bumps.
- `harness validate-pr-checklist` will hard-block a push with `Status=Done` while any task row is `[ ]`.
- Recursive permission tests must use multi-level parent chains (depth 3+) to verify SpiceDB's recursive evaluator terminates correctly per A7.

## Discoveries & Decisions During Implementation

### [Implementer] SpiceDB's empty-marker oneof variants emit with NIL inner fields

The `c.GetXSelf() != nil` pattern (mirroring how the existing TupleToUserset / ComputedUserset cases were detected) FAILED to match `_self` children. Diagnostic via debug print revealed: SpiceDB's `namespace.Self()` factory at `~/go/pkg/mod/github.com/authzed/spicedb@v1.52.0/pkg/namespace/builder.go:254` emits:

```go
return &core.SetOperation_Child{
    ChildType: &core.SetOperation_Child_XSelf{},  // wrapper, NO inner Self
}
```

The wrapper struct's `XSelf *SetOperation_Child_Self` field is left NIL. Go proto's generated `GetXSelf()` accessor returns that nil inner field, so `!= nil` evaluates false even when the oneof IS active. Same pattern for `Nil()` and `This()` factories.

**Fix**: refactored the entire `lowerSetOperationChild` switch to use type assertion on `c.GetChildType().(type)`. Type assertion checks the wrapper itself (always non-nil for the active oneof case), not the inner field. Switched all 7 cases to the new pattern. Idempotent for content-bearing cases (ComputedUserset, TupleToUserset, etc.) which had Get-accessor checks that worked correctly. Critical for the empty-marker cases.

This was a real adapt-time bug masked by the fact that no fixture had used `_self` / `_this` / `_nil` before (all were rejected). The XSelf rejection wasn't actually rejecting `_self` schemas — they were silently hitting the `default` branch and erroring with "unknown rewrite child type." Fixing this also tightens the rejection of `_this` / `_nil` (now correctly identified rather than falling through default).

### [Implementer] Cycle detection rejected legitimate recursion

Initial regen after the adapter fix surfaced `cycle detected at extsvc/folder/ancestor_or_self`. The `resolveTransitive` function had unconditional cycle detection — any permission whose expression recursively references itself triggered the error.

But `permission ancestor_or_self = self + parent_for_self->ancestor_or_self` is the canonical recursive `_self` pattern. Cycle is by design.

**Fix**: changed the cycle check from "error on revisit" to "return empty types on revisit." The recursive call adds no NEW input types beyond what the outer call already collects (the `_self` leg contributes OwnType directly; the recursive arrow leg contributes nothing new). Outer dedup merges correctly. SpiceDB's evaluator handles recursion at Check time with its own cycle protection.

This was a quiet pre-existing bug — the original cycle check would have rejected ANY recursive permission, not just `_self`-using ones. It happened to never fire because no existing fixture had recursion. AUZ-017 surfaced it.

### [Implementer] Zero template change held up

SPEC-012 promised the template wouldn't need changes — `permissionInputTypes` iteration handles new input types automatically. Verified: regen with the new fixture produces `CheckFolderAncestorOrSelfInputs.Folder []Folder` field correctly. The `<TypeName>` field name comes from the existing `typeName` helper applied to the OwnType namespace; the routing through `CheckPermission` with `subjectType = TypeFolder` matches the standard pattern.
