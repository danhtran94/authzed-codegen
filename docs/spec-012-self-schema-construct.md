# [SPEC-012] `_self` Schema Construct

| Field      | Value                                                |
|------------|------------------------------------------------------|
| Status     | Accepted                                             |
| Created    | 2026-05-09                                           |
| Author     | Danh Tran                                            |
| Implements | (lifts ADR-001 rejection of `_self`)                 |

---

## Overview

This SPEC accepts SpiceDB's `_self` permission expression — schemas declared with `use self` at the top can use the `self` keyword inside permission expressions to mean "the resource as its own subject." Today the adapter rejects `XSelf` at adapt time (`internal/generator/adapter.go:602`); SPEC-012 lifts the rejection by introducing a new `PermExprSelf` kind in the codegen's permission expression tree, propagating self-reachability through the existing tree resolver, and emitting a `<ResourceType> []<ResourceType>` input field on `Check<Perm>Inputs` for self-reaching permissions. The semantic is server-side: SpiceDB's `checkSelf` evaluator (per `internal/graph/check.go:598-621`) grants only when the subject is **identity-equal** to the resource (same type, same ID, no sub-relation).

**What this component does:** Add `PermExprSelf` constant in `internal/generator/adapter.go` parallel to the existing `PermExprIdentifier` / `PermExprArrow` kinds. Replace the current rejection in `lowerSetOperationChild` (`case c.GetXSelf()`) with a return of `PermissionExpr{Kind: PermExprSelf}`. Extend `resolvePermissionExpressionTypes` to handle the new kind by appending a `Permission{Types: []string{args.ObjectType}, Kind: "single", Value: "self"}` — flowing the OWN type into the permission's input types via the existing tree resolver. Generated `Check<Perm>Inputs` for self-reaching permissions gain a `<ResourceType> []<ResourceType>` field; routing through `Engine.CheckPermission` is identical to any other typed input. Schema fixture adds `use self` directive at the top + a recursive permission pattern (`permission ancestor_or_self = self + parent->ancestor_or_self`) on `extsvc/folder`.

**What this component does not do:** Add new Engine methods — `CheckPermission` already accepts any subject type, and `_self` is server-side identity match. Modify Lookup paths — they work transparently because the resource type is now a possible input type for self-reaching permissions. Auto-detect "this is a self-equivalent input" — caller passes the same Folder ID as both the receiver and the input field; the codegen doesn't infer this. Handle `_self` in non-permission expressions — `_self` is only valid in `UsersetRewrite` children per the proto. Detect "useless self" patterns (e.g. `permission p = self` alone, which always grants only for the trivial case) — schema author's choice. Touch the existing `_this` / `_nil` rejections — they remain rejected per SPEC-012 scope.

---

## Interface Contracts

### Adapter — `internal/generator/adapter.go`

New constant in the `PermExprKind` group:

```go
const (
    PermExprIdentifier = "identifier"
    PermExprArrow      = "arrow"
    PermExprSelf       = "self"  // NEW
)
```

`lowerSetOperationChild` replaces the rejection branch:

```go
// BEFORE (current):
case c.GetXSelf() != nil:
    return PermissionExpr{}, fmt.Errorf("permission %q: self child is not supported", permName)

// AFTER (SPEC-012):
case c.GetXSelf() != nil:
    return PermissionExpr{Kind: PermExprSelf}, nil
```

The `LeftRel` / `RightPerm` / `Ident` fields remain empty for `PermExprSelf` — the kind alone identifies the construct; no additional payload required.

### Tree resolver — `internal/generator/generator.go`

`resolvePermissionExpressionTypes` extended to handle the new kind. The OWN type of the definition (`args.ObjectType`) propagates as the resolved input type for the self leg:

```go
case PermExprSelf:
    permissions = append(permissions, Permission{
        Types: []string{args.ObjectType},
        Kind:  "single",
        Value: "self",
    })
```

`Kind: "single"` (not `"permission"`) means `resolveTransitive` adds `args.ObjectType` directly to the permission's input types without recursing. Per A1 — `args.ObjectType` is the namespace string of the definition currently being resolved (e.g., `"extsvc/folder"`).

The existing `seen` dedup in `resolveTransitive` ensures duplicate types (e.g., self + a relation typed as the OwnType) collapse to one entry. Per C2.

