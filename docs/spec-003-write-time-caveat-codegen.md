# [SPEC-003] Write-time Caveat Codegen

| Field        | Value                                          |
|--------------|------------------------------------------------|
| Status       | Draft                                          |
| Created      | 2026-05-08                                     |
| Author       | Danh Tran                                      |
| Implements   | (follow-up to SPEC-002 / AUZ-006 Discoveries 2 + 10) |

---

## Overview

This SPEC defines the codegen and runtime changes needed to attach caveats to relationships at **write time**. SPEC-002 / AUZ-006 lifted the read-side rejection of caveats — the adapter accepts `with <caveat>` and the generated `Check<Perm>` routes through `CheckPermissionWithCaveat` — but the write path is incomplete. `Create<Rel>Relations` still emits a non-caveated `RelationshipUpdate`, which SpiceDB rejects for relations declared with a caveat (the AUZ-006 e2e tests had to bypass the codegen via `authzed.Client.WriteRelationships` directly to set up caveated tuples). This SPEC closes that gap so callers writing to a `with caveat` relation can stay inside the generated API.

**What this component does:** When a relation declares any `with <caveat>` allowed type, the generated `<Rel>Objects` struct gains a `Caveats <RelName>Caveats` sub-struct (mirroring the existing `Wildcards <RelName>Wildcards` pattern), with one `<TypeName> *<CaveatPascal>Args` field per caveated allowed type. Add `Engine.CreateRelationsWithCaveat(ctx, to, relation, subject, ids, caveatName, caveatParams) error` to the runtime interface; the implementation emits `RelationshipUpdate.Relationship.OptionalCaveat = &v1.ContextualizedCaveat{CaveatName, Context}` from the typed Args (per A1, A2). The generator's `Create<Rel>Relations` routes each allowed type independently — caveat-bearing types call the new method with the codegen-known caveat name and user-supplied params; non-caveated types continue through the existing `CreateRelations`. On the read side, `Check<Perm>Inputs` gains a parallel `Caveats Check<Perm>Caveats` sub-struct with one field per **unique caveat name** reachable from the permission (named `<CaveatPascal>`); the generated `Check<Perm>` body merges every non-nil entry into a single wire Context map. A `detectPermCaveatCollisions` codegen check errors when 2+ caveats reachable from one permission share a parameter name, preventing silent wire-key clobbering. Reuses AUZ-006's `<CaveatPascal>Args` structs and `serializeCaveatMap` helper. A nil sub-struct field translates to a name-only attach (per A6) — wire-supported, equivalent to deferring that caveat's parameter binding to check time.

**What this component does not do:** Modify `Delete<Rel>Relations` — SpiceDB's `OPERATION_DELETE` matches on 6-column tuple identity and ignores caveat metadata (per A3). Expose `OPERATION_TOUCH` / upsert — not in the current API surface. Surface caveat metadata on the read side — `Read<Rel><Type>Relations` continues to return only IDs (`ReadRelationshipsResponse.Relationship.OptionalCaveat` is dropped at the engine layer; `Read<Rel><Type>RelationsWithCaveat` returning `[]ReadResult[T]{ID, Caveat, CaveatName}` is a deferred enhancement). Pass request-time `Context` on `Lookup<Perm><Type>Resources` / `Lookup<Perm><Type>Subjects` — SpiceDB's `LookupResourcesRequest.Context` / `LookupSubjectsRequest.Context` accept structpb but the engine methods don't surface them; caveat-reaching permissions therefore return `CONDITIONAL_PERMISSION` for every result (deferred). Filter or surface `LookupPermissionship == CONDITIONAL_PERMISSION` distinctly from `HAS_PERMISSION` — currently `pkg/authz/spicedb/crud.go` appends every result regardless of `Permissionship`, so caveated lookups silently return false-positives until both Lookup-with-Context AND CONDITIONAL filtering ship together (deferred — recommended same job per AUZ-007 Discoveries). Surface `CONDITIONAL_PERMISSIONSHIP` distinctly from hard deny in the check path (the empirical probe confirms SpiceDB returns `missing_required_context: "<field>"` on `PartialCaveatInfo` — preserving that signal is a follow-up). Generate per-field pointer types within `<CaveatPascal>Args` for fine-grained partial binding — **lifted post-AUZ-007** — see Discoveries.

---

## Interface Contracts

### `Engine` interface — new method

`pkg/authz/authz.go`:

