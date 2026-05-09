# [SPEC-008] Lookup Conditional Surfacing

| Field      | Value                                                |
|------------|------------------------------------------------------|
| Status     | Accepted                                             |
| Created    | 2026-05-09                                           |
| Author     | Danh Tran                                            |
| Implements | (closes silent CONDITIONAL filter from AUZ-008/AUZ-012) |

---

## Overview

This SPEC closes the symmetric gap to v1.6's `Check<Perm>` rich signal: today `LookupResources` / `LookupSubjects` (and their `*WithCaveat` variants) silently filter `LookupPermissionship_LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION` from the returned slice — a caveat-reaching Lookup that would grant access if the caller supplied missing context returns "no resources found" instead of surfacing the recoverable subset. After this SPEC, all Lookup paths return a typed result struct partitioning definite grants from conditional grants; conditional entries carry `MissingKeys` directly from `PartialCaveatInfo.MissingRequiredContext` so callers can fetch context and retry. Variant-C philosophy from AUZ-010 SPEC-005: uniform replacement across all 4 Lookup paths, schema evolution invisible to API.

**What this component does:** Add `LookupResult` and `LookupConditionalEntry` runtime types in `pkg/authz/authz.go`. Change all 4 `Engine.Lookup*` method signatures to return `LookupResult` instead of `[]ID`. Update `*spicedb.Engine` implementations to populate `Conditional` from rows where `Permissionship == LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION`, reading `MissingKeys` from `PartialCaveatInfo.MissingRequiredContext` (per A1 — wire field present on conditional rows). Generate per-resource-type and per-subject-type result structs (`<Type>LookupResult` and `<Type>ConditionalLookupEntry`). Generated `Lookup<Perm><Type>Resources` / `Lookup<Perm><Type>Subjects` methods change return type from `([]<Type>, error)` to `(<Type>LookupResult, error)` uniformly across caveat and non-caveat permissions.

**What this component does not do:** Change Check paths — v1.6's typed-error path on Check is the symmetric solution and stays unchanged. Auto-decode `MissingKeys` to typed caveat arguments — caller knows which caveat from call context (per AUZ-010 SPEC-005 C1 precedent). Provide auto-retry helper — surfacing missing keys is the engine's job, deciding whether to fetch and retry is the caller's. Modify `HasPublicSubject` (which uses `LookupSubjects` internally) — the wildcard check semantic is "is `WildcardID` granted?" and conditional wildcards are a non-occurring case in practice (per A4). Apply variant-A or variant-B (parallel methods or selective emission) — the uniform replacement is the explicit design pick, mirroring AUZ-010.

---

## Interface Contracts

### Runtime types — `pkg/authz/authz.go`

```go
// LookupResult is the return value of every Engine.Lookup* method. Definite
// holds resource/subject IDs the caller has confirmed access to. Conditional
// holds entries that would be granted IF the caller supplies the named
// missing keys; treating these as confirmed is unsafe — callers fetch the
// missing context and retry the Check (or filter Conditional out entirely
// when only definite grants matter).
type LookupResult struct {
    Definite    []ID
    Conditional []LookupConditionalEntry
}

// LookupConditionalEntry surfaces SpiceDB's PartialCaveatInfo for a single
// conditional row. MissingKeys is the caveat parameter names from
// PartialCaveatInfo.MissingRequiredContext — directly off the wire.
type LookupConditionalEntry struct {
    ID          ID
    MissingKeys []string
}
```

### Engine interface — `pkg/authz/authz.go`

All 4 Lookup signatures change from `([]ID, error)` to `(LookupResult, error)`:

```go
type Engine interface {
    // ... other methods unchanged ...
    LookupResources(ctx context.Context, from Type, match Permission, subject Type, byIDs []ID) (LookupResult, error)
    LookupResourcesWithCaveat(ctx context.Context, from Type, match Permission, subject Type, byIDs []ID, caveatParams map[string]any) (LookupResult, error)
    LookupSubjects(ctx context.Context, on Resource, permission Permission, subject Type) (LookupResult, error)
    LookupSubjectsWithCaveat(ctx context.Context, on Resource, permission Permission, subject Type, caveatParams map[string]any) (LookupResult, error)
}
```

