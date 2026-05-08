# [SPEC-002] Caveat Codegen

| Field     | Value                                        |
|-----------|----------------------------------------------|
| Status    | Draft                                         |
| Created   | 2026-05-08                                    |
| Author    | Danh Tran                                      |
| Implements| (follow-up to ADR-001)                         |

---

## Overview

This SPEC defines the codegen and runtime changes for **caveats** — parameterized conditional gates on AuthZED relations. A relation declared `relation viewer: user with can_view` compiles to a typed `CheckXInputs` with caveat parameter fields, a new `CheckPermissionWithCaveat(ctx, dest, has, subj, ids, caveat map[string]any) error` on the `authz.Engine` interface, and generated code that extracts the typed caveat struct into a map and passes it through to the new engine method.

**What this component does:** Accept `with <caveat>` in schema relations. The adapter extracts the caveat name from `AllowedRelation.GetRequiredCaveat().GetCaveatName()` and the parameter names/types from `CompiledSchema.CaveatDefinitions`. The adapter replaces the current "caveats are not supported" rejection with an `AllowedType.CaveatName string` field. The generator emits a `CaveatArgs` struct per namespace per caveat, adds the caveat field to `CheckXInputs`, and generates the call to `CheckPermissionWithCaveat`. The engine implementation serializes the map to `structpb.Struct` and sends it as `CheckPermissionRequest.Context`.

**What this component does not do:** Emit caveat evaluation code (the SpiceDB server evaluates the CEL expression). Support caveats on permissions (not relations). Handle stored/snapshot caveats. Generate tests for caveat evaluation correctness. Support expiration traits, sub-relation references, or caveats with functioned tuple-to-userset.

---

## Interface Contracts

### Changes to `internal/generator/adapter.go`

**New field on `AllowedType`:**

```go
type AllowedType struct {
    Namespace  string
    IsWildcard bool
    CaveatName string   // NEW: non-empty if relation has "with <caveat>"
}
```

**`flattenAllowedTypes` — remove the rejection, capture the caveat name:**

```go
// BEFORE (current):
func flattenAllowedTypes(ti *core.TypeInformation) ([]AllowedType, error) {
    for _, ar := range ti.GetAllowedDirectRelations() {
        if ar.GetRequiredCaveat() != nil {
            return nil, fmt.Errorf("caveats are not supported (allowed type %q)", ar.GetNamespace())
        }
        // ...
    }
}

// AFTER (key change):
func flattenAllowedTypes(ti *core.TypeInformation) ([]AllowedType, error) {
    for _, ar := range ti.GetAllowedDirectRelations() {
        caveatName := ""
        if ar.GetRequiredCaveat() != nil {
            caveatName = ar.GetRequiredCaveat().GetCaveatName()
        }
        // ...
        types = append(types, AllowedType{
            Namespace:  ar.GetNamespace(),
            IsWildcard: ar.GetPublicWildcard() != nil,
            CaveatName: caveatName,    // NEW
        })
    }
}
```

**`AdaptDefinitions` — new parameter `caveatDefs` and new function `buildCaveatMap`:**

```go
func AdaptDefinitions(caveatDefs []*core.CaveatDefinition, defs []*core.NamespaceDefinition) ([]*DefinitionView, error)
```

The function signature gains a leading `caveatDefs` parameter. Inside, `buildCaveatMap` converts the list to a map keyed by caveat name, value is the list of parameter names:

```go
func buildCaveatMap(caveatDefs []*core.CaveatDefinition) map[string][]string {
    m := make(map[string][]string)
    for _, cd := range caveatDefs {
        params := make([]string, 0, len(cd.GetParameterTypes()))
        for pname := range cd.GetParameterTypes() {
            params = append(params, pname)
        }
        m[cd.GetName()] = params
    }
    return m
}
```

The `ObjectDefinitions` loop is unchanged. The `caveatDefs` map is threaded through to the template.

### New type on `pkg/authz/authz.go`

**New method on the `Engine` interface:**

```go
type Engine interface {
    // ... existing methods unchanged ...
    CheckPermissionWithCaveat(ctx context.Context, dest Resource, has Permission, subj Type, audIDs []ID, caveatParams map[string]any) error
}
```

This is the only interface change. All existing methods remain byte-identical. Callers without caveats call `CheckPermission`; callers with caveats call `CheckPermissionWithCaveat`.

**New implementation in `pkg/authz/spicedb/crud.go`:**