```go
type Engine interface {
    // ... existing methods unchanged ...
    CreateRelationsWithCaveat(
        ctx context.Context,
        to Resource,
        relation Relation,
        subject Type,
        ids []ID,
        caveatName string,
        caveatParams map[string]any,
    ) error
}
```

Mirrors `CreateRelations` plus two trailing parameters: `caveatName` (the prefixed caveat identifier known at codegen time) and `caveatParams` (the user-supplied pre-binding, may be nil/empty for direct callers who want to attach name-only and defer all binding to check time).

### `*spicedb.Engine` implementation

`pkg/authz/spicedb/crud.go`:

```go
func (e *Engine) CreateRelationsWithCaveat(
    ctx context.Context,
    to authz.Resource,
    relation authz.Relation,
    subject authz.Type,
    ids []authz.ID,
    caveatName string,
    caveatParams map[string]any,
) error {
    e.debugLog("Creating caveated relations: to=%v, relation=%v, subject=%v, ids=%v, caveat=%s, params=%v",
        to, relation, subject, ids, caveatName, caveatParams)

    caveatCtx, err := serializeCaveatMap(caveatParams)
    if err != nil {
        return fmt.Errorf("serialize caveat params: %w", err)
    }

    updates := make([]*v1.RelationshipUpdate, 0, len(ids))
    for _, id := range ids {
        updates = append(updates, &v1.RelationshipUpdate{
            Operation: v1.RelationshipUpdate_OPERATION_CREATE,
            Relationship: &v1.Relationship{
                Resource: &v1.ObjectReference{
                    ObjectType: string(to.Type),
                    ObjectId:   string(to.ID),
                },
                Relation: string(relation),
                Subject: &v1.SubjectReference{
                    Object: &v1.ObjectReference{
                        ObjectType: string(subject),
                        ObjectId:   string(id),
                    },
                },
                OptionalCaveat: &v1.ContextualizedCaveat{
                    CaveatName: caveatName,
                    Context:    caveatCtx,
                },
            },
        })
    }

    res, err := e.client.WriteRelationships(ctx, &v1.WriteRelationshipsRequest{Updates: updates})
    if err != nil {
        return err
    }
    e.setToken(res.WrittenAt.Token)
    return nil
}
```

`serializeCaveatMap` already exists from AUZ-006 — no changes. `OptionalCaveat.Context` is `nil` when `caveatParams` is nil/empty (matches AUZ-006 check-side semantics: empty map ↔ no pre-binding).

### Generated `<Rel>Objects` struct — nested `Caveats` sub-struct mirroring `Wildcards`

For a relation with at least one caveated allowed type, the `<Rel>Objects` struct gains a `Caveats <RelName>Caveats` field grouped after the ID-slice fields and after the optional `Wildcards` sub-struct, exactly mirroring the existing Wildcards pattern. The sub-struct holds one `<TypeName> *<CaveatPascal>Args` field per caveated allowed type.

Single caveated allowed type (`tenanted_viewer: extsvc/user with extsvc/tenant_match`):

```go
// BEFORE (AUZ-006):
type FolderTenantedViewerObjects struct {
    User []User
}

// AFTER (SPEC-003):
type FolderTenantedViewerObjects struct {
    User    []User
    Caveats FolderTenantedViewerCaveats
}
type FolderTenantedViewerCaveats struct {
    User *TenantMatchArgs
}
```

Multi-allowed-type with mixed caveated/non-caveated branches (`foo: user with cav_a | group with cav_b | role`):

```go
type FolderFooObjects struct {
    User    []User
    Group   []Group
    Role    []Role
    Caveats FolderFooCaveats
}
type FolderFooCaveats struct {
    User  *CavAArgs   // caveated branches only — Role omitted
    Group *CavBArgs
}
```

Wildcard + caveat (`relation guarded: extsvc/user:* with extsvc/tenant_match`):

```go
type FolderGuardedObjects struct {
    User      []User
    Wildcards FolderGuardedWildcards
    Caveats   FolderGuardedCaveats
}
type FolderGuardedCaveats struct {
    User *TenantMatchArgs
}
```

The same `Caveats.User` field is consumed by both the regular (`if len(objects.User) > 0`) and wildcard (`if objects.Wildcards.User`) branches when User is caveated. Field naming inside `<RelName>Caveats` uses the allowed-type name (`<TypeName>`), not the caveat-args struct name, so two allowed types of the same Go-name shape but gated by different caveats remain disambiguable. The `<CaveatPascal>Args` struct is reused from AUZ-006 — same per-namespace declaration, no duplication.

