# Changelog

All notable changes to this project are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.7.0] - 2026-05-09

Closes the symmetric gap to v1.6's Check rich-signal: `LookupResources` / `LookupSubjects` (and their `*WithCaveat` variants) now return a typed `LookupResult` partitioning definite grants from conditional grants. Conditional entries carry `MissingKeys` from `PartialCaveatInfo.MissingRequiredContext` so callers can fetch missing context and retry — no more silent "no resources found" when the actual answer is "found conditional, supply context to see them."

After v1.7, Check and Lookup paths give consistent semantics for caveat-reaching schemas: both surface the recoverable-conditional case distinctly from definite grants and from hard denies. Variant-C philosophy from AUZ-010 SPEC-005: uniform replacement across all 4 Lookup paths, schema evolution invisible.

### Added

- **`authz.LookupResult`** — engine-surface return type for all `Lookup*` methods. `Definite []ID` and `Conditional []LookupConditionalEntry`. Both slices initialised to empty (not nil) — callers range over either field unconditionally.
- **`authz.LookupConditionalEntry`** — runtime conditional row with `ID` and `MissingKeys []string`.
- **Generated `<Type>LookupResult`** — typed counterpart per resource/subject type. `Definite []<Type>` + `Conditional []<Type>ConditionalLookupEntry`. Shared across every Lookup method returning that type (per-resource-type, NOT per-permission).
- **Generated `<Type>ConditionalLookupEntry`** — typed conditional row.
- **5 new e2e tests** covering: conditional surfacing on Subjects path with `MissingKeys` populated, conditional surfacing on Resources path, hard-deny path (CEL false → both slices empty, NOT conditional), mixed definite/conditional in a single Lookup, regression check on existing AUZ-008 conditional-filter behavior (now via `.Definite`).

### Changed

- **BREAKING (Engine interface)**: `Engine.LookupResources` / `LookupResourcesWithCaveat` / `LookupSubjects` / `LookupSubjectsWithCaveat` return types change from `([]ID, error)` to `(LookupResult, error)`. External `Engine` implementers must update.
- **BREAKING (Generated code)**: every generated `Lookup<Perm><Type>Resources` / `Lookup<Perm><Type>Subjects` return type changes from `([]<Type>, error)` to `(<Type>LookupResult, error)`.
- `*spicedb.Engine.HasPublicSubject` body rewritten to scan `result.Definite` for `WildcardID`. External `(bool, error)` signature preserved.
- `*spicedb.Engine.HasPublicRelation` similarly preserved.

### Migration recipe

For tests/callers that consumed `[]<Type>` from Lookup methods:

```go
// Before:
ids, err := folder.LookupBrowseUserSubjects(ctx)
assert.Contains(t, ids, extsvc.User("u1"))

// After:
ids, err := folder.LookupBrowseUserSubjects(ctx)
assert.Contains(t, ids.Definite, extsvc.User("u1"))

// Caveat-aware caller — recover from conditional Lookup:
result, err := folder.LookupTenantedBrowseUserSubjects(ctx, caveats)
for _, c := range result.Conditional {
    fetched := fetch(c.MissingKeys)
    // retry Check or Lookup with fetched context
}
```

### Verified

- All 4 e2e packages pass.
- Codegen idempotent at the new baseline.
- `go build ./...` + `go vet ./...` clean.

### Deferred

- **Conditional wildcards** — `HasPublicSubject` and the wildcard subject methods (`Lookup<Perm><Type>WildcardSubjects`) check only `result.Definite` for `WildcardID`. A wildcard tuple with a caveat that resolves CONDITIONAL at Lookup would land in `result.Conditional`, NOT trigger the wildcard helper. Per SPEC-008 A4 — this case is extremely rare in practice; if a real schema needs it, a future SPEC adds `HasPublicSubjectConditional` or similar.
- **Auto-retry helper for Lookup** — same disposition as v1.6's Check path. Surfacing `MissingKeys` is the engine's job; deciding whether to fetch and retry is the caller's.

## [1.6.0] - 2026-05-09