### Generated `Check<Perm>Inputs` — new resource-type input field

For permissions reaching `_self`, the codegen emits a `<ResourceType> []<ResourceType>` field on the Check input struct:

```go
// definition extsvc/folder { permission ancestor_or_self = self + parent_for_self->ancestor_or_self }
//
// Generated:
type CheckFolderAncestorOrSelfInputs struct {
    Folder []Folder    // ← from the self leg AND the parent_for_self relation (same OwnType)
}

func (folder Folder) CheckAncestorOrSelf(
    ctx context.Context, input CheckFolderAncestorOrSelfInputs,
) (bool, error) {
    if len(input.Folder) == 0 && true {
        return false, authz.ErrNoInput
    }
    if len(input.Folder) > 0 {
        err := authz.GetEngine(ctx).CheckPermission(ctx, authz.Resource{
            Type: TypeFolder,
            ID:   authz.ID(folder),
        }, authz.Permission(FolderAncestorOrSelf), TypeFolder, authz.IDs(input.Folder))
        if err != nil {
            return false, err
        }
    }
    return true, nil
}
```

The generated method uses the existing `CheckPermission` path — no new engine method. Subject type is the OwnType (`TypeFolder`); subject IDs come from `input.Folder`.

### Generated `Lookup<Perm>FolderResources` / `Lookup<Perm>FolderSubjects`

Unchanged template handling. The OwnType appears in `permissionInputTypes` for self-reaching permissions; the existing per-input-type loop generates `Lookup<Perm>FolderResources` (resources of OwnType reachable to the subject) and `Lookup<Perm>FolderSubjects` (subjects of OwnType who reach the resource). No new Lookup-method shape required.

### Engine — no change

`Engine.CheckPermission` already accepts arbitrary subject types. SpiceDB's evaluator handles `_self` server-side (per `internal/graph/check.go:598-621`). The codegen is a thin pass-through.

### Caller pattern

```go
// Schema:
//   relation parent_for_self: extsvc/folder
//   permission ancestor_or_self = self + parent_for_self->ancestor_or_self

folderB := extsvc.Folder("b")
folderA := extsvc.Folder("a")

// Identity match — folderA is itself
ok, _ := folderA.CheckAncestorOrSelf(ctx, extsvc.CheckFolderAncestorOrSelfInputs{
    Folder: []extsvc.Folder{folderA},
})
// → granted via the self leg

// Recursive walk — folderA is an ancestor of folderB
require.NoError(t, folderB.CreateParentForSelfRelations(ctx, extsvc.FolderParentForSelfObjects{
    Folder: []extsvc.Folder{folderA},
}))
ok, _ = folderB.CheckAncestorOrSelf(ctx, extsvc.CheckFolderAncestorOrSelfInputs{
    Folder: []extsvc.Folder{folderA},
})
// → granted via the parent_for_self->ancestor_or_self leg (recursive)
```

---

## Sequence

Wire flow at codegen time:

```
schema.zed:
    use self
    definition extsvc/folder {
        relation parent_for_self: extsvc/folder
        permission ancestor_or_self = self + parent_for_self->ancestor_or_self
    }

         │
         ▼
SpiceDB compiler emits:
    SetOperation_Child{ChildType: *SetOperation_Child_XSelf{}}
    SetOperation_Child{ChildType: *SetOperation_Child_TupleToUserset{...}}

         │
         ▼
codegen lowerSetOperationChild:
    case c.GetXSelf() != nil:
      → PermissionExpr{Kind: PermExprSelf}
    case c.GetTupleToUserset() != nil:
      → PermissionExpr{Kind: PermExprArrow, LeftRel: "parent_for_self", RightPerm: "ancestor_or_self"}

         │
         ▼
resolvePermissionExpressionTypes (extsvc/folder.ancestor_or_self):
    [self] → Permission{Types: ["extsvc/folder"], Kind: "single", Value: "self"}
    [arrow] → walks parent_for_self (extsvc/folder) → recurses into ancestor_or_self
              → eventually finds another self → adds extsvc/folder again
              → dedup keeps single entry

         │
         ▼
permissionInputTypes("extsvc/folder", "ancestor_or_self") = ["extsvc/folder"]

         │
         ▼
template emits CheckFolderAncestorOrSelfInputs:
    type CheckFolderAncestorOrSelfInputs struct {
        Folder []Folder
    }
```