### Generated `Create<Rel>Relations` method — per-type routing

For each allowed type, the method's body switches on whether the type has a `CaveatName`. Caveat-bearing branches build a context map only when the corresponding `objects.Caveats.<TypeName>` field is non-nil; a nil sub-struct field translates to `OptionalCaveat.Context = nil` on the wire — a SpiceDB-supported "name-only attach" that defers that caveat's binding to check time.

```go
// BEFORE (AUZ-006, no caveat path):
func (folder Folder) CreateTenantedViewerRelations(ctx context.Context, objects FolderTenantedViewerObjects) error {
    if len(objects.User) > 0 {
        err := authz.GetEngine(ctx).CreateRelations(ctx, authz.Resource{
            Type: TypeFolder, ID: authz.ID(folder),
        }, authz.Relation(FolderTenantedViewer), TypeUser, authz.IDs(objects.User))
        if err != nil {
            return err
        }
    }
    return nil
}

// AFTER (SPEC-003):
func (folder Folder) CreateTenantedViewerRelations(ctx context.Context, objects FolderTenantedViewerObjects) error {
    if len(objects.User) > 0 {
        var caveatCtx map[string]any
        if objects.Caveats.User != nil {
            caveatCtx = map[string]any{
                "tenant": objects.Caveats.User.Tenant,
            }
        }
        err := authz.GetEngine(ctx).CreateRelationsWithCaveat(ctx, authz.Resource{
            Type: TypeFolder, ID: authz.ID(folder),
        }, authz.Relation(FolderTenantedViewer), TypeUser, authz.IDs(objects.User),
            "extsvc/tenant_match", caveatCtx)
        if err != nil {
            return err
        }
    }
    return nil
}
```

Wildcard + caveat branch (when allowed type has both `IsWildcard` and `CaveatName`):

```go
if objects.Wildcards.User {
    var caveatCtx map[string]any
    if objects.Caveats.User != nil {
        caveatCtx = map[string]any{
            "tenant": objects.Caveats.User.Tenant,
        }
    }
    err := authz.GetEngine(ctx).CreateRelationsWithCaveat(ctx, authz.Resource{
        Type: TypeFolder, ID: authz.ID(folder),
    }, authz.Relation(FolderGuarded), TypeUser, []authz.ID{authz.WildcardID},
        "extsvc/tenant_match", caveatCtx)
    if err != nil {
        return err
    }
}
```

For non-caveated allowed types (caveat-free relations or non-caveated branches of mixed relations), the generated branch continues to call `CreateRelations` with the existing 4-positional argument list. The two paths coexist within one method body — one `if` block per allowed type and one per wildcard, each routing independently.

### Generated `Check<Perm>Inputs` and `Check<Perm>` — multi-caveat support

When a permission reaches one or more caveats (per `permCaveats`), the generated `Check<Perm>Inputs` gains a `Caveats Check<Perm>Caveats` sub-struct. The sub-struct holds one `<CaveatPascal> *<CaveatPascal>Args` field per **unique caveat name** reachable from the permission. The `Check<Perm>` body lazily allocates a single map and merges every non-nil entry — SpiceDB's wire Context is a key-bag, and each tuple's caveat picks up only the keys it needs (per A6).

Single-caveat permission (`tenanted_browse = tenanted_viewer`, where `tenanted_viewer: user with tenant_match`):

```go
type CheckFolderTenantedBrowseInputs struct {
    User    []User
    Caveats CheckFolderTenantedBrowseCaveats
}
type CheckFolderTenantedBrowseCaveats struct {
    TenantMatch *TenantMatchArgs
}
```

Multi-caveat permission (`multi_check = tenanted_user + windowed_user`, reaching both `tenant_match` and `within_window`):

```go
type CheckFolderMultiCheckInputs struct {
    User    []User
    Caveats CheckFolderMultiCheckCaveats
}
type CheckFolderMultiCheckCaveats struct {
    TenantMatch  *TenantMatchArgs
    WithinWindow *WithinWindowArgs
}
```

The generated `CheckMultiCheck` body merges supplied caveats:

```go
func (folder Folder) CheckMultiCheck(ctx context.Context, input CheckFolderMultiCheckInputs) (bool, error) {
    if len(input.User) == 0 && true {
        return false, authz.ErrNoInput
    }

    var caveatCtx map[string]any
    if input.Caveats.TenantMatch != nil {
        if caveatCtx == nil { caveatCtx = map[string]any{} }
        caveatCtx["tenant"] = input.Caveats.TenantMatch.Tenant
    }
    if input.Caveats.WithinWindow != nil {
        if caveatCtx == nil { caveatCtx = map[string]any{} }
        caveatCtx["allowed_actions"]  = input.Caveats.WithinWindow.AllowedActions
        caveatCtx["requested_action"] = input.Caveats.WithinWindow.RequestedAction
    }

    if len(input.User) > 0 {
        err := authz.GetEngine(ctx).CheckPermissionWithCaveat(ctx, authz.Resource{
            Type: TypeFolder, ID: authz.ID(folder),
        }, authz.Permission(FolderMultiCheck), TypeUser, authz.IDs(input.User), caveatCtx)
        if err != nil { return false, err }
    }
    return true, nil
}
```

If both `Caveats.X` fields are nil, `caveatCtx` stays nil — `CheckPermissionWithCaveat` then sends `Context: nil` and SpiceDB returns `CONDITIONAL_PERMISSION` for any tuple whose caveat needs unsupplied keys (`errorIfDenied` maps that to `ErrPermissionDenied`).

### `detectPermCaveatCollisions` — codegen-time guard