`HasPublicSubject` (signature `(bool, error)`) is unchanged; its body adapts to consume the new `LookupResult` return shape internally.

### `*spicedb.Engine` implementation — `pkg/authz/spicedb/crud.go`

`LookupResourcesWithCaveat` shape:

```go
func (e *Engine) LookupResourcesWithCaveat(...) (authz.LookupResult, error) {
    // ... existing setup unchanged ...

    result := authz.LookupResult{
        Definite:    []authz.ID{},
        Conditional: []authz.LookupConditionalEntry{},
    }
    for _, id := range byIDs {
        res, err := e.client.LookupResources(...)
        if err != nil {
            return authz.LookupResult{}, err
        }

        data, err := res.Recv()
        for ; err == nil && data != nil; data, err = res.Recv() {
            switch data.Permissionship {
            case v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION:
                result.Definite = append(result.Definite, authz.ID(data.ResourceObjectId))

            case v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION:
                var missing []string
                if pci := data.PartialCaveatInfo; pci != nil {
                    missing = pci.MissingRequiredContext
                }
                result.Conditional = append(result.Conditional, authz.LookupConditionalEntry{
                    ID:          authz.ID(data.ResourceObjectId),
                    MissingKeys: missing,
                })
            }
            // LOOKUP_PERMISSIONSHIP_UNSPECIFIED is dropped (per A2)
        }
        if !errors.Is(err, io.EOF) {
            return authz.LookupResult{}, err
        }
    }
    return result, nil
}
```

`LookupSubjectsWithCaveat` follows the same pattern but reads from `data.Subject` — `Subject.SubjectObjectId`, `Subject.Permissionship`, `Subject.PartialCaveatInfo` (the top-level `data.Permissionship` field is deprecated per the proto, per A3).

`LookupResources` and `LookupSubjects` (non-caveat variants) keep their thin pass-through delegating to the `*WithCaveat` form with `caveatParams=nil`.

`HasPublicSubject` body update — same `(bool, error)` external signature:

```go
func (e *Engine) HasPublicSubject(...) (bool, error) {
    result, err := e.LookupSubjects(...)
    if err != nil {
        return false, err
    }
    for _, id := range result.Definite {
        if id == authz.WildcardID {
            return true, nil
        }
    }
    return false, nil
}
```

Conditional wildcards (a wildcard `WildcardID` granted with a caveat) are not surfaced by this method — per A4, the wildcard check is "is wildcard definitely granted?" and conditional wildcards in practice are extremely rare (a wildcard tuple with a caveat would resolve at runtime via the `LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION` branch into `result.Conditional`, NOT through `result.Definite`). Callers who need that signal use the regular `Lookup<Perm><Type>WildcardSubjects` path which goes through `HasPublicSubject` — same as v1.6 behavior, no breaking change.

### Generated typed result struct — codegen template

Per resource type (results of `Lookup*Resources`) AND per subject type (results of `Lookup*Subjects`):

```go
// One struct per object type whose codegen output emits a Lookup method:
type FolderLookupResult struct {
    Definite    []Folder
    Conditional []FolderConditionalLookupEntry
}

type FolderConditionalLookupEntry struct {
    ID          Folder
    MissingKeys []string
}
```

The struct lives in the same package as the typed `Folder` ID alias (i.e., `extsvc/folder.gen.go`). `<Type>LookupResult` is shared across every `Lookup*` method returning that type — multiple permissions on the same definition produce one struct, not one per permission.

### Generated `Lookup<Perm><Type>Resources` / `Lookup<Perm><Type>Subjects` — return type change