```go
func (e *Engine) CheckPermissionWithCaveat(ctx context.Context, dest authz.Resource, has authz.Permission, subj authz.Type, audIDs []authz.ID, caveatParams map[string]any) error {
    consistency := e.getConsistencySnapshot()
    ctxStruct, err := serializeCaveatMap(caveatParams)
    if err != nil {
        return err
    }
    for _, id := range audIDs {
        err := errorIfDenied(e.client.CheckPermission(ctx, &v1.CheckPermissionRequest{
            Consistency:  consistency,
            Resource:     &v1.ObjectReference{ObjectType: string(dest.Type), ObjectId: string(dest.ID)},
            Permission:   string(has),
            Subject:      &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: string(subj), ObjectId: string(id)}},
            Context:      ctxStruct,    // <-- the caveat params
        }))
        if err != nil {
            return err
        }
    }
    return nil
}
```

**New helper `serializeCaveatMap`:**

```go
func serializeCaveatMap(m map[string]any) (*structpb.Struct, error) {
    if len(m) == 0 {
        return nil, nil
    }
    return structpb.NewStruct(m)
}
```

The helper is package-private to `pkg/authz/spicedb/`. It uses `google.golang.org/protobuf/types/structpb` which is already an indirect dependency of authzed-go.

### Changes to the template `internal/templates/object.go.tmpl`

**`CheckXInputs` struct — new `Caveat` field per caveat-bearing relation:**

For each relation with `CaveatName != ""`, the generated `CheckXInputs` gains a typed field:

```go
// BEFORE (no caveat):
type CheckFolderViewerInputs struct {
    User []User
}

// AFTER (with caveat):
type CheckFolderViewerInputs struct {
    User          []User
    Caveat        CanViewArgs   // typed params for "with can_view"
}
```

The `CanViewArgs` struct is generated in the same `.gen.go` file:

```go
type CanViewArgs struct {
    AllowedActions []string
}
```

**The generated `Check<Perm>` method — extract caveat params and call the new engine method:**

```go
func (folder Folder) CheckViewer(ctx context.Context, input CheckFolderViewerInputs) (bool, error) {
    // ... existing subject checks ...
    var ctxMap map[string]any
    if len(input.Caveat.AllowedActions) > 0 || len(input.Caveat.OtherParam) > 0 {
        ctxMap = map[string]any{
            "allowed_actions": input.Caveat.AllowedActions,
            "other_param":     input.Caveat.OtherParam,
        }
    }
    if err := authz.GetEngine(ctx).CheckPermissionWithCaveat(
        ctx, authz.Resource{Type: TypeFolder, ID: authz.ID(folder)},
        PermissionFolderViewer, TypeUser, authz.IDs(input.User),
        ctxMap,    // caveat params
    ); err != nil {
        return false, err
    }
    return true, nil
}
```

### `cmd/authzed-codegen/main.go` — pass `CaveatDefinitions` to the adapter

```go
// BEFORE:
defs, err := generator.AdaptDefinitions(compiled.ObjectDefinitions)

// AFTER:
defs, err := generator.AdaptDefinitions(compiled.CaveatDefinitions, compiled.ObjectDefinitions)
```

The `CaveatDefinitions` list is passed as the first argument. The adapter uses it to build the `caveatMap` (caveat name → list of parameter names).

---

## Sequence

```
authzed-codegen example/schema.zed --output example/authzed/
     │
     ├─► compiler.Compile() → *CompiledSchema
     │     ObjectDefinitions: []*core.NamespaceDefinition
     │     CaveatDefinitions: []*core.CaveatDefinition    // NEW
     │
     ├─► generator.AdaptDefinitions(caveatDefs, objDefs)
     │     │
     │     ├─► buildCaveatMap(caveatDefs) → map[string][]string
     │     │      can_view → ["allowed_actions", "subject_types"]
     │     │
     │     ├─► flattenAllowedTypes(typeInfo)
     │     │      extracts CaveatName from each AllowedRelation
     │     │      no longer errors on GetRequiredCaveat() != nil
     │     │
     │     └─► []*DefinitionView — same shape, CaveatName fields populated
     │
     ├─► generator.NewGenerator(defs)
     │     │
     │     └─► template execution with caveatMap passed through
     │
     ├─► template generates:
     │     ├─ CanViewArgs struct (from caveat params)
     │     └── CheckFolderViewerInputs with Caveat field
     │
     └─► WriteFile → example/authzed/foldersvc/folder.gen.go
```

---

## Errors