Surfaces SpiceDB's `CONDITIONAL_PERMISSION` signal as a typed error path. Recoverable failures (caller forgot to supply caveat context) are now distinguishable from hard denies (user genuinely lacks permission) via `errors.Is(err, ErrConditionalPermission)` and `errors.As(err, &cpe)` — `cpe.MissingKeys` carries the caveat parameter names from `PartialCaveatInfo.MissingRequiredContext` so callers can fetch and retry. Backward compat preserved: existing `errors.Is(err, ErrPermissionDenied)` checks still match all deny cases via the typed error's custom `Is` method.

This was documented as deferred work in CHANGELOG entries from v1.1.0 through v1.4.0; SPEC-007 closes the gap with zero codegen template change.

### Added

- **`authz.ErrConditionalPermission`** — sentinel error for `errors.Is` matching the rich-signal path.
- **`authz.ConditionalPermissionError`** — typed struct carrying `MissingKeys []string` (from `PartialCaveatInfo.MissingRequiredContext`). Implements custom `Is(target error) bool` matching BOTH `ErrConditionalPermission` AND `ErrPermissionDenied`.
- **4 new e2e tests** covering: granted path (regression check, no behavior change); conditional path (assert `errors.Is(_, ErrConditionalPermission)` + `errors.As` extracts `MissingKeys = ["tenant"]`); backward-compat (conditional also matches `ErrPermissionDenied`); hard-deny path (CEL false → NOT conditional, plain `ErrPermissionDenied`).

### Changed

- **`*spicedb.Engine.errorIfDenied`** — switch on `Permissionship` covering HAS_PERMISSION (nil), CONDITIONAL_PERMISSION (typed pointer error), default (`ErrPermissionDenied`). Single point of error construction; propagates rich signal to every Check method (`CheckPermission`, `CheckPermissionWithCaveat`, `CheckPermissionUserset`).
- Generated `Check<Perm>` method bodies are unchanged — the richer error flows through the existing `(bool, error)` return shape. No template diff, no regenerated `.gen.go` files. Round-trip stable against v1.5.0 baseline.

### Caller migration (rich-signal opt-in)

```go
err := folder.CheckTenantedBrowse(ctx, input)
switch {
case err == nil:
    // granted
case errors.Is(err, authz.ErrConditionalPermission):
    var cpe *authz.ConditionalPermissionError
    errors.As(err, &cpe)
    // cpe.MissingKeys lists the caveat keys to fetch and retry with
case errors.Is(err, authz.ErrPermissionDenied):
    // hard deny — user genuinely lacks permission
}
```

Existing v1.5 callers checking only `errors.Is(err, ErrPermissionDenied)` see no behavior change.

### Verified

- All 4 e2e packages pass.
- Codegen idempotent at the new baseline (zero diff vs v1.5.0).
- `go build ./...` + `go vet ./...` clean.

### Deferred

- **Lookup paths surfacing CONDITIONAL** — `LookupResources` / `LookupSubjects` / their `WithCaveat` variants continue to silently filter `Permissionship != HAS_PERMISSION` per AUZ-008. Surfacing the conditional-but-recoverable subset would change the typed return shape (e.g. `[]ID + []ConditionalEntry{ID, MissingKeys}`); deferred until concrete demand.
- **Auto-retry helper** — the SPEC surfaces missing keys; deciding whether to fetch and retry is the caller's concern. A future `CheckPermissionWithFetcher(ctx, ..., fetcher func([]string) map[string]any)` could wrap the pattern but is out of scope here.

## [1.5.0] - 2026-05-09

Closes the last big rejected schema construct from ADR-001 — sub-relation references (`relation member: team#admin`). After this release, the codegen accepts every commonly-used SpiceDB schema feature: caveats, expiration, intersection, exclusion, wildcards, read-side metadata, and now usersets. Schema constructs of the form `T#R` are captured into a new `AllowedType.SubRelation` field, written via `Subject.OptionalRelation` on the wire, and surfaced through both write fields (`<TypeName><PascalSubRel>`) on `<Rel>Objects` and Check-input fields on `Check<Perm>Inputs`.