```go
func LookupTenantedBrowseFolderResources(
    ctx context.Context, input CheckFolderTenantedBrowseInputs,
) (FolderLookupResult, error) {
    var caveatCtx map[string]any
    if c := input.Caveats.TenantMatch; c != nil {
        caveatCtx = map[string]any{"tenant": *c.Tenant}
    }

    if len(input.User) > 0 {
        result, err := authz.GetEngine(ctx).LookupResourcesWithCaveat(ctx,
            TypeFolder, authz.Permission(FolderTenantedBrowse),
            TypeUser, authz.IDs(input.User), caveatCtx,
        )
        if err != nil {
            return FolderLookupResult{}, err
        }

        // Cast untyped LookupResult to typed.
        out := FolderLookupResult{
            Definite:    authz.FromIDs[Folder](result.Definite),
            Conditional: make([]FolderConditionalLookupEntry, 0, len(result.Conditional)),
        }
        for _, c := range result.Conditional {
            out.Conditional = append(out.Conditional, FolderConditionalLookupEntry{
                ID:          Folder(c.ID),
                MissingKeys: c.MissingKeys,
            })
        }
        return out, nil
    }
    return FolderLookupResult{}, nil
}
```

`LookupSubjects` mirrors the shape with `subject` type instead of `resource` type. Wildcard subject methods (`Lookup<Perm><Type>WildcardSubjects` returning `(bool, error)`) are unchanged — they still wrap `HasPublicSubject` per AUZ-008 ADR-003.

### Fixture migration — `example/authzed/**/*_test.go`

Every existing Lookup call site updates from `ids, err := ...` to `result, err := ...; ids := result.Definite`. Mechanical sweep — same pattern as AUZ-010's `authz.IDsOf` migration. Test count: ~12 sites across `extsvc_test.go`, `bookingsvc_test.go`, `menusvc_test.go`. New tests verify the conditional path surfaces `MissingKeys` for caveat-reaching Lookups.

---

## Sequence

Wire flow when Lookup reaches a caveat with missing context:

```
caller code:

    result, err := folder.LookupTenantedBrowseFolderResources(ctx,
        extsvc.CheckFolderTenantedBrowseInputs{
            User: []extsvc.User{user},
            // Caveats omitted — no tenant supplied
        },
    )
         │
         ▼
generated method body:
    ├─► caveatCtx = nil  (no caveat fields populated)
    │
    └─► engine.LookupResourcesWithCaveat(ctx, ..., nil)

         │
         ▼
*spicedb.Engine.LookupResourcesWithCaveat:

    ├─► client.LookupResources(...) → stream
    │
    ├─► stream loop, per response:
    │     switch data.Permissionship:
    │       HAS_PERMISSION:
    │         result.Definite = append(..., data.ResourceObjectId)
    │       CONDITIONAL_PERMISSION:
    │         result.Conditional = append(..., {
    │           ID:          data.ResourceObjectId,
    │           MissingKeys: data.PartialCaveatInfo.MissingRequiredContext,
    │         })
    │       UNSPECIFIED:
    │         (drop — per A2)
    │
    └─► return LookupResult{Definite, Conditional}

         │
         ▼
generated method casts to typed:

    out := FolderLookupResult{
        Definite:    FromIDs[Folder](result.Definite),
        Conditional: [per-entry cast to FolderConditionalLookupEntry],
    }
    return out, nil

         │
         ▼
caller branches on result fields:

    if len(result.Definite) > 0  { /* confirmed grants */ }
    if len(result.Conditional) > 0 {
      for _, c := range result.Conditional {
        // c.ID, c.MissingKeys — fetch and retry per-entry
      }
    }
```

Backward-compat is NOT preserved at the type level (the return type changes); migration is a mechanical `result, err := ...; ids := result.Definite` sweep at every existing call site. Per the active-development consumer profile (only this repo), the breaking change cost is bounded.

---

## Errors

| Error class | Trigger | Layer |
|---|---|---|
| `LookupResult{}, gRPC error` | Transport failure or `client.LookupResources/Subjects` initial call fails | Engine — passed through unwrapped |
| `LookupResult{}, stream error` | Mid-stream gRPC error (non-EOF) | Engine — caller observes empty result + non-nil err; partial entries before the error are not surfaced (matching pre-SPEC behavior) |
| `LookupResult{}, "serialize caveat params: <wrapped>"` | `serializeCaveatMap` fails on protobuf-incompatible value | Engine — pre-stream |
| Empty result (no error) | No matching resources / subjects | Engine — `LookupResult{Definite:[], Conditional:[]}, nil` |

The result is returned as `LookupResult{}` (zero-value, both slices nil) when err is non-nil. Per A5 — caller should not inspect the result fields when err != nil.