Wire flow at Check time (recursive walk):

```
caller:
    folderB.CheckAncestorOrSelf(ctx, input{Folder: []Folder{folderA}})
         │
         ▼
generated method body:
    └─► engine.CheckPermission(ctx, ResourceFolderB, "ancestor_or_self", TypeFolder, [folderA])

         │
         ▼
SpiceDB evaluator:
    folder:b#ancestor_or_self
        ├─► child 1: PermExprSelf
        │     ├─► subject == resource? subject=folder:a, resource=folder:b
        │     │     namespace match (folder == folder) ✓
        │     │     ID match (a != b) ✗
        │     │     → noMembers
        │     └─► self leg denies
        │
        └─► child 2: parent_for_self->ancestor_or_self
              ├─► reads folder:b.parent_for_self → finds folder:a
              ├─► recurses: folder:a#ancestor_or_self with subject=folder:a
              │     ├─► child 1: self → subject=a == resource=a → grant ✓
              │     └─► returns granted
              └─► returns granted
        │
        └─► union: granted via leg 2
```

---

## Errors

| Error class | Trigger | Layer |
|---|---|---|
| (REMOVED) `"permission %q: self child is not supported"` | Schema uses `_self`. Currently rejected at adapt time; SPEC-012 lifts. | Adapter |
| Pre-codegen schema rejection: `self` keyword without `use self` directive | Schema uses `self` without enabling the flag. `compiler.Compile()` rejects before codegen. | Pre-codegen (SpiceDB compiler) |
| Naming collision: definition has a relation typed as its own type AND uses `_self` | E.g. `definition folder { relation folder_self: folder; permission p = self + folder_self }` would emit both as input type `extsvc/folder` → dedups to one `Folder []Folder` field. **No error.** | (No collision — handled by existing dedup per SPEC-012 C2.) |

No new error classes.

---

## Constraints

- **C1.** `PermExprSelf` carries no payload beyond the kind. `LeftRel`, `RightPerm`, `Ident` remain empty strings. Per the design — `_self` has no parameters; it's a constant evaluator semantic.

- **C2.** Duplicate OwnType from self + a same-type relation collapses via the existing `seen` dedup in `resolveTransitive`. A permission like `p = self + parent_for_self->p` (where parent_for_self is typed as OwnType) results in one `<TypeName>` entry, not two.

- **C3.** `<ResourceType> []<ResourceType>` field naming follows the existing `permissionInputTypes` → field-name pattern via `typeName` (last path segment, PascalCased). No special handling for self-derived inputs.

- **C4.** Field-name collision detection. If a definition has a relation whose `IDFieldName` collides with the OwnType's `<TypeName>` (e.g., `definition folder { relation folder: ... }`), the existing `Check<Perm>Inputs` field generation already uses `typeName(namespace)` for both the relation input AND the self input, producing one field. Per A2 — this is structurally fine because both inputs route through `CheckPermission` with the OwnType; the field semantically covers both roles.

- **C5.** `_self` is recognised only inside permission expressions (UsersetRewrite children). The proto enforces this at the protocol level — `XSelf` cannot appear in `AllowedRelation`. Codegen does not need a defensive check.

- **C6.** No new Engine method. `CheckPermission(ctx, dest, has, subject, audIDs)` covers self-reaching permissions transparently — subject type is the resource's OwnType.

- **C7.** Lookup paths (`LookupResources` / `LookupSubjects`) work transparently. For self-reaching permissions, the OwnType appears in `permissionInputTypes`; existing per-input-type emission generates `Lookup<Perm><OwnType>Resources` / `Lookup<Perm><OwnType>Subjects` methods using the standard pattern.

- **C8.** Round-trip idempotency stable. Schemas without `_self` regenerate byte-identical to v1.11.0. The new fixture adds `use self` + recursive permission; existing fixtures untouched.

- **C9.** `use self` directive is parsed by `compiler.Compile()` before the codegen sees the schema. The codegen does not enforce the directive — invalid usage (`self` without `use self`) errors at compile time.

- **C10.** `_self` is not a userset reference. Per the SpiceDB evaluator, `_self` requires `Subject.Relation == ellipsis` (no sub-relation). If the caller passes a userset subject (e.g. `team:t1#admin`) to a self-reaching permission Check, SpiceDB denies. The codegen does not gate this client-side; runtime denies correctly.

