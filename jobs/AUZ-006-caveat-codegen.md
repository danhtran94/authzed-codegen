# AUZ-006: Caveat Codegen

| Field        | Value                                          |
|--------------|------------------------------------------------|
| Status       | Implemented + runtime-verified                  |
| Created      | 2026-05-08                                     |
| Assignee     | danhtran94                                     |
| Source       | docs/spec-002-caveat-codegen.md                |
| Implements   | (caveat support from ADR-001 rejection list)   |
| Depends on   | —                                              |

---

## Goal

Add caveats (`with <caveat>`) to the codegen surface. After this job:

1. The adapter accepts `with <caveat>` in schema relations instead of rejecting with "caveats are not supported".
2. `AllowedType` gains a `CaveatName string` field populated from `AllowedRelation.GetRequiredCaveat().GetCaveatName()`.
3. `AdaptDefinitions` gains a leading `caveatDefs []*core.CaveatDefinition` parameter and a `buildCaveatMap` helper that maps caveat names to typed parameter specs.
4. `cmd/authzed-codegen/main.go` passes `compiled.CaveatDefinitions` to `AdaptDefinitions` and to `NewGenerator`.
5. `pkg/authz/authz.go` gains `CheckPermissionWithCaveat(ctx, dest, has, subj, ids, caveatParams map[string]any) error` on the `Engine` interface.
6. `pkg/authz/spicedb/crud.go` implements `CheckPermissionWithCaveat` + `serializeCaveatMap` — serializes `map[string]any` to `*structpb.Struct` and sends as `CheckPermissionRequest.Context`.
7. The template generates `CaveatArgs` structs per namespace per caveat, adds a `Caveat *CaveatArgs` field to `CheckXInputs` when the relation has a caveat, and emits a `CheckPermissionWithCaveat` call when any caveat branch is reachable.
8. `go build ./...` and `go vet ./...` pass; `git diff --quiet example/authzed/` after regen exits 0 (round-trip is byte-identical because `example/schema.zed` has no `with <caveat>` clauses — caveat template branches stay unfired).

---

## Pre-execution decisions (resolving SPEC ↔ job-doc contradictions)

These three picks were made on 2026-05-08 after a forensic audit of the prior job doc state. They override the SPEC where the SPEC contradicts itself, and the prior Discoveries are discarded as fictional.

- **D1. Typed parameter fields, not names-only.** SPEC C2 says "names only" but the SPEC's own example output (lines 287-291) shows typed Go fields (`AllowedActions []string`, `RequireSubject bool`). We honor the example: `buildCaveatMap` returns `map[string][]ParamSpec` where `ParamSpec` carries `{Name string, GoType string}`, derived from `CaveatDefinition.ParameterTypes` (`map[string]*CaveatTypeReference`). Names-only would emit `any` fields and defeat the purpose of typed codegen.
- **D2. `Caveat` field is a pointer (`*CaveatArgs`).** The SPEC shows both forms; SPEC C7 allows either. Pointer cleanly maps "no caveat params for this call" → `nil`, lets the template emit `if input.Caveat != nil` without ambiguity, and matches the example block at line 297.
- **D3. `AdaptDefinitions(caveatDefs, defs)` signature change goes in.** The SPEC dictates the change; the prior job-doc Discovery #1 ("kept signature unchanged, added BuildCaveatMap exported") described code that was never merged. We follow the SPEC.

---

## Tasks

### Task 1: Extend `AllowedType` with `CaveatName`

- **File:** `internal/generator/adapter.go`
- **Action:**
    - Add `CaveatName string` field to `AllowedType`.
    - In `flattenAllowedTypes`, remove the `if ar.GetRequiredCaveat() != nil { return error }` branch.
    - Extract `caveatName := ""` (empty when no caveat); when `ar.GetRequiredCaveat() != nil`, set it to `ar.GetRequiredCaveat().GetCaveatName()`.
    - Pass it into `AllowedType{}` construction.