---

## Constraints

- **C1.** `LookupResult` returned by zero value is `{Definite: nil, Conditional: nil}`. The `*spicedb.Engine` impl explicitly initialises both slices to empty (`[]ID{}` and `[]LookupConditionalEntry{}`) before the stream loop — callers can range over either field unconditionally without nil-checks.

- **C2.** `Conditional` entries are NOT included in `Definite`. A caller reading `result.Definite` observes only confirmed grants; using `result.Conditional` requires explicit access. Safe-by-default (per the design rationale).

- **C3.** `MissingKeys` may be empty on a `Conditional` entry. Per A1/A2 — SpiceDB sometimes returns CONDITIONAL without populated `MissingRequiredContext` (e.g. CEL expression returned indeterminate for an ambiguous expression). Callers should not assume non-empty.

- **C4.** All 4 Lookup paths (`Resources`, `Subjects`, `*WithCaveat`) get the new return shape uniformly. Non-caveat permissions always produce empty `Conditional` slice (caveats can't reach them; conditional is impossible). Per the variant-C design pick — schema evolution invisible.

- **C5.** Generated `<Type>LookupResult` is per-resource-type or per-subject-type, NOT per-permission. Multiple permissions returning the same type share the struct.

- **C6.** Generated `<Type>ConditionalLookupEntry` field order is positional-stable: `{ID, MissingKeys}`. Future protocol additions append; existing keyed-struct callers stay compiling.

- **C7.** `HasPublicSubject` external signature `(bool, error)` is preserved. Body updates to consume `LookupResult` internally and check `Definite` for the wildcard sentinel — caller observes no behavior change for the common case (definite wildcard grant). Per A4 — conditional wildcards are not surfaced by this method.

- **C8.** Wildcard-subject methods (`Lookup<Perm><Type>WildcardSubjects`) are unchanged. They wrap `HasPublicSubject` and return `(bool, error)` per AUZ-008 ADR-003. Conditional wildcards are not exposed via this method shape — callers wanting to detect them use the regular `Lookup<Perm><Type>Subjects` path and inspect the `Conditional` slice for entries with `ID == WildcardID`.

- **C9.** Round-trip idempotency stable at the new baseline. Every existing `.gen.go` regenerates with the new return type; subsequent re-runs produce zero diff. Per the AUZ-010 migration pattern — one round-trip baseline shift, then stable.

- **C10.** Generated `Lookup*` methods use `authz.FromIDs[<Type>]` (NOT `FromIDsExcludingWildcard`) when projecting the engine's `[]ID` result onto the typed `Definite` slice. SpiceDB's wildcard handling for Lookup is determined by the schema; if a wildcard tuple grants the permission, the wildcard ID would appear in the engine's `Definite` slice and should be propagated. This matches the AUZ-008 behavior.

---

## Assumptions

- **A1 [EXTERNAL FACT]:** SpiceDB's `LookupResourcesResponse.Permissionship` and `LookupSubjectsResponse.Subject.Permissionship` carry `LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION` when the row's caveat reaches an indeterminate evaluation. The accompanying `PartialCaveatInfo.MissingRequiredContext` field lists the parameter names the CEL evaluator couldn't bind. Evidence: `go doc github.com/authzed/authzed-go/proto/authzed/api/v1 LookupResourcesResponse` and `LookupSubjectsResponse`; mirrors the `CheckPermissionResponse` shape from AUZ-012 SPEC-007 A2.

- **A2 [EXTERNAL FACT]:** `LookupPermissionship_UNSPECIFIED` is a wire-level placeholder for protocol-version mismatches; it's not produced by current SpiceDB versions for valid Lookup queries. The `*spicedb.Engine` impl drops UNSPECIFIED rows silently — they aren't valid grants of either category. Evidence: SpiceDB proto file documentation.

- **A3 [VERIFIED]:** `LookupSubjectsResponse.Permissionship` (top-level) is deprecated in favor of `Subject.Permissionship`. The current implementation already reads from `data.Subject.Permissionship` (per `pkg/authz/spicedb/crud.go:542`); SPEC-008 preserves this. Evidence: existing code; proto doc string.

- **A4 [HYPOTHESIS]:** Conditional-permission wildcards (a wildcard tuple with a caveat that reaches Lookup as `WildcardID + CONDITIONAL`) are extremely rare in practice. AUZ-009.1 verified `extsvc/user:* with extsvc/tenant_match` schemas write/check correctly when caveat is satisfied; the conditional-wildcard case (caveat parameter unbound at Lookup time) hasn't surfaced in any fixture. `HasPublicSubject` continues to check only `result.Definite` for `WildcardID`. Verification deferred — if a real schema needs conditional-wildcard discovery, a future SPEC adds `HasPublicSubjectConditional`.

- **A5 [VERIFIED]:** Engine returns zero-value `LookupResult{}` on error consistent with the existing pattern of returning `nil` slices on Lookup errors (per `pkg/authz/spicedb/crud.go:485,501,548` — the existing impl returns `nil` slices when err is non-nil). Callers must not inspect the result fields when err != nil. SPEC-008 preserves this contract.

- **A6 [VERIFIED]:** Migration of test call sites is mechanical and bounded. AUZ-010 migrated ~18 read sites with the same pattern; current Lookup call sites are ~12 across `bookingsvc_test.go`, `extsvc_test.go`, `menusvc_test.go`. Evidence: `grep -nE 'Lookup.*(Resources|Subjects)\(ctx' example/authzed/**/*_test.go` shows the migration scope. Internal-only consumer profile per AUZ-010 SPEC-005 C8.

- **A7 [VERIFIED]:** `HasPublicSubject`'s `(bool, error)` signature is preserved across SPEC-008. The change is body-only — internal switch from `slices.Contains(ids, WildcardID)` to `for _, id := range result.Definite`. AUZ-010's `HasPublicRelation` underwent the identical body-rewrite; this SPEC follows the same pattern. Evidence: `pkg/authz/spicedb/crud.go:559` post-AUZ-010.

---

## Unresolved Questions

(none)

---

## Summary

Net change scope:

| File | Change |
|---|---|
| `pkg/authz/authz.go` | Add `LookupResult` struct (`Definite []ID`, `Conditional []LookupConditionalEntry`). Add `LookupConditionalEntry` struct (`ID`, `MissingKeys []string`). Change `Engine.LookupResources` / `LookupResourcesWithCaveat` / `LookupSubjects` / `LookupSubjectsWithCaveat` return types from `([]ID, error)` to `(LookupResult, error)`. |
| `pkg/authz/spicedb/crud.go` | Update `LookupResourcesWithCaveat` and `LookupSubjectsWithCaveat` to switch on `Permissionship` and populate `Definite` / `Conditional` from rows + `PartialCaveatInfo.MissingRequiredContext`. Update `HasPublicSubject` body to read `result.Definite` instead of slice. Pass-through wrappers (`LookupResources` / `LookupSubjects`) trivially adapt. |
| `internal/templates/object.go.tmpl` | Emit `<Type>LookupResult` and `<Type>ConditionalLookupEntry` once per resource/subject type. Update generated `Lookup<Perm><Type>Resources` / `Lookup<Perm><Type>Subjects` bodies to project the typed result. Wildcard-subject methods unchanged. |
| `example/authzed/**/*.gen.go` | Regenerated output — every relation's Lookup methods change return signatures. |
| `example/authzed/**/*_test.go` | Migrate call sites: `ids, err := X.Lookup...` → `result, err := X.Lookup...; ids := result.Definite`. ~12 sites across 3 test files. New tests verify conditional surfacing for caveat-reaching Lookups. |
| `internal/generator/adapter.go` / `generator.go` | No changes — adapter / tree-walker infrastructure already provides the data needed; only the template emits new shapes. |

E2E tests cover: definite path (regression check, no behavior change at the value level — only at the access pattern); conditional Lookup of `tenanted_browse` without supplied tenant → `result.Conditional` contains the resource ID with `MissingKeys = ["tenant"]`; mismatched tenant (CEL false) → `result.Definite` is empty AND `result.Conditional` is empty (hard deny — not conditional); migration sweep verifies all existing tests pass after the call-site update.

---

## History

(History is owned by `harness history-update` — do not hand-edit.)