| Error Class        | Trigger                                          |
|--------------------|--------------------------------------------------|
| `ErrCaveatNotFound`| A relation references a `CaveatName` not present in `CaveatDefinitions` |
| `ErrSerialization` | `serializeCaveatMap` fails on `structpb.NewStruct` (malformed map value types) |
| Pre-existing: `ErrNoInput` | Unchanged. Pre-existing: `ErrPermissionDenied` — unchanged |
| Pre-existing errors from `flattenAllowedTypes` (caveats) | **Removed** — replaced by the `CaveatName` field |

The `ErrCaveatNotFound` error is returned by `AdaptDefinitions` when a relation's `CaveatName` does not appear in any `CaveatDefinition`. This catches schema-level mismatches at codegen time rather than silently failing at runtime.

---

## Constraints

- **C1.** The `Context` field on `CheckPermissionRequest` is shared across all subjects in a multi-ID check. The same `caveatParams` map is sent for each `audID`. This matches the SpiceDB wire contract — `Context` is on the request, not the subject.
- **C2.** The codegen does not validate caveat parameter types at codegen time. Parameter types come from `CaveatDefinition.ParameterTypes` which is a `map[string]*CaveatTypeReference` — the codegen reads parameter *names* only. Type validation happens at runtime (the SpiceDB server evaluates the expression).
- **C3.** The `CaveatName` field on `AllowedType` is empty string when the relation has no caveat. This is the zero-value and follows the same pattern as `CaveatName` on `AllowedCaveat` (`""` means no caveat).
- **C4.** The `serializeCaveatMap` helper uses `structpb.NewStruct` which requires all map values to be protobuf-compatible (strings, numbers, bools, slices, maps, nil). Non-protobuf types (custom structs, channels, functions) cause `ErrSerialization`. This is a hard constraint: caveat parameters must be serializable to protobuf Struct.
- **C5.** The generated `CaveatArgs` struct is per-namespace (one per `.gen.go` file per namespace). The struct name is derived from the caveat name (`CanViewArgs` for `can_view`). If two relations in the same namespace use the same caveat, they share the same struct type.
- **C6.** `CheckPermissionWithCaveat` has the same signature as `CheckPermission` but with an additional `caveatParams map[string]any` parameter. The `map[string]any` is the wire boundary — it exists at the engine method signature but is never seen by callers of the generated code.

---

## Generated Output Example

Given this schema:

```zed
caveat can_view(allowed_actions: list<string>, require_subject: bool) {
  true   // placeholder expression
}

definition folder {
    relation viewer: user with can_view
}
```

The generated `folder.gen.go` contains:

```go
package foldersvc

const TypeFolder authz.Type = "folder"

type RelationFolder authz.Relation
type PermissionFolder authz.Permission

const FolderViewer RelationFolder = "viewer"

type CanViewArgs struct {
    AllowedActions    []string
    RequireSubject    bool
}

type Folder authz.ID

func CheckFolderViewerInputs struct {
    User     []User
    Caveat   *CanViewArgs
}

func (folder Folder) CheckViewer(ctx context.Context, input CheckFolderViewerInputs) (bool, error) {
    var ctxMap map[string]any
    if input.Caveat != nil {
        ctxMap = map[string]any{
            "allowed_actions": input.Caveat.AllowedActions,
            "require_subject": input.Caveat.RequireSubject,
        }
    }
    if err := authz.GetEngine(ctx).CheckPermissionWithCaveat(
        ctx, authz.Resource{Type: TypeFolder, ID: authz.ID(folder)},
        PermissionFolderViewer, TypeUser, authz.IDs(input.User),
        ctxMap,
    ); err != nil {
        return false, err
    }
    return true, nil
}
```

---

## Constraints (continued)

- **C7.** The `CaveatArgs` struct is nullable (`*CanViewArgs` or `CanViewArgs` with zero value). A zero-value `CaveatArgs` passes an empty `ctxMap` (nil) to `CheckPermissionWithCaveat`, which the SpiceDB server treats as "no caveat parameters." This allows callers to omit the caveat field when the relation has no meaningful caveat for the check.

- **C8.** The `CheckPermissionWithCaveat` method on `*Engine` in `pkg/authz/spicedb/` handles `caveatParams == nil` or `caveatParams == empty map` by passing `nil` as `Context` to `CheckPermissionRequest`. This matches the wire contract for non-caveat checks.

- **C9.** The adapter's `buildCaveatMap` runs in O(N) where N is the number of `CaveatDefinitions`. This is a one-time cost at codegen time. Caveat definitions are bounded (production schemas typically have <20 caveats).

- **C10.** The template's `CheckXInputs` struct always includes the `Caveat` field when the relation has a `CaveatName`, even if the field's zero value is sufficient. This avoids conditional template logic and keeps the output deterministic.

---