---

## Assumptions

- **A1 [VERIFIED]:** `args.ObjectType` in `resolvePermissionExpressionTypes` is the namespace string of the definition being resolved. Evidence: `internal/generator/generator.go:347-352` — `defs.OutputPath` is set to the definition's `objectType := d.ObjectType.String()` before calling the resolver.

- **A2 [VERIFIED]:** Field-name collision between a self-derived input and a same-type relation input results in one merged field. The `permissionInputTypes` helper returns deduped namespaces; the template iterates and emits one field per namespace. Whether the namespace came from a relation, a permission, or `_self` is invisible at this layer.

- **A3 [EXTERNAL FACT]:** SpiceDB's `checkSelf` evaluator (`internal/graph/check.go:598-621`) implements identity match: same namespace, same object ID, ellipsis subject relation. Self denies for sub-relation subjects, cross-type subjects, and non-equal IDs.

- **A4 [EXTERNAL FACT]:** The `use self` directive is gated by SpiceDB's lexer flag system per `pkg/schemadsl/lexer/flags.go:63-71`. Schemas without the directive can't use the keyword; `compiler.Compile()` rejects.

- **A5 [HYPOTHESIS]:** No production schema in this codebase uses `_self`. Verification: no existing relation/permission in `example/schema.zed` references `self`. Adding the fixture is additive; no migration required.

- **A6 [VERIFIED]:** `resolveTransitive`'s `seen` dedup collapses duplicate type entries. Evidence: `internal/generator/generator.go:294-300` — `seen` map keyed by namespace string.

- **A7 [HYPOTHESIS]:** Recursive permission patterns (`p = self + parent->p`) terminate in SpiceDB's evaluator without infinite loops. Per SpiceDB's documented behavior — recursive rewrites are evaluated breadth-first with cycle detection. Verification: e2e tests exercise multi-level parent chains (depth 3+) and confirm grants/denies are computed correctly.

---

## Unresolved Questions

(none)

---

## Summary

Net change scope:

| File | Change |
|---|---|
| `internal/generator/adapter.go` | Add `PermExprSelf` constant. Replace the rejection branch for `c.GetXSelf()` in `lowerSetOperationChild` with a return of `PermissionExpr{Kind: PermExprSelf}`. |
| `internal/generator/generator.go` | Extend `resolvePermissionExpressionTypes` switch with a `case PermExprSelf:` branch appending `Permission{Types: [args.ObjectType], Kind: "single", Value: "self"}`. |
| `internal/templates/object.go.tmpl` | NO CHANGES. Existing `permissionInputTypes` iteration handles the OwnType field emission transparently. |
| `example/schema.zed` | Add `use self` directive at the top. Add a recursive permission fixture on `extsvc/folder`: `relation parent_for_self: extsvc/folder` + `permission ancestor_or_self = self + parent_for_self->ancestor_or_self`. |
| `example/authzed/extsvc/folder.gen.go` | Regenerated — gains methods for `parent_for_self` relation + `ancestor_or_self` permission. Other generated methods unchanged. |
| `example/authzed/extsvc/extsvc_test.go` | Add e2e tests covering: identity match (folder.CheckAncestorOrSelf with self), identity mismatch (different folder denies), recursive ancestor walk (3-level chain grants), cross-type subject (passing wrong type denies via SpiceDB; codegen prevents at compile time), sub-relation subject (userset subjects denied per SpiceDB evaluator). |

E2E tests verify SpiceDB's identity-match semantic against live SpiceDB:
1. `folder.CheckAncestorOrSelf(ctx, {Folder: []Folder{folder}})` → granted (self leg)
2. `folderB.CheckAncestorOrSelf(ctx, {Folder: []Folder{folderA}})` where A != B and no parent chain → denied
3. Build chain `folderC.parent = folderB`, `folderB.parent = folderA`; `folderC.CheckAncestorOrSelf(ctx, {Folder: []Folder{folderA}})` → granted (3-level recursive walk)
4. Vacuous case: folderX with no parents and no self-input match → denied
5. Verify Lookup pattern: `LookupAncestorOrSelfFolderResources(ctx, {Folder: []Folder{folderA}})` returns folderA + every descendant in the chain.

---

## History

(History is owned by `harness history-update` — do not hand-edit.)