- **Verification:** `go build ./...` + `go vet ./...` pass. Round-trip stays byte-identical (no schema changes).
- **Workstream:** adapter
- **Depends on:** —

### Task 2: Thread `caveatDefs` through adapter + generator

- **Files:** `internal/generator/adapter.go`, `internal/generator/generator.go`
- **Action:**
    - Define `ParamSpec struct { Name, GoType string }`.
    - Add `buildCaveatMap(caveatDefs []*core.CaveatDefinition) map[string][]ParamSpec` (handles nil input).
    - Add a `caveatTypeToGo(*core.CaveatTypeReference) string` helper covering the SpiceDB CEL type set (`string`, `bool`, `int`, `uint`, `double`, `bytes`, `duration`, `timestamp`, `any`, `list<T>`, `map<K,V>`, `ipaddress`); fall back to `any` for unknown / unsupported with no error.
    - Change `AdaptDefinitions` signature: `func AdaptDefinitions(caveatDefs []*core.CaveatDefinition, defs []*core.NamespaceDefinition) ([]*DefinitionView, error)`. Build the `caveatMap` and stash it on a package-level (or returned) artefact reachable from the generator. Cleanest: change the signature again to also return `map[string][]ParamSpec`, OR store on the Generator at construction.
    - Add `CaveatMap map[string][]ParamSpec` field to `Generator`.
    - Change `NewGenerator(caveatMap, definitions)`.
- **Verification:** `go build ./...` passes. Field stored but template not yet using it.
- **Workstream:** adapter + generator
- **Depends on:** Task 1

### Task 3: Wire `compiled.CaveatDefinitions` in CLI

- **File:** `cmd/authzed-codegen/main.go`
- **Action:**
    - `defs, caveatMap, err := generator.AdaptDefinitions(compiled.CaveatDefinitions, compiled.ObjectDefinitions)` (final signature decided in Task 2).
    - `g := generator.NewGenerator(caveatMap, defs)`.
- **Verification:** `go build ./...` passes.
- **Workstream:** wiring
- **Depends on:** Task 2

### Task 4: Add `CheckPermissionWithCaveat` to `Engine` interface

- **File:** `pkg/authz/authz.go`
- **Action:**
    - Add to the `Engine` interface:
      ```go
      CheckPermissionWithCaveat(ctx context.Context, dest Resource, has Permission, subj Type, audIDs []ID, caveatParams map[string]any) error
      ```
- **Verification:** `go build ./...` will fail at `pkg/authz/spicedb/Engine` until Task 5 — expected.
- **Workstream:** interface
- **Depends on:** —

### Task 5: Implement `CheckPermissionWithCaveat` + `serializeCaveatMap`

- **File:** `pkg/authz/spicedb/crud.go`
- **Action:**
    - Add method on `*Engine` that mirrors `CheckPermission` but builds `Context` from `caveatParams`. When `caveatParams` is nil/empty, pass `Context: nil`.
    - Add `serializeCaveatMap(map[string]any) (*structpb.Struct, error)` using `structpb.NewStruct`. Empty map → `nil, nil`.
    - Import `google.golang.org/protobuf/types/structpb` (already present indirectly via authzed-go in `go.sum`).
- **Verification:** `go build ./...` passes (closes the Task 4 break). `go vet ./...` clean.
- **Workstream:** runtime
- **Depends on:** Task 4

### Task 6: Template caveat path