## Assumptions

- **A1 [VERIFIED]:** `core.CaveatDefinition` has `Name string`, `SerializedExpression []byte`, and `ParameterTypes map[string]*CaveatTypeReference`. Evidence: `go doc` on the SpiceDB proto confirms all three fields exist on `CaveatDefinition`.
- **A2 [VERIFIED]:** `core.AllowedRelation.GetRequiredCaveat()` returns `*AllowedCaveat` with a `GetCaveatName() string` method. Evidence: `go doc` on `AllowedCaveat` in `core.pb.go` confirms the field and accessor.
- **A3 [VERIFIED]:** `CompiledSchema` from `compiler.Compile()` exposes `CaveatDefinitions []*core.CaveatDefinition` as a top-level field. Evidence: `compiler.go` line ~20 confirms the field exists on the returned struct.
- **A4 [VERIFIED]:** `CheckPermissionRequest.Context` is `*structpb.Struct` in authzed-go. Evidence: `permission_service.pb.go` in `authzed-go` v1.9.0 confirms the field type.
- **A5 [EXTERNAL FACT]:** The SpiceDB server evaluates the `SerializedExpression` as a CEL expression. The `Context` field on `CheckPermissionRequest` binds caveat parameters to the CEL evaluation context. Evidence: SpiceDB source code in `internal/caveats/run.go` deserializes and evaluates the expression.
- **A6 [EXTERNAL FACT]:** `structpb.NewStruct` requires all map values to be protobuf Struct-compatible types (string, number, bool, null, list, struct). Custom types, functions, and channels cause an error. Evidence: `google.golang.org/protobuf/protostruct` validation in `structpb/value.go`.
- **A7 [VERIFIED]:** authzed-go v1.9 `SubjectReference` has NO `Contextualization` or `ContextualizedCaveat` field. The only context-bearing field is on `CheckPermissionRequest` at the request level. Evidence: `core.pb.go` in `authzed-go` v1.9.0 confirms `SubjectReference` only has `Object` and `OptionalRelation` fields.
- **A8 [VERIFIED]:** The `Context` field on `CheckPermissionRequest` is shared across all subjects in a multi-ID check. Evidence: `CheckPermission` iterates over `audIDs` and sends the same request structure for each.

---

## Out of Scope

- **Caveat parameter validation at codegen time.** The adapter extracts parameter names from `CaveatDefinition` but does not validate their types or default values. Validation happens at the SpiceDB server during expression evaluation.

- **Caveat documentation generation.** No docs are generated alongside the `.gen.go` files explaining caveat usage. Rationale: generation time.

- **Caveat-only relations.** Relations that have ONLY a caveat (no allowed types, e.g. `relation viewer: with can_view`) are not tested. The adapter code handles them, but no test fixture exercises them.

- **Expiration traits (`with expiration`).** Deferred to a separate job. Expiration is a subset of caveats (it is a `CaveatDefinition` with an `expiration` parameter).

- **Sub-relation references (`relation foo: bar#baz`).** These are orthogonal to caveats. A relation can have both caveats and sub-relation references, but codegen for both is deferred.

- **Test coverage.** The scope does not include adding e2e tests for caveat behavior. The e2e tests for AUZ-005 cover non-caveat paths. Caveat tests are deferred to the implementation job.

- **Caveat caching.** The `serializeCaveatMap` is called once per `CheckPermissionWithCaveat` call. Per-subject caching within the same call is deferred. The wire boundary is the only `map[string]any` in the system.

---

## Summary

This SPEC adds caveats to the codegen surface: the adapter extracts `CaveatName` from `AllowedRelation` and parameter names from `CaveatDefinition`, replaces the "caveats are not supported" rejection with `AllowedType.CaveatName string`, generates `CaveatArgs` structs per namespace, adds a `Caveat` field to `CheckXInputs`, and extends the `Engine` interface with `CheckPermissionWithCaveat` that serializes the caveat map to `structpb.Struct` for the SpiceDB wire.

**The net change is bounded to five files:**

| File        | Change                                                  |
|-------------|---------------------------------------------------------|
| `adapter.go`   | Add `CaveatName string` to `AllowedType`; accept `caveatDefs` |
| `crud.go`      | Add `CheckPermissionWithCaveat` impl + `serializeCaveatMap` |
| `authz.go`     | Add `CheckPermissionWithCaveat` to `Engine` interface      |
| `object.go.tmpl` | Add `Caveat` field to `CheckXInputs`; emit caveat ctx map |
| `main.go`      | Pass `compiled.CaveatDefinitions` to `AdaptDefinitions`    |