### Added

- **`Engine.CreateRelationsToUserset`** — single new write method covering all four userset combinations (plain / +caveat / +expiration / +both) via sentinel parameters. Always issues `OPERATION_TOUCH` (per SPEC-006 C2/A3 — same expired-collision rationale as AUZ-009).
- **`Engine.CheckPermissionUserset`** — new Check method for the rare userset-as-subject case ("does t1#admin have view?"). SpiceDB matches the literal userset reference; no recursive expansion (per SPEC-006 A2).
- **`AllowedType.SubRelation string`** — adapter-level field captured from `AllowedRelation.Relation`. Empty for direct subjects, non-empty for userset references. Drives codegen routing.
- **`RelationTuple.SubRelation string`** — populated from `Relationship.Subject.OptionalRelation` on read.
- **Generated `<Rel><Type>Relation.SubRelation`** — read-side field surfacing the sub-relation tag for mixed direct + userset relations.
- **Generated userset write fields** — `<Rel>Objects.<TypeName><PascalSubRel> []<TypeName>` per userset allowed type. Caller writes `TeamAdmin: []Team{"t1"}` to grant team t1's admin set.
- **Generated userset Check input fields** — `Check<Perm>Inputs.<TypeName><PascalSubRel> []<TypeName>` for permissions reaching userset allowed types. Routes through `CheckPermissionUserset`.
- **3-key disambiguation** — `(Namespace, IsWildcard, SubRelation)` extends the existing caveat-disambiguation logic. Schemas declaring `team#admin | team#owner` produce distinct `TeamAdmin` / `TeamOwner` field names.
- **Schema fixture: `extsvc/team`** — new definition with `owner` / `manager` relations and `admin` permission. Four new userset relations on `extsvc/folder`: `collab` (plain), `mixed_view` (mixed direct + userset), `gated_collab` (userset + caveat), `temp_collab` (userset + expiration).
- **7 new e2e tests** covering wire-level write/read, literal userset Check, mismatched team Check, mixed direct + userset Read disjoint subsets, userset + caveat, userset + expiration metadata round-trip, regression check on direct-subject SubRelation emptiness.
- **5 new adapter unit tests** in `adapter_test.go` covering plain userset, mixed direct + userset, two usersets same namespace different sub-relations, direct + userset same namespace, userset with distinct caveats.

### Changed

- The Engine interface gained two new methods (`CreateRelationsToUserset`, `CheckPermissionUserset`). External implementers must add them. The only impl in this repo is `*spicedb.Engine`.
- `AllowedType` struct gains the `SubRelation string` field. Generated metadata structs (`<Rel><Type>Relation`) gain `SubRelation string` field — positional-stable per AUZ-010 SPEC-005 C6.
- `*spicedb.Engine.ReadRelations` populates `RelationTuple.SubRelation` from `rel.Subject.OptionalRelation`. No change to the response shape (already a slice of `RelationTuple`).
- `relationFromView` filters out userset allowed types from the direct-subject permission tree — userset references are exposed via the new `permissionInputUsersets` helper instead.

### Verified

- All 4 e2e packages pass.
- Codegen idempotent at the new baseline.
- `go build ./...` + `go vet ./...` clean.

### Deferred

- **Lookup with userset results** (per SPEC-006 C9). `LookupSubjects` still returns `[]<Type>` of direct subject IDs only. Returning userset triples would change the typed return shape and is a heavier scope; deferred until concrete demand.
- **Lookup with userset inputs**. `LookupResources` accepts direct subjects only; userset-as-input on Lookup is uncommon and follows the same return-shape question as the previous bullet.
- **Userset expiration deny-after-expiry under AtExactSnapshot consistency** — the engine's snapshot-pinned consistency mode evaluates userset-as-subject Check at the snapshot revision, so expiration filtering doesn't trigger on the wall-clock comparison. Direct-subject expiration filtering (AUZ-009) is unaffected because chain walking handles it differently. Documented in AUZ-011 Discoveries as a consistency-mode constraint.