- **File:** `internal/templates/object.go.tmpl`
- **Action:**
    - Register a `caveatParams` template function in the generator's FuncMap that closes over `g.CaveatMap` and returns `[]ParamSpec` for a caveat name.
    - Register a `hasCaveatRelations` helper to short-circuit the no-caveat path.
    - Per-namespace, for each unique `CaveatName` referenced by any relation, emit a `<PascalCaveat>Args` struct (deduplicated within a namespace).
    - When a relation has at least one allowed type with a `CaveatName`, add a `Caveat *<PascalCaveat>Args` field to its `Check<Perm>Inputs`. (If the relation references multiple caveats, fail loudly at generation time — out of scope; document in Discoveries if it happens.)
    - Replace `CheckPermission` with `CheckPermissionWithCaveat` in the generated method body for caveat-bearing branches; build the `map[string]any` from the typed `Caveat` struct fields, using JSON-tag-style snake_case keys.
    - Every new branch is guarded on `hasCaveatRelations` so no-caveat schemas keep producing byte-identical output.
- **Verification:** `go build ./...` + `go vet ./...` pass. Round-trip clean.
- **Workstream:** template
- **Depends on:** Tasks 1-5

### Task 7: Round-trip verification

- **Action:** `go run ./cmd/authzed-codegen --output example/authzed example/schema.zed && git diff --quiet example/authzed/`.
- **Verification:** exits 0.
- **Depends on:** Task 6

### Task 8: Build + e2e verification

- **Action:** `go build ./...`, `go vet ./...`, `go test ./pkg/authz/spicedb/... ./example/authzed/...`. The e2e tests skip cleanly via `spicedbtest.ErrDockerUnavailable` when Docker is absent.
- **Verification:** all exit 0; tests skipped (Docker absent) is acceptable.
- **Depends on:** Task 7

---

## Discoveries & Decisions During Implementation

1. **Prior job-doc state was fictional (2026-05-08).** The previous version of this file marked Tasks 6/7/8 as ✅ done and listed six "Discoveries" describing code that was never merged. Forensic audit confirmed 0 of 8 tasks were actually implemented; `internal/generator/generator.go.bak` was a partial in-flight snapshot of Task 2 + helper funcs that was reverted. The doc was fully reset before this redo started.

2. **`AdaptDefinitions` returns the caveat map (not stored on a global).** SPEC line 69 specified the new signature but didn't say where the map travels. We picked `(defs, caveatMap, err)` returns + `NewGenerator(caveatMap, defs)` parameter. Cleanest: no globals, no extra exported function, the dependency between adapter + generator is explicit at the call site.

3. **Caveat-name collision detection at adapt time.** `AdaptDefinitions` errors out if a relation references a caveat that isn't in `compiled.CaveatDefinitions`. SpiceDB's compiler should already enforce this, but the codegen check is cheap insurance against partial schemas.

4. **`caveatTypeToGo` covers the typed subset only.** Clean Go analogues: `bool`, `string`, `int → int64`, `uint → uint64`, `double → float64`, `bytes → []byte`, `list<T>` (recursive). Everything else (`any`, `duration`, `timestamp`, `map<K,V>`, `ipaddress`, unknown) falls back to `any`. Generated code stays import-clean (no `time`, no `net`); caller is responsible for passing a `structpb`-compatible value at the call site. Refining duration/timestamp into `time.Duration` / `time.Time` is deferred — would require conditional `time` import in generated files plus marshaling logic in the generated `caveatCtx` builder.

5. **Single-caveat-per-permission cap, enforced at codegen.** `collectPermCaveats` walks each permission's expressions (Identifier resolves a relation or recurses to a sibling permission; Arrow contributes the LeftRel's caveats only). If the walk reaches more than one distinct caveat name, codegen errors with `"permission X reaches multiple caveats Y — multi-caveat per permission is out of scope (AUZ-006)"`. Generated `Check<Perm>Inputs` carries a single `Caveat *<Pascal>Args` field. Multi-caveat support requires either union-of-params structs or per-relation Check splits — both deferred.

6. **Arrow caveats: LeftRel only.** When a permission contains `parent->browse`, the LeftRel (`parent`) is on THIS object and its caveats apply at this Check call's wire boundary. Caveats on `browse` resolve through the parent object's permission tree and travel via SpiceDB's internal caveat evaluation, not via the immediate `Context` field. The walker reflects this — RightPerm caveats are not collected.