If two caveats reachable from the same permission declare the same parameter name, the generated merge would silently last-write-wins (one caveat's value would clobber the other on the wire). The codegen errors at gen time:

```
permission extsvc/folder/p reaches caveats "extsvc/cav_a" and "extsvc/cav_b"
which both declare parameter "tenant" — rename one in the schema to disambiguate
```

The schema author resolves by renaming. Single-caveat permissions and multi-caveat permissions with disjoint parameter sets are unaffected.

### Template `FuncMap` — additions for nested `Caveats` and multi-caveat permissions

New helpers added on top of AUZ-006's `pascalCaveat` + `caveatParams`:

- `anyCaveat(types []AllowedType) bool` — mirrors `anyWildcard`; gates emission of the `Caveats <RelName>Caveats` sub-struct.
- `permCaveats(objectType, perm string) []string` — replaces AUZ-006's single-string `permCaveat`. Returns the sorted slice of unique caveat names reachable from the permission.
- `hasPermCaveats(objectType, perm string) bool` — quick guard for `Check<Perm>Inputs.Caveats` emission.

Generator-side: `collectPermCaveats` returns `map[string][]string` (was `map[string]string` with multi-caveat error). A new `detectPermCaveatCollisions(permCaveats, caveatMap)` runs after collection — errors when 2+ reachable caveats share a parameter name.

### `cmd/authzed-codegen/main.go` — no change

The CLI wiring established in AUZ-006 (`AdaptDefinitions(compiled.CaveatDefinitions, compiled.ObjectDefinitions)` returning the caveat map, threaded into `NewGenerator`) is sufficient. SPEC-003 reads the same `AllowedType.CaveatName` field that AUZ-006 already populates.

---

## Sequence

Runtime flow when a caller writes to a caveated relation. The example
schema is `tenanted_viewer: extsvc/user with extsvc/tenant_match` and
the caller wants to grant tenant `"acme"` to two user IDs.

```
caller code:

    folder.CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
        User: []User{"u1", "u2"},
        Caveats: extsvc.FolderTenantedViewerCaveats{
            User: &TenantMatchArgs{Tenant: "acme"},
        },
    })
         │
         ▼
generated method body — iterates each allowed type independently:

    for AllowedType{Namespace="extsvc/user", CaveatName="extsvc/tenant_match"}:
         │
         ├─► if len(objects.User) == 0          → skip branch
         │
         ├─► caveatCtx := nil
         │   if objects.Caveats.User != nil:
         │     caveatCtx = map[string]any{"tenant": objects.Caveats.User.Tenant}
         │   (nil → OptionalCaveat.Context = nil on wire = name-only attach, defer to check time)
         │
         └─► authz.GetEngine(ctx).CreateRelationsWithCaveat(
                 ctx,
                 authz.Resource{Type: TypeFolder, ID: authz.ID(folder)},
                 authz.Relation(FolderTenantedViewer),
                 TypeUser,
                 authz.IDs([]User{"u1", "u2"}),
                 "extsvc/tenant_match",   // codegen-known caveat name (literal)
                 caveatCtx,                // user-supplied pre-binding
             )
                  │
                  ▼
*spicedb.Engine.CreateRelationsWithCaveat:
                  │
                  ├─► caveatCtx, err := serializeCaveatMap(caveatCtx)
                  │     │
                  │     ├─► nil/empty   → returns (nil, nil)         ▶ Context: nil on wire
                  │     └─► non-empty   → structpb.NewStruct(map)    ▶ Context: structpb on wire
                  │
                  ├─► build one *v1.RelationshipUpdate per id, each carrying:
                  │     Operation:    OPERATION_CREATE
                  │     Resource:     {ObjectType: "extsvc/folder", ObjectId: <folder>}
                  │     Relation:     "tenanted_viewer"
                  │     Subject:      {ObjectType: "extsvc/user",   ObjectId: <id>}
                  │     OptionalCaveat:
                  │       CaveatName: "extsvc/tenant_match"
                  │       Context:    <caveatCtx>
                  │
                  ├─► client.WriteRelationships(ctx, &Request{Updates: [...]})
                  │
                  ├─► on success: e.setToken(res.WrittenAt.Token)
                  │
                  └─► return nil
```

When the relation has multiple allowed types with different caveats
(e.g. `foo: user with cav_a | group with cav_b | role`), the generated
method runs three independent branches in declaration order: User
through `CreateRelationsWithCaveat("extsvc/cav_a", caveatCtx)` reading
from `objects.Caveats.User`, Group through `CreateRelationsWithCaveat("extsvc/cav_b", caveatCtx)`
reading from `objects.Caveats.Group`, and Role through the existing
`CreateRelations` (no caveat). Each branch's nil-guard reads its own
sub-struct field independently.

Wildcard + caveat shares the Caveats sub-struct field. When the
allowed type has both `IsWildcard=true` and a non-empty `CaveatName`,
the wildcard branch (`if objects.Wildcards.User`) runs the same
nil-guard against `objects.Caveats.User`, builds the same ctx map,
and calls `CreateRelationsWithCaveat` with `[]authz.ID{authz.WildcardID}`
as the single ID. The User and Wildcards branches in the same method
write two separate tuples (regular + wildcard) but both consume one
`objects.Caveats.User` value.

For `Check<Perm>` calls against multi-caveat permissions, the merge
logic is per-caveat: the body iterates `input.Caveats.<CaveatPascal>`
fields and merges every non-nil entry into one wire `Context` map.
SpiceDB's evaluator routes keys to whichever tuple's caveat consumes
them. Nil sub-struct fields contribute nothing to the wire map; the
final `caveatCtx` is the union of all supplied caveats' parameters
(per A6).

```
codegen flow at build time (unchanged from AUZ-006):

    example/schema.zed
         │
         ▼
    compiler.Compile() → *CompiledSchema
         CaveatDefinitions: []*core.CaveatDefinition
         ObjectDefinitions: []*core.NamespaceDefinition
         │
         ▼
    AdaptDefinitions(caveatDefs, objDefs) → (defs, caveatMap, err)
         │
         ▼
    NewGenerator(caveatMap, defs)
         │
         ▼
    template execution
         ├─► <CaveatPascal>Args struct (per namespace, per unique caveat)
         ├─► <Rel>Objects struct
         │     ├─► <TypeName> []<TypeName> field per allowed type
         │     ├─► Wildcards sub-struct if any allowed type is wildcard
         │     └─► Caveats <RelName>Caveats sub-struct  ◀── NEW (this SPEC)
         │            └─► <TypeName> *<CaveatPascal>Args per caveated allowed type
         │
         ├─► Check<Perm>Inputs struct
         │     ├─► <TypeName> []<TypeName> field per reachable input type
         │     └─► Caveats Check<Perm>Caveats sub-struct  ◀── NEW (this SPEC)
         │            └─► <CaveatPascal> *<CaveatPascal>Args per unique reachable caveat
         │
         └─► Create<Rel>Relations + Check<Perm> method bodies
               ├─► per allowed-type branch (existing)
               ├─► per allowed-type-with-caveat branch  ◀── NEW
               │     ├─► build map[string]any from objects.Caveats.<TypeName> (nil → defer)
               │     └─► CreateRelationsWithCaveat(...)
               ├─► per wildcard branch (caveat-aware extension)
               └─► Check<Perm>: lazy-alloc map + merge from input.Caveats.<CaveatPascal> per reachable caveat
```

---

## Errors

| Error class | Trigger | Layer |
|---|---|---|
| `"serialize caveat params: <wrapped>"` | `serializeCaveatMap` is called with `caveatParams` carrying protobuf-incompatible values (functions, channels, custom structs not serializable to `structpb`) | Engine (`*spicedb.Engine.CreateRelationsWithCaveat`) |
| Unknown caveat reference (codegen-time) | The schema declares a relation with `with <name>` where `<name>` has no matching `CaveatDefinition` | Adapter (`AdaptDefinitions`) — pre-existing AUZ-006 check |
| Cross-caveat parameter-name collision (codegen-time) | A permission reaches 2+ caveats that declare the same parameter name. Error message names the permission, both caveats, and the conflicting key | Generator (`detectPermCaveatCollisions`) — fail-loud at gen time so the schema author renames in one of the caveats |
| SpiceDB wire rejection | `client.WriteRelationships` returns a gRPC error — wrong caveat name pinned to a tuple, malformed relationship, transport failure | Engine — passed through unwrapped; caller distinguishes via `status.Code(err)` |

The codegen wrapper is intentionally permissive on `objects.Caveats.<TypeName> = nil` and `input.Caveats.<CaveatPascal> = nil` — empirical verification (per A6, [B1]) confirms `OptionalCaveat{CaveatName, Context: nil}` is a wire-legal SpiceDB pattern equivalent to "attach this caveat name, defer all parameter binding to check time." Erroring at the codegen boundary for a wire-legal pattern would block legitimate use cases (e.g. caveats whose parameters are exclusively request-data, set by the caller at check time). The trade-off accepted: callers who intend to pre-bind policy values but forget to populate the sub-struct field get an `eval false → deny` at runtime instead of an API-boundary error. Integration tests catch this; the wire-time `eval false → CheckPermissionResponse` path is the same path that catches every other "caveat parameter has the wrong value" mistake (e.g. typo'd `Tenant: "acm"` instead of `"acme"`), so adding strictness specifically for nil would be inconsistent.

Pre-existing errors not in this SPEC's scope: `authz.ErrNoInput` (caveat-free; raised in `Check<Perm>` when no input IDs of any allowed type are supplied), `authz.ErrPermissionDenied` (read-side; AUZ-006 surface). Neither fires on the write path and neither is modified.

---

## Constraints

- **C1.** The caveat name in the generated `CreateRelationsWithCaveat` call is a string literal embedded by the codegen (e.g. `"extsvc/tenant_match"`). The caveat identity is part of the generated code's contract — callers cannot substitute a different caveat through the typed API. Per A1.

- **C2.** The `<Rel>Objects.Caveats.<TypeName>` field is per-allowed-type, per-call. Its value applies uniformly to every ID in `objects.<TypeName>` and to the wildcard branch within the same `Create<Rel>Relations` invocation; SpiceDB's wire format pins `OptionalCaveat` per `RelationshipUpdate`, so the same caveat name + context is sent for every ID written in that call. Per A2.

- **C3.** Multi-caveat per relation is supported on the write side. Each caveated allowed type is routed through its own `CreateRelationsWithCaveat` call with its own caveat name and its own pre-context — branches are independent, no shared state.

- **C4.** Multi-caveat per permission is supported on the read side. The generated `Check<Perm>Inputs.Caveats` sub-struct holds one `<CaveatPascal> *<CaveatPascal>Args` field per unique caveat name reachable from the permission. The `Check<Perm>` body lazily allocates a single map and merges every non-nil entry. SpiceDB's wire `Context` is a shared key-bag where each tuple's caveat picks up only the keys it needs (per A6) — no per-tuple Context routing required at the gRPC layer.

- **C5.** `Delete<Rel>Relations` is unchanged. SpiceDB's `OPERATION_DELETE` matches by 6-column tuple identity and does not consume `OptionalCaveat` — deletes against caveated relations continue to call the existing `Engine.DeleteRelations` with the caveat name implicit in the relation. Per A3.

- **C6.** Caveat-free schemas regenerate byte-identically across runs. All new template branches are guarded by `{{ if $relType.CaveatName }}` / `{{ if hasPermCaveats … }}`; no caveated allowed types and no caveat-reaching permissions means zero new emission, preserving the AUZ-006 round-trip invariant for the existing fixture surface (`bookingsvc`, `menusvc`, the non-caveat parts of `extsvc`).

- **C7.** Both `Engine.CreateRelationsWithCaveat` and the codegen wrapper are permissive on nil. `caveatParams = nil` (engine) and `objects.Caveats.<TypeName> = nil` / `input.Caveats.<CaveatPascal> = nil` (codegen) all produce `OptionalCaveat.Context = nil` on the wire — equivalent to "attach this caveat name, defer all parameter binding to check time." This is the design SpiceDB itself models (per A6) and is required to support caveats whose parameters are exclusively request-data. Per-key write-time precedence (A6, [A3]) means callers cannot bypass policy by omitting binding — write-time wins on collision, but unbound keys are filled by check time.

- **C8.** Field naming inside the `<RelName>Caveats` sub-struct uses the allowed-type's PascalCase TypeName (e.g. `Caveats.User`, `Caveats.Group`); inside the `Check<Perm>Caveats` sub-struct it uses the caveat's PascalCase name (e.g. `Caveats.TenantMatch`, `Caveats.WithinWindow`). The asymmetry reflects the actual multiplicity — write-side caveats are scoped per-allowed-type (a tuple has at most one caveat, attached to one `(resource, relation, subject)` row), while read-side caveats are scoped per-name across all reachable tuples (the wire `Context` is a shared key-bag where SpiceDB matches keys to whichever tuple needs them).

- **C9.** Sub-struct field order is deterministic. `<RelName>Caveats` fields follow the schema declaration order of the relation's allowed types. `Check<Perm>Caveats` fields follow the alphabetical order of unique caveat names returned by `permCaveats` (which sorts the slice). Both guarantee idempotent codegen output regardless of map iteration ordering.

- **C10.** Wildcard + caveat shares the `Caveats.<TypeName>` field. When an allowed type has both `IsWildcard=true` and a non-empty `CaveatName`, the same sub-struct field is consumed by the regular branch (`if len(objects.<TypeName>) > 0`) and the wildcard branch (`if objects.Wildcards.<TypeName>`) — two writes from one Caveat input.

- **C11.** Cross-caveat parameter-name collisions error at codegen time. `detectPermCaveatCollisions` scans every permission with 2+ reachable caveats; if any two of those caveats declare the same parameter name, the codegen errors with the offending permission, both caveat names, and the conflicting key. Reason: the wire `Context` is a single shared map — merging two caveats that both want to write the same key is silently last-wins, which would be invisibly wrong at runtime. Failing loud at gen time forces the schema author to rename in one caveat to disambiguate.

---

## Assumptions

- **A1 [VERIFIED]:** `authzed-go` v1.9 `Relationship` carries `OptionalCaveat *ContextualizedCaveat` and `ContextualizedCaveat` has `CaveatName string` + `Context *structpb.Struct`. This is the wire shape the codegen emits in `OptionalCaveat`. Evidence: `go doc github.com/authzed/authzed-go/proto/authzed/api/v1 ContextualizedCaveat` and `Relationship` confirm both fields with the documented semantics ("caveat_name is the name of the caveat expression to use, as defined in the schema"; "context consists of any named values that are defined at write time").

- **A2 [VERIFIED]:** `OptionalCaveat` is per-`Relationship` (and therefore per-`RelationshipUpdate` in `WriteRelationships`). The same caveat name + Context is sent for every ID written in one `CreateRelationsWithCaveat` invocation that loops over IDs. Evidence: `go doc github.com/authzed/authzed-go/proto/authzed/api/v1 RelationshipUpdate` shows one `Relationship` per update; the codegen builds N updates for N IDs in one call, each with the identical `OptionalCaveat` structure.

- **A3 [VERIFIED]:** `Engine.DeleteRelations` (which emits `OPERATION_DELETE` without `OptionalCaveat`) correctly removes caveated tuples. SpiceDB's deletion matches on the 6-column tuple identity `(resource_type, resource_id, relation, subject_type, subject_id, subject_relation)` and ignores caveat metadata for the match. This is the basis for **C5**. Evidence: memdb backend `internal/datastore/memdb/readwrite.go:66-75` uses an `indexID` lookup over those 6 fields with no caveat columns, then `tx.Delete(existing)` at lines 122-126 regardless of caveat metadata; postgres backend `internal/datastore/postgres/readwrite.go:779-788` (`exactRelationshipClause`, used for DELETE at line 205) builds a SQL WHERE clause over the same 6 columns — `ColCaveatName` and `ColCaveatContext` are deliberately excluded.

- **A4 [VERIFIED]:** `serializeCaveatMap` (introduced by AUZ-006 in `pkg/authz/spicedb/crud.go`) returns `(nil, nil)` for nil/empty input and `*structpb.Struct` for non-empty input. The new `CreateRelationsWithCaveat` reuses this helper and inherits its empty-map semantics. Evidence: `pkg/authz/spicedb/crud.go:54-65` (the helper) plus AUZ-006 e2e tests `TestFolder_CheckTenantedBrowse_*` exercising both empty and populated paths.

- **A5 [VERIFIED]:** SpiceDB's schema grammar allows multiple allowed types per relation, each with its own optional `with <caveat>` clause. A relation `foo: user with cav_a | group with cav_b | role` is legal and compiles. Evidence: existing AUZ-006 fixture (`extsvc/folder.tenanted_viewer`) uses one caveated allowed type; the grammar imposes no cap on the count of distinct caveats across allowed types. This permits **C3**'s multi-caveat-per-relation write-side support. Wildcard + caveat (`type:* with caveat`) is also schema-legal — the Authzed docs example `relation viewer: user:* with has_matching_group_id` confirms it.

- **A6 [VERIFIED]:** SpiceDB's caveat context resolution is per-key union with write-time precedence on key collisions; nil write-time `Context` (with `CaveatName` set) is a legitimate SpiceDB-supported pattern equivalent to "defer all binding to check time"; `PERMISSIONSHIP_CONDITIONAL_PERMISSION` surfaces the unresolved parameter names via `PartialCaveatInfo.MissingRequiredContext`. This permits **C7**'s permissive nil semantics in both layers and the codegen's "nil = name-only attach" design choice. Evidence: empirical probe against SpiceDB v1.52 (memdb backend) — write `{policy_key="expected_policy"}` + check `{request_key="expected_request"}` returned `HAS_PERMISSION` ([A1]); write `{policy_key=expected}` + check `{policy_key="ATTACKER", request_key="ok"}` still returned `HAS_PERMISSION` proving write-time wins on collision ([A3]); write with `Context: nil` + check supplying both keys returned `HAS_PERMISSION` ([B1]); CONDITIONAL responses included `missing_required_context: "request_key"` ([A2, B2]); same-key collision write `{shared="winner"}` + check `{shared="loser"}` evaluated to `winner == winner = true` returning `HAS_PERMISSION` ([C1]). All matches the documented model in the Authzed docs ("relationship-stored values take precedence").

---

## Unresolved Questions

(none)

---

## Summary

This SPEC closes AUZ-006's write-side gap and lifts the per-permission caveat cap, unifying both sides of the codegen surface around a nested `Caveats` sub-struct that mirrors the existing `Wildcards` pattern. **The net change spans seven files:**

| File | Change |
|---|---|
| `internal/templates/object.go.tmpl` | Emit `Caveats <RelName>Caveats` sub-struct on `<Rel>Objects` (one `<TypeName> *<CaveatPascal>Args` field per caveated allowed type). Per-type routing in `Create<Rel>Relations` (regular + wildcard) to `CreateRelationsWithCaveat`. Emit `Caveats Check<Perm>Caveats` sub-struct on `Check<Perm>Inputs` (one `<CaveatPascal> *<CaveatPascal>Args` field per unique reachable caveat). `Check<Perm>` body lazy-allocates and merges every non-nil entry. All paths guarded — caveat-free schemas regen byte-identically. |
| `internal/generator/adapter.go` | `collectPermCaveats` returns `map[string][]string` (sorted unique caveat names per permission). `detectPermCaveatCollisions` errors at codegen when 2+ reachable caveats share a parameter name. |
| `internal/generator/generator.go` | Template helpers `anyCaveat`, `permCaveats`, `hasPermCaveats`. `Generator.PermCaveats` field carries the slice map. |
| `pkg/authz/authz.go` | Add `Engine.CreateRelationsWithCaveat` to the interface. |
| `pkg/authz/spicedb/crud.go` | Implement `*Engine.CreateRelationsWithCaveat`. `serializeCaveatMap` extended with `coerceStructpbMap` / `coerceStructpbValue` (handles typed slices and nested structures via reflection fallback; short-circuits `[]byte` for native base64). |
| `example/schema.zed` | New caveat fixtures spanning all supported parameter types (`string`, `bool`, `int`, `uint`, `double`, `bytes`, `list<T>`, nested `list<list<T>>`); single + wildcard + multi-allowed-type relations; multi-caveat-per-permission fixture (`multi_check`). |
| `example/authzed/extsvc/folder.gen.go` | Regenerated output. |

23 e2e tests against live SpiceDB cover: defer/pre-bind binding patterns, wildcard + caveat, mixed caveated/non-caveated relations, multi-caveat-per-permission, write-time precedence, delete-on-caveated-tuple, all supported parameter types, structpb coercion edge cases.