## [1.4.0] - 2026-05-09

Closes the read-side metadata gap left by AUZ-006/007/009 (caveat name, caveat context, and expiration timestamp travel on the wire but were silently dropped by `Read<Rel><Type>Relations`). Replaces the read return type uniformly: every relation now returns `[]<Rel><Type>Relation` carrying ID + metadata. Schemas adopting `with caveat` or `with expiration` later don't change method names — only what populates in the existing struct's nil-able fields.

This is a breaking API change on `Engine.ReadRelations` and on every generated `Read<Rel><Type>Relations` and `Read<Rel><Type>Wildcard` method. The only consumer is this repo so we're staying on minor per active-development convention; external adopters (if any appear) follow the migration recipe below.

### Added

- **`authz.RelationTuple`** — engine-surface type carrying `ID + CaveatName + CaveatContext + ExpiresAt`. Returned by `Engine.ReadRelations`.
- **Generated `<Rel><Type>Relation` struct** — typed counterpart per `(relation, allowed-type)` pair. Same fields as `RelationTuple` but `ID` is the typed subject (`User`, `Group`, …). Implements `RelationID() T` so generic helpers can project IDs without per-type boilerplate.
- **`authz.IDsOf[T,R](rels) []T`** — generic ID projector. Caller writes `authz.IDsOf(rels)`; type inference resolves `T` and `R` from the single positional argument. Used by tests and any caller that wants the pre-AUZ-010 simple-IDs shape.
- **`authz.IDsOfExcludingWildcard[T,R](rels) []R`** — symmetric to the existing `FromIDsExcludingWildcard`; drops tuples where `RelationID() == WildcardID`. Generated `Read<Rel><Type>Relations` filters wildcards before returning.
- **6 new e2e tests** covering: non-traited tuple → all metadata fields nil/empty; caveated tuple → `CaveatName + CaveatContext` populated; expiring tuple → `ExpiresAt` populated within ±2s; combined caveat+expiration → both populate; wildcard via `Read<Rel><Type>Wildcard` → metadata struct alongside the bool; `IDsOf` round-trip equivalence with the pre-AUZ-010 API.

### Changed

- **BREAKING**: `Engine.ReadRelations` return type from `([]ID, error)` to `([]RelationTuple, error)`. External `Engine` implementers must update.
- **BREAKING**: every generated `Read<Rel><Type>Relations(ctx) ([]<Type>, error)` becomes `(ctx) ([]<Rel><Type>Relation, error)`.
- **BREAKING**: every generated `Read<Rel><Type>Wildcard(ctx) (bool, error)` becomes `(ctx) (<Rel><Type>Relation, bool, error)` — the wildcard tuple's metadata surfaces alongside the presence bool.
- `*spicedb.Engine.ReadRelations` populates caveat and expiration fields from `Relationship.OptionalCaveat` and `Relationship.OptionalExpiresAt` (via `*structpb.Struct.AsMap()` and `*timestamppb.Timestamp.AsTime()`).
- `*spicedb.Engine.HasPublicRelation` body rewritten to scan tuples for `ID == WildcardID` instead of `slices.Contains(ids, WildcardID)`. Public signature unchanged.
- Generated `.gen.go` files now always import `"time"` because every metadata struct references `*time.Time`.

### Migration recipe

For tests/callers that consumed `[]<Type>` from `Read<Rel><Type>Relations`:

```go
// Before:
users, err := folder.ReadViewerUserRelations(ctx)  // []User

// After (when only IDs are needed):
rels, err := folder.ReadViewerUserRelations(ctx)   // []FolderViewerUserRelation
users := authz.IDsOf(rels)                         // []User

// After (when metadata matters):
rels, err := folder.ReadViewerUserRelations(ctx)
for _, r := range rels {
    if r.CaveatName != "" {
        // surface r.CaveatName, r.CaveatContext to UI
    }
    if r.ExpiresAt != nil {
        // show "expires at <t>" badge
    }
}
```

For wildcard call sites:

```go
// Before:
isWildcard, err := folder.ReadGuestUserWildcard(ctx)

// After (when only the bool matters):
_, isWildcard, err := folder.ReadGuestUserWildcard(ctx)

// After (when wildcard's caveat/expiry matter):
meta, isWildcard, err := folder.ReadGuestUserWildcard(ctx)
if isWildcard && meta.ExpiresAt != nil {
    // public-until-timestamp pattern
}
```

### Verified

- All 4 e2e packages pass (`pkg/authz/spicedb`, `example/authzed/{bookingsvc,extsvc,menusvc}`).
- Codegen idempotent at the new baseline.
- `go build ./...` + `go vet ./...` clean.

### Deferred

- Iterator API for `ReadRelations`. Currently `[]RelationTuple` materializes the full result; SpiceDB's `ReadRelationships` is server-streamed. Per SPEC-005 A4 — no schema in this codebase has hit memory pressure; revisit if proven wrong.
- Auto-decoding `CaveatContext` to typed `<Caveat>Args` structs. Caller decodes based on `CaveatName` (one switch per consumer); auto-decoding would force enumeration of all caveats reachable per allowed type, multiplying the generated surface.

## [1.3.0] - 2026-05-09

Adds `with expiration` support — schemas can now declare per-tuple TTL via SpiceDB's expiration trait, and combined `with <caveat> and expiration` works end-to-end. SpiceDB filters expired tuples server-side from Check / Lookup / Read so the client side requires no awareness of expiry beyond the write call.

### Added

- **`Engine.CreateRelationsWithExpiration`** — single new interface method covering both expiration-only and caveat-plus-expiration writes. `caveatName == ""` and `caveatParams == nil` mean "expiration only"; non-empty values opt into the combined path. Hard-codes `OPERATION_TOUCH` because un-garbage-collected expired tuples may collide on tuple identity (per SpiceDB docs).
- Generated `<Rel>Objects` gains an `Expirations <RelName>Expirations` sub-struct mirroring `Wildcards` and `Caveats`, with one `<IDFieldName> *time.Time` field per expiring allowed type.
- Generated `Create<Rel>Relations` per-allowed-type 4-way routing: `(no-trait)` → `CreateRelations`; `(caveat)` → `CreateRelationsWithCaveat`; `(expiration)` → `CreateRelationsWithExpiration("", nil, expiresAt)`; `(caveat+expiration)` → `CreateRelationsWithExpiration(name, params, expiresAt)`. Auto-switch to TOUCH happens transparently for expiring branches.
- `AllowedType.IsExpiring bool` — adapter accepts `with expiration` (previously rejected at adapt time per ADR-001 list).
- `anyExpiring` and `anyExpiringInRels` template helpers — gate `Expirations` sub-struct emission and conditional `time` import.
- Schema fixtures: `relation expiring_viewer: extsvc/user with expiration` (pure expiration) and `relation gated_token: extsvc/user with extsvc/tenant_match and expiration` (combined). Plus the `use expiration` directive at the top of `example/schema.zed`.
- 5 new e2e tests against live SpiceDB: grants-before-expiry, denies-after-expiry (with `time.Sleep` past TTL), gated-token grants when both gates pass, gated-token denies on caveat fail (deferred at write so check-time tenant value reaches eval), TOUCH-allows-rewrite-after-expiry.

### Changed

- The Engine interface gained one new method (`CreateRelationsWithExpiration`). External implementers must add it. The only impl in this repo is `*spicedb.Engine`.
- Template adds a conditional `"time"` import to generated files when any relation in the definition has an expiring allowed type. Non-expiring schemas regenerate byte-identically.

### Verified

- All 4 e2e packages pass.
- Codegen idempotent at new baseline.
- `go build ./...` + `go vet ./...` clean.

### Deferred (carried forward from earlier jobs)

- `Read<Rel><Type>Relations` still strips `OptionalCaveat` AND `OptionalExpiresAt` from response tuples. A future `Read<Rel><Type>RelationsWithMetadata` would surface both. Tracked in AUZ-007 Discoveries Gap C and SPEC-004 C10.
- `CONDITIONAL_PERMISSION` in the Check path still collapses to `ErrPermissionDenied`; `PartialCaveatInfo.MissingRequiredContext` is dropped.