7. **Round-trip stays byte-identical because example/schema.zed has zero caveats.** All template caveat paths sit behind `{{ if permCaveat ... }}` / `{{ range definitionCaveats ... }}` guards that return empty/false for the example. The caveat code path is exercised only by codegen-time inspection of the helpers (build/vet) — there is no runtime fixture exercising it. A schema with caveats would need to be added under `example/` or a dedicated test corpus to lock the generated code shape; this is left for a follow-up job.

8. **`PascalCaveat` = `SnakeToPascal`.** No separate naming function — caveat names are snake_case identifiers (`can_view`) that pascal-case cleanly (`CanView`) into struct prefix `CanViewArgs`. The template func `pascalCaveat` is bound directly to `utilstr.SnakeToPascal`.

---

## Verification log

- `go build ./...` — clean
- `go vet ./...` — clean
- `go mod tidy` — `google.golang.org/protobuf v1.36.11` promoted to direct
- Codegen idempotent at new baseline (regen produces zero diff)
- `go test ./pkg/authz/spicedb/... ./example/authzed/bookingsvc/... ./example/authzed/menusvc/... ./example/authzed/extsvc/...` — all PASS
- Three new e2e tests added (`TestFolder_CheckTenantedBrowse_*`) exercising the live caveat code path against a SpiceDB container

## Discoveries from runtime testing

9. **Prefixed caveat names broke `pascalCaveat`.** The schema compiler runs with `RequirePrefixedObjectType()`, so caveat names also need a prefix (`extsvc/tenant_match`). The first attempt at `pascalCaveat` was bound directly to `SnakeToPascal`, which produced the invalid Go identifier `Extsvc/tenantMatchArgs`. Fixed by stripping everything before the last `/` in `pascalCaveat` before pascalising.

10. **`with caveat` writes are mandatory at the relationship level.** SpiceDB rejects writes to a `with caveat` relation that don't attach the caveat. The codegen's `Create<Rel>Relations` does not yet emit caveat attachment at write time — write-side caveat support is deferred. The new e2e tests bypass the codegen for the write half and use `authzed.Client.WriteRelationships` directly with `OptionalCaveat`.

11. **Engine snapshot consistency requires token sync after direct writes.** The engine pins reads to the last token it wrote itself (3s window). When tests bypass the engine for writes, the engine's stored snapshot predates the new tuple — reads via `CheckPermission*` see a stale view. Added `(*Engine).SetSnapshotToken(token)` so test helpers can advance the snapshot after a direct write. Initial test run failed only on the MatchTenant case because Wrong/NilCaveat deny-by-default masked the visibility issue.

12. **`CONDITIONAL_PERMISSIONSHIP` falls through to `ErrPermissionDenied`.** When a caveat-bearing tuple is checked without binding the params (e.g. nil `input.Caveat`), SpiceDB returns `PERMISSIONSHIP_CONDITIONAL_PERMISSION`. `errorIfDenied` only treats `HAS_PERMISSION` as success, so conditional grants surface as deny. This is conservative-by-default behavior; surfacing CONDITIONAL as a distinct signal (e.g. a third return value or sentinel error) is a future job. Documented in the `TestFolder_CheckTenantedBrowse_NilCaveat` test.

## Caveat fixture in example/schema.zed

```zed
caveat extsvc/tenant_match(tenant string) {
    tenant == "acme"
}

definition extsvc/folder {
    relation tenanted_viewer: extsvc/user with extsvc/tenant_match
    permission tenanted_browse = tenanted_viewer
}
```

Generates: `TenantMatchArgs{Tenant string}`, `CheckFolderTenantedBrowseInputs{User []User; Caveat *TenantMatchArgs}`, and a `CheckTenantedBrowse` method that builds `caveatCtx` from the typed input and calls `CheckPermissionWithCaveat`.
