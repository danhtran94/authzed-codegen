# Changelog

All notable changes to this project are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