## [1.2.0] - 2026-05-08

Closes the Lookup correctness gap from v1.1.0 — `Lookup<Perm><Type>Resources` and `Lookup<Perm><Type>Subjects` for caveat-reaching permissions thread request-time `Context` through to SpiceDB, and `Permissionship == CONDITIONAL_PERMISSION` results are now filtered out of the returned ID slice (matching `Check<Perm>`'s `errorIfDenied` collapse-to-deny behavior).

### Added

- **`Engine.LookupResourcesWithCaveat`** — interface method threading `caveatParams` through `LookupResourcesRequest.Context`. Definite grants only.
- **`Engine.LookupSubjectsWithCaveat`** — same shape for `LookupSubjectsRequest`.
- Generated `Lookup<Perm><Type>Resources` for caveat-reaching permissions reads `input.Caveats` (already on the existing `Check<Perm>Inputs` shape) and routes through the new engine method.
- 4 new e2e tests covering granted-with-caveat (Subjects + Resources), CONDITIONAL filtered (no caveat supplied), and wrong-caveat filtered.

### Changed

- **BREAKING**: caveated `Lookup<Perm><Type>Subjects` signature changes from `(ctx)` to `(ctx, caveats Check<Perm>Caveats)`. Non-caveated permissions (e.g. `LookupBrowseUserSubjects` on the default `viewer` permission) keep their existing `(ctx)` signature.
- **Permissionship filter applied to all 4 Lookup paths.** The pre-existing `LookupResources` / `LookupSubjects` methods now also filter `Permissionship != HAS_PERMISSION`. For non-caveat permissions this is a no-op (no caveat → no CONDITIONAL); for caveated paths it closes the silent false-positive class where v1.1.0 returned conditional grants as if they were definite.

### Verified

- All 4 e2e packages pass.
- Codegen idempotent at new baseline.
- `go build ./...` + `go vet ./...` clean.

### Deferred

- `Read<Rel><Type>Relations` still strips caveat metadata. A future job will surface attached caveat info per tuple via `Read<Rel><Type>RelationsWithCaveat` returning `[]ReadResult[T]{ID, Caveat, CaveatName}`.
- `CONDITIONAL_PERMISSION` in the Check path still collapses to `ErrPermissionDenied`; `PartialCaveatInfo.MissingRequiredContext` is dropped. Surfacing missing keys distinctly from hard deny is a future "rich signal" change.

## [1.1.0] - 2026-05-08

End-to-end caveat support — read side (`Check<Perm>`) and write side
(`Create<Rel>Relations`), plus the supporting runtime, template, and
e2e fixture.

### Added

- **Caveat codegen.** Relations and allowed types declared `with <caveat>` generate a typed `<CaveatPascal>Args` struct per caveat (one per namespace). The `<Rel>Objects` and `Check<Perm>Inputs` structs gain a nested `Caveats` sub-struct mirroring the existing `Wildcards` pattern, with one typed pointer field per caveated allowed type (writes) or per unique reachable caveat (checks).
- **`Engine.CheckPermissionWithCaveat`** — new interface method threading caveat parameters through `CheckPermissionRequest.Context` as a `*structpb.Struct`. Generated `Check<Perm>` builds the merged map from non-nil `input.Caveats.<Caveat>` fields and routes accordingly.
- **`Engine.CreateRelationsWithCaveat`** — new interface method emitting `RelationshipUpdate.Relationship.OptionalCaveat = &v1.ContextualizedCaveat{CaveatName, Context}`. Generated `Create<Rel>Relations` per-allowed-type routing: caveat-bearing branches go through this method with the codegen-known caveat name as a string literal; non-caveated branches stay on `CreateRelations`.
- **Multi-caveat per permission.** `Check<Perm>Inputs.Caveats` holds one field per **unique caveat name** reachable from the permission (named `<CaveatPascal>`); the generated `Check<Perm>` body merges every non-nil entry into a single wire `Context`. Cross-caveat parameter-name collisions are detected at codegen via `detectPermCaveatCollisions` and emit a clear error.
- **Per-field pointer types** in `<CaveatPascal>Args` for partial binding within a single caveat. Scalar parameters become `*T` (`*string`, `*int`, `*bool`, `*float64`, `*uint`); container types (`[]T`, `[]byte`, `map`) stay direct. Callers can write-bind some keys (policy) and defer others (request data) to check time within the same caveat. Uses Go 1.26's `new(expr)` builtin for ergonomic pointer literals — `new("acme")`, `new(5)`, `new(true)`.
- **Disambiguated field names** when `(Namespace, IsWildcard)` collides on a relation. `relation foo: user with cav_a | user with cav_b` generates `UserCavA` / `UserCavB` ID-slice and `Caveats` fields per branch — caller picks per-batch which caveat applies. Non-colliding schemas keep their existing field names.
- **Wildcard + caveat** relations supported (`type:* with caveat`). Wildcard branch consumes the same `Caveats.<Type>` field as the regular branch.
- **Multi-namespace caveats** verified (caveats in `extsvc`, `bookingsvc`, `menusvc`).
- **40 e2e tests** against live SpiceDB cover defer/pre-bind binding, wildcard + caveat, mixed caveated/non-caveated relations, multi-caveat-per-permission, write-time precedence, delete-on-caveated-tuple, all supported parameter types (string, bool, int, uint, double, bytes, list<T>, nested list<list<T>>), all permission operators (union, arrow, intersection, exclusion), and within-single-caveat partial binding.

### Changed

- **Engine interface expanded** with `CheckPermissionWithCaveat` and `CreateRelationsWithCaveat`. The only implementation in this repo is `*spicedb.Engine`; external implementers must add the methods.
- **`<Rel>Objects.Caveats` sub-struct** replaces the previous flat `<TypeName>Caveat` field convention from earlier development snapshots; final API mirrors `Wildcards` for symmetry.
- **Scalar caveat parameter mapping**: `int` → Go `int` (not `int64`); `uint` → Go `uint` (not `uint64`). Idiomatic Go default; no precision loss on 64-bit platforms (which are universal for SpiceDB clients).
- **`serializeCaveatMap` runtime helper** extended with `coerceStructpbValue` and reflection-based fallback to convert typed slices (`[]string`, `[]int`, `[][]string`) into `[]any` at the wire boundary; `[]byte` short-circuits so `structpb`'s native base64 encoding kicks in.

### Verified

- All 4 e2e packages pass (`pkg/authz/spicedb`, `example/authzed/{bookingsvc,extsvc,menusvc}`).
- Codegen idempotent — `git diff --quiet example/authzed/` exits 0 after a second regen against the new baseline.
- `go build ./...` + `go vet ./...` clean.

### Deferred

Documented in `jobs/AUZ-007-write-time-caveat-codegen.md` Discoveries:

- `Lookup<Perm><Type>Resources` / `Lookup<Perm><Type>Subjects` don't yet pass request-time `Context` for caveated permissions, and they silently include `CONDITIONAL_PERMISSION` results as if they were `HAS_PERMISSION`. Fix is one job (correctness + missing input).
- `Read<Rel><Type>Relations` strips caveat metadata. A future `Read<Rel><Type>RelationsWithCaveat` would surface attached caveat info per tuple.
- `CONDITIONAL_PERMISSION` still collapses to `ErrPermissionDenied` in the Check path; `PartialCaveatInfo.MissingRequiredContext` is dropped. A future signal-surfacing change could expose missing keys.

## [1.0.0] - 2026-05-XX

Initial release. Codegen produces `.gen.go` per `definition` block with
typed constructors, relation writers, and per-permission `Check` /
`Lookup` methods over a SpiceDB-backed `authz.Engine`. Schema support
covers union, arrow, intersection, exclusion, and wildcard relations.
Caveats and expiration traits are rejected at adapt time. End-to-end
verified against a real SpiceDB container via `testcontainers-go`.
