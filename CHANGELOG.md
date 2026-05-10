# Changelog

All notable changes to this project are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.14.0] - 2026-05-10

Adds OPA Go builtins codegen. With the new `--emit-opa` CLI flag, the generator emits a per-package `opa.gen.go` exposing every `Check<Perm>` and `Lookup<Perm>Resources` method as an OPA custom builtin invocable from Rego policies. Pattern 4 of RFC-001's policy-engine integration tiers — embedded OPA in a Go-first stack with the existing `authz.Engine` gRPC connection reused for SpiceDB calls. See `docs/RFC-001`, `docs/ADR-004`, `docs/scope-opa-go-builtins-codegen.md`, `docs/spec-013-opa-go-builtins-codegen.md`.

### Added

- **`--emit-opa` CLI flag** on `cmd/authzed-codegen` — opt-in; default off. When set, emits `opa.gen.go` per package alongside existing `<entity>.gen.go` files.
- **`OPATemplate` embed** in `internal/templates/embed.go` backed by `internal/templates/opa.go.tmpl`.
- **`Generator.GenerateOPASource(tmplStr string)` method** in `internal/generator/opa.go` (split from `generator.go` for separation of OPA-specific generator code) plus `OPAPackageView` struct + `groupByPackage` helper. Definitions and permissions sorted alphabetically per (Resource, Permission) for round-trip determinism.
- **Per-package `SpiceDBBuiltins(engine authz.Engine, ctx context.Context) []func(*rego.Rego)`** in generated `opa.gen.go`. Returns OPA registration options for every Check / Lookup method in the package. Caller passes the result to `rego.New(opts...)`.
- **Uniform builtin signatures** — Check is 3-arg `(subject string, resource_id string, caveat_context object) → bool`; Lookup is 2-arg `(subject string, caveat_context object) → []string`. `subject` is a `"type:id"` string. `caveat_context` is always required at the call site; pass `{}` when no caveat applies.
- **Caveat-aware dispatch** — closure inspects `caveat_context`; empty → `Engine.CheckPermission` / `Engine.LookupResources`; non-empty → `*WithCaveat` variant via `structpb.NewStruct(m)` conversion.
- **Engine-error mapping** — `authz.ErrPermissionDenied` / `authz.ErrConditionalPermission` map to `BooleanTerm(false)` (policy denial signals); any other engine error fails the Rego eval with `fmt.Errorf` (system error). Distinct from policy denial per SPEC-013 C4.
- **Lookup result extraction** — returns `LookupResult.Definite` only as a Rego `[]string`. `Conditional` entries dropped silently with a Go doc comment naming the limitation.
- **OPA dependency** — `github.com/open-policy-agent/opa v1.16.1` added to `go.mod` (latest stable v1.x as of 2026-05-10).
- **e2e test** in `example/authzed/extsvc/extsvc_opa_test.go` — exercises no-caveat path, with-caveat match, with-caveat mismatch, and Lookup against a live SpiceDB testcontainer.
- **Generated fixtures** — `example/authzed/{bookingsvc,menusvc,extsvc}/opa.gen.go` committed (round-trip regression bar).

### Notes

- **API divergence from SPEC-013**: The exported function is `SpiceDBBuiltins(engine, ctx) []func(*rego.Rego)` (returns options) rather than the SPEC's `RegisterSpiceDBBuiltins(r, engine, ctx)` (mutates r). The mutate-r approach silently failed at eval time because `rego.New` builds the AST compiler with `WithBuiltins(r.builtinDecls)` BEFORE post-construction `Function3(...)(r)` calls take effect. The options-slice form is the canonical Go-OPA pattern. Captured in `jobs/AUZ-019` Discoveries.
- **Subject-argument format**: `"type:id"` string instead of separate type+id args. The existing typed `Check<X>` methods accept multi-subject-type Inputs structs and dispatch internally; the OPA binding bypasses that layer to call `Engine.CheckPermission` directly. SPEC's stated arity (3-arg Check, 2-arg Lookup) is preserved.
- **OPA imports use the canonical `/v1/...` paths** — generated files import `github.com/open-policy-agent/opa/v1/{ast,rego,types}`. The legacy paths (`github.com/open-policy-agent/opa/{ast,rego,types}`) carry an upstream `Deprecated:` marker pointing at `/v1/...` as the recommended location. Symbols are identical; the legacy shim is a thin re-export. SPEC-013 C2 enumerated the legacy paths; the implementation flipped to canonical after the deprecation warning surfaced.
- **Out of scope** for this release: `Create<Rel>Relations` builtins (writes don't fit Rego's pure-eval model), Conditional Lookup surfacing, build-tag opt-in, README documentation, `runtime.NewRuntime` HTTP server scaffolding, OPA decision-log / bundle-distribution wiring, CEL bindings (Pattern 2), Rego/HTTP helpers (Pattern 3). Each carries an explicit reason in `docs/scope-opa-go-builtins-codegen.md`.
- **Caller migration** for users wanting OPA bindings: opt in with `--emit-opa`; commit the generated `opa.gen.go` files; pull `SpiceDBBuiltins` into `rego.New(opts...)`; write Rego policies that invoke `<package>.check_<resource>_<perm>` / `<package>.lookup_<resource>_<perm>_resources`. Without `--emit-opa`, behavior is unchanged.

## [1.13.0] - 2026-05-09

Extends caveat parameter type coverage. SpiceDB's `duration`, `timestamp`, and `ipaddress` types now map to typed Go values on generated `<Caveat>Args` structs instead of falling back to `any`. Caller DX improves significantly for the common time-based caveat patterns (rate limiting, session expiry, deadlines) and IP-based access control.

### Added

- **`caveatTypeToGo` extended** in `internal/generator/adapter.go`:
  - `duration` → `*time.Duration`
  - `timestamp` → `*time.Time`
  - `ipaddress` → `*string` (avoids forcing `net` package import on every generated file; user calls `.String()` once at the call site)
- **`caveatValueExpr` template helper** — emits the right Go expression for converting typed caveat fields to structpb-compatible values:
  - `*time.Duration` → `c.Param.String()` (produces "1h0m0s" parseable by `time.ParseDuration`)
  - `*time.Time` → `c.Param.Format(time.RFC3339)`
  - other pointer types → existing `*c.Param` deref (unchanged)
- **3 new caveat fixtures** on `extsvc/`:
  - `extsvc/within_window_d(window duration)` — duration parameter
  - `extsvc/before_deadline(deadline timestamp)` — timestamp parameter
  - `extsvc/from_subnet(client_ip ipaddress)` — ipaddress parameter using CEL's `in_cidr` member function
- **6 new e2e tests** covering grant/deny pairs for duration, timestamp, and ipaddress caveats.

### Changed

- **5 template sites** in `internal/templates/object.go.tmpl` swapped from `{{ deref $param.GoType }}c.{{ ... }}` to `{{ caveatValueExpr $param.GoType ... }}`. Existing caveat fixtures (string, bool, int, uint, double, bytes, list) regenerate byte-identical — `caveatValueExpr` falls through to the same `*deref` semantic for those types.

### Caller pattern

```go
// duration caveat:
window := time.Hour
folder.CreateDurationViewerRelations(ctx, FolderDurationViewerObjects{
    User: []User{user},
    Caveats: FolderDurationViewerCaveats{
        User: &WithinWindowDArgs{Window: &window},  // typed *time.Duration
    },
})
// timestamp caveat:
deadline := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
folder.CreateDeadlineViewerRelations(ctx, FolderDeadlineViewerObjects{
    User: []User{user},
    Caveats: FolderDeadlineViewerCaveats{
        User: &BeforeDeadlineArgs{Deadline: &deadline},  // typed *time.Time
    },
})
```

### Discovered during implementation

CEL doesn't expose an `ipaddress(...)` constructor literal — only the `in_cidr(string)` member function works on IPAddress values. Fixture caveat expression rewritten from `client_ip == ipaddress("10.0.0.1")` to `client_ip.in_cidr("10.0.0.0/24")`. `ipaddress` is a CEL `OpaqueType` registered with that one method; equality / direct comparison against literals isn't supported. Comparison against another IPAddress field is — but for typical "is this IP in our allowed range" use cases, `in_cidr` is the natural pattern.

### Verified

- All 5 e2e packages pass.
- Codegen idempotent at the new baseline.
- Existing caveat fixtures (using string/int/bool/etc. types) regenerate byte-identical to v1.12.0.

### Deferred

- `map<K,V>` caveat parameters — still surfaced as `any`. No clean Go mapping (SpiceDB's typed maps support arbitrary key/value types).
- Permission type annotations (`permission view: user = ...`) — verified during AUZ-017 follow-up that SpiceDB's annotation parser rejects prefixed types; our `RequirePrefixedObjectType()` requirement means annotations conflict with our codegen's input requirements. SpiceDB-side limitation.

## [1.12.0] - 2026-05-09

Accepts SpiceDB's `_self` schema construct — schemas declared with `use self` at the top can now use the `self` keyword in permission expressions. SpiceDB's `checkSelf` evaluator (per `internal/graph/check.go:598-621`) grants only when the subject is **identity-equal** to the resource (same type, same ID, no sub-relation). Most useful in **recursive permission patterns** for tree-shaped data: `permission ancestor_or_self = self + parent->ancestor_or_self` provides the base case for "X or any X-reachable object via the recursion."

After this release, the codegen accepts every commonly-used SpiceDB schema construct. Only the deprecated `_this` and the rare compiler-internal `_nil` remain rejected.

### Added

- **`PermExprSelf` constant** in `internal/generator/adapter.go` parallel to `PermExprIdentifier` / `PermExprArrow` — empty payload; kind alone identifies the construct.
- **Tree resolver case** in `resolvePermissionExpressionTypes` propagating the OwnType into self-reaching permissions. Generated `Check<Perm>Inputs` for self-reaching permissions automatically gains `<ResourceType> []<ResourceType>` field via existing `permissionInputTypes` iteration — zero template change.
- **Schema fixture** on `extsvc/folder`: `use self` directive at the top + `relation parent_for_self: extsvc/folder` + `permission ancestor_or_self = self + parent_for_self->ancestor_or_self`.
- **Adapter unit test** verifying `XSelf` maps to `PermExprSelf` with empty payload.
- **5 e2e tests** covering identity match, identity mismatch, 3-level recursive ancestor walk, outside-chain deny, LookupResources tree walk (returns input folder + all descendants).

### Changed

- **`lowerSetOperationChild` refactored** to use type assertion on `c.GetChildType()` instead of `c.Get*() != nil` accessors. Required because SpiceDB's `namespace.Self()` / `namespace.This()` / `namespace.Nil()` factories construct empty-marker oneof variants with NIL inner fields — `c.GetXSelf()` returns nil even when the wrapper is the active oneof variant. Type assertion is the reliable detection path.
- **Cycle detection in `resolveTransitive` relaxed** to allow recursive permission expressions (the canonical `_self` use case). Returns empty types on revisit; the dedup in the outer call combines correctly.
- **README Schema Support table** — `_self` graduates to ✓; remaining rejected constructs are now `_this`, `_nil`, and `with self` (functioned tuple-to-userset's `with self` form, distinct from the `_self` keyword).

### Use cases for `_self`

- **Tree containment** — `permission ancestor_or_self = self + parent->ancestor_or_self` (folder/document hierarchies)
- **Reflexive permissions** — `permission self_check = self` (sentinel pattern)
- **Self-ownership in graphs** — `permission referential_integrity = self + linked->is_canonical`

The recursive-permission pattern is the most valuable: without `_self`, you can't express "X or any X-reachable object via the recursion" cleanly — schema authors had to require callers to add the starting resource manually.

### Verified

- All 5 e2e packages pass.
- Codegen idempotent at the new baseline.
- `go build ./...` + `go vet ./...` clean.
- Per-namespace `.gen.go` files unchanged for definitions without `_self` usage.

### Discovered during implementation

**SpiceDB's `namespace.Self()` factory emits empty-marker oneof variants with NIL inner fields.** The constructor pattern is:

```go
func Self() *core.SetOperation_Child {
    return &core.SetOperation_Child{
        ChildType: &core.SetOperation_Child_XSelf{},  // wrapper present, inner XSelf field NIL
    }
}
```

Same for `This()` and `Nil()`. The Go proto-generated `c.GetXSelf()` accessor returns the inner field, which is nil → `c.GetXSelf() != nil` evaluates false even when the wrapper IS the active oneof variant. Initial implementation used `Get*` accessors and silently fell through to default ("unknown rewrite child type") for legitimate `_self` schemas.

Fix: switched the entire `lowerSetOperationChild` switch to type assertion on `c.GetChildType().(type)`. The functioned-TTU and other content-bearing cases work either way, but the empty-marker cases REQUIRE type assertion. Refactoring to type assertion is more idiomatic for proto oneofs in Go.

**Cycle detection was overly strict.** The original `resolveTransitive` rejected ANY recursive permission with "cycle detected." But recursive permissions are precisely the canonical `_self` use case (`permission p = self + parent->p`). Relaxed cycle detection to return empty types on revisit; the outer call's dedup merges everything correctly. SpiceDB's evaluator handles the recursion server-side with cycle detection at Check time.

### Deferred

- `_this` — fully deprecated upstream; permanent rejection.
- `_nil` graceful skip — internal compiler artifact; users don't write it.
- Functioned `with self` arrow form (`parent.any(view).self`) — distinct from the `_self` keyword; rare specialised pattern.

## [1.11.0] - 2026-05-09

Accepts SpiceDB's functioned tuple-to-userset syntax — schemas using `.any(view)` or `.all(view)` arrow function syntax now compile. `.any()` is semantically equivalent to a regular arrow `parent->view`; `.all()` is the genuinely-new strict-intersection semantic ("subject must reach the inner permission via EVERY parent row, not just any one") used in dual-control / multi-approver / cross-region patterns.

The codegen treats functioned arrows identically to regular arrows — function value (`FUNCTION_ANY` / `FUNCTION_ALL`) is server-side semantic enforced by SpiceDB at Check time. Generated `Check<Perm>` / `Lookup<Perm>` method signatures are byte-identical between regular and functioned arrows; the difference is invisible to caller-facing API.

### Added

- **Adapter** in `internal/generator/adapter.go` — `lowerSetOperationChild` handles `FunctionedTupleToUserset` parallel to the existing `TupleToUserset` branch. Maps `tupleset.relation` → `LeftRel` and `computed_userset.relation` → `RightPerm`.
- **Schema fixtures** on `extsvc/folder` exercising both functions plus combinations:
  - `relation any_parent: extsvc/folder` + `permission any_via = any_parent.any(browse)` (regular form)
  - `relation all_parent: extsvc/folder` + `permission all_via = all_parent.all(browse)` (strict-intersection)
  - `relation gated_parent: extsvc/folder with extsvc/tenant_match` + `permission gated_all_via = gated_parent.all(browse)` (`.all()` reaching a caveated LeftRel — verifies caveat collection extends to functioned arrows)
  - `relation direct_member: extsvc/user` + `permission mixed_all = direct_member + all_parent.all(browse)` (mixed expression — regular identifier combined with functioned arrow)
- **2 new adapter unit tests** verifying `.any()` and `.all()` map to `PermExprArrow` correctly (function value not stored).
- **8 new e2e tests** covering: `.any()` single-parent grant; `.all()` two-parent both grant; `.all()` two-parent only one grants → deny (proves strict intersection); `.all()` zero parents → vacuous deny; `.all()` + matching caveat → grant; `.all()` + caveat false → deny; mixed expression direct path; mixed expression `.all()` path.

### Changed

- **README Schema Support table** gains a row for functioned arrows (`.any()` / `.all()`) marked ✓.

### Caller pattern (no API change vs regular arrows)

```go
// Regular arrow:
folder.CheckView(ctx, input)         // permission view = parent->browse

// Functioned `.all()` arrow — same caller surface:
folder.CheckAllVia(ctx, input)       // permission all_via = all_parent.all(browse)
// SpiceDB enforces: subject must reach `browse` via EVERY all_parent row
```

### Use cases for `.all()`

- **Dual-control / four-eyes** — `permission deploy = approver_pool.all(approved)` (every approver must sign off)
- **Multi-tenant compliance** — `permission process = jurisdiction.all(compliant)` (every regulator must clear)
- **Cross-region replication** — `permission read = region.all(authorized_reader)` (every region must authorize)
- **Multi-team ownership** — `permission merge = owning_team.all(reviewer)` (every team must have a reviewer approve)

The alternative — N Check calls intersected client-side — has measurable cost (N round-trips), no atomic evaluation, and shifts the semantic into application code. `.all()` does it in one Check against a single SpiceDB snapshot.

### Verified

- All 5 e2e packages pass.
- Codegen idempotent at the new baseline.
- `go build ./...` + `go vet ./...` clean.
- Per-namespace `.gen.go` files unchanged for definitions without functioned-TTU usage.

### Deferred (carried forward)

- `_self` schema construct (`use self`) — reflexive permissions; less common pattern. Future SPEC if real schema needs it.
- `_nil` graceful skip — internal compiler artifact; users don't write it. Defensive polish if it ever appears.
- `_this` — fully deprecated upstream; permanent rejection.

## [1.10.0] - 2026-05-09

**Stable milestone.** Same code as v1.9.0; this release marks the API stability commitment going forward. From v1.10 onward, breaking changes to the `Engine` interface, runtime types in `pkg/authz/`, or generated method signatures require a major bump (v2.0). Active-development minor bumps with breaking changes (the v1.0–v1.9 pattern, e.g. v1.4 changed `ReadRelations` return type, v1.7 changed `Lookup*` return types) end here.

### Added

- **Versioning policy** documented in README. Semver-real from v1.10 onward: major (v2.0) = breaking, minor (v1.11+) = additive, patch (v1.10.1) = fixes.

### Changed

- **ADR-001 rejection list** refreshed. Constructs lifted across v1.0–v1.9 (intersection/exclusion, caveats, expiration, sub-relation references, caveat definitions) annotated with their shipping job + SPEC. Only `_this` and functioned tuple-to-userset (`with self`) remain rejected — both rare in production schemas.
- **README Schema Support table** gains a row noting the still-rejected constructs explicitly.

### What's stable as of v1.10

End-to-end SpiceDB feature coverage:
- ✅ Union, arrow, intersection, exclusion permission operators
- ✅ Wildcard relations
- ✅ Caveats (write-time pre-context + check-time context, multi-caveat-per-permission, partial binding via per-field pointers)
- ✅ Expiration traits with auto-TOUCH semantics
- ✅ Sub-relation references (userset writes, reads, and check inputs)
- ✅ Read with metadata (caveat name + context + expiry per tuple)
- ✅ Lookup with conditional surfacing (Definite + Conditional partition with MissingKeys)
- ✅ Conditional permission rich signal on Check (typed error + backward-compat `Is`)
- ✅ Per-call consistency mode override via context
- ✅ Schema drift detection at startup via `VerifySchema(ctx)`

### Verified

- All 5 e2e packages pass.
- Codegen idempotent (zero diff vs v1.9.0).
- `go build ./...` + `go vet ./...` clean.

### Deferred (carried forward)

Tracked openly in CHANGELOG entries from earlier releases:
- `_this` and `with self` schema constructs — rejected at adapt time; revisit if a real schema needs them.
- Iterator API for `ReadRelations` — `[]RelationTuple` materializes; SpiceDB stream is wasted.
- Token-based consistency modes (`AtLeastAsFresh`, `AtExactSnapshot` with caller-supplied tokens).
- Conditional wildcards on `HasPublicSubject` / `Lookup<Perm><Type>WildcardSubjects` — extremely rare in practice.
- Auto-retry helper for `*ConditionalPermissionError` — caller's concern.
- Watch API codegen — change feeds for cache invalidation.
- Lookup pagination/cursor — for large result sets.

## [1.9.0] - 2026-05-09

Adds runtime detection of mismatch between the codegen baseline schema and the schema currently deployed in SpiceDB. Closes a class of silent production bugs: binary built against schema v1 calling SpiceDB running schema v2 → mis-permission everything with no error path. The codegen now emits a top-level `<output-dir>/schema.gen.go` containing the source `.zed` bytes verbatim plus `VerifySchema(ctx)` helper that calls SpiceDB's `DiffSchema` RPC, server-side normalises, and partitions the typed diffs into severity buckets. Caller hard-fails at startup on `IsBreaking()`.

### Added

- **`authz.SchemaDrift`** — runtime drift report with `Added`, `Removed`, `Changed`, `Cosmetic` `[]DriftEntry` slices, plus `IsBreaking()` / `IsClean()` predicates.
- **`authz.DriftEntry`** — single drift row carrying `Description string` (human-readable) and `Raw *v1.ReflectionSchemaDiff` (typed wire payload for fine-grained handling).
- **`Engine.DiffSchema(ctx, comparisonSchema)`** — new interface method calling SpiceDB's `SchemaService.DiffSchema` RPC.
- **`*spicedb.Engine.DiffSchema`** — single-call implementation; consistency override intentionally not applied (schema service has its own consistency model).
- **Generated `<output-dir>/schema.gen.go`** — new top-level file. Package name derived from the output dir's last segment (e.g. `--output example/authzed` → `package authzed`). Contains:
  - `SchemaText` — verbatim source bytes (escaped via `strconv.Quote` for safety; backticks in schema comments are common)
  - `SchemaDigest` — sha256 of the source bytes for cheap pre-flight equality
  - `VerifySchema(ctx)` — calls the engine, buckets diffs, returns `SchemaDrift`
  - `bucketDrift` / `describeDrift` — internal helpers
- **4 new e2e tests** in `example/authzed/schema_test.go` covering clean state, additive drift, breaking drift, cosmetic drift. Each test boots a fresh SpiceDB sandbox to avoid cross-test schema contamination.

### Changed

- **CLI** (`cmd/authzed-codegen/main.go`) emits one additional file per run at `<output>/schema.gen.go`. No new flags.
- **Generator** (`internal/generator/generator.go`) gains `GenerateSchemaSource(tmplStr, schemaBytes)` method.

### Caller pattern (startup hook)

```go
import authzed "github.com/danhtran94/authzed-codegen/example/authzed"

func main() {
    engine := spicedb.NewEngine(client, 3*time.Second)
    authz.SetDefaultEngine(engine)

    drift, err := authzed.VerifySchema(ctx)
    if err != nil {
        log.Fatalf("schema verification failed: %v", err)
    }
    if drift.IsBreaking() {
        log.Fatalf("schema drift: %d removed, %d changed — refusing to start",
            len(drift.Removed), len(drift.Changed))
    }
    if !drift.IsClean() {
        log.Warnf("schema is ahead of binary: %d added, %d cosmetic",
            len(drift.Added), len(drift.Cosmetic))
    }
    // ... continue with normal startup ...
}
```

### Discovery during implementation

SpiceDB's `DiffSchema` returns diffs from the *comparison schema's perspective* — operations needed to apply to the deployed schema to reach the comparison. So `*_Added` means "comparison has it, deployed lacks it" (BREAKING from binary's perspective) and `*_Removed` means "deployed has it, comparison lacks it" (ADDITIVE). The bucketing in `bucketDrift` inverts these to match the SchemaDrift API's caller-friendly semantics (`Added` = safe additive drift, `Removed` = breaking).

SPEC-010 A7 hypothesis was wrong — `example/schema.zed` does contain backticks (in inline-code comments like `` `with expiration` ``). Resolved by encoding the schema via `strconv.Quote` instead of a raw string literal.

### Verified

- All 5 e2e packages pass (added new `example/authzed` package).
- Codegen idempotent at the new baseline.
- Per-namespace `.gen.go` files byte-identical to v1.8.0 (additive change only).
- `go build ./...` + `go vet ./...` clean.

### Deferred

- **Auto-invoke at engine init** — drift policy is opinionated (fail-fast vs log-and-continue). Caller-driven by design.
- **CLI verify mode** (`authzed-codegen --verify`) — out of scope for v1.9; the runtime helper is the primitive. Future tool.
- **Conditional wildcards** (carried from v1.7) — `HasPublicSubject` and `Lookup<Perm><Type>WildcardSubjects` check `result.Definite` only.

## [1.8.0] - 2026-05-09

Adds caller-controlled consistency mode selection on read-side methods. The `*spicedb.Engine` previously hardcoded a time-based policy (`AtExactSnapshot` post-write, `MinimumLatency` otherwise) — fine for read-your-own-writes but not for security-sensitive checks where stale reads are unacceptable. Callers now opt into `ConsistencyFullyConsistent` via `authz.WithConsistency(ctx, mode)`; the override flows through every Check / Lookup / Read method via context. **Zero codegen template change** — ctx is already plumbed through every generated method.

### Added

- **`authz.ConsistencyMode`** — closed `int` type with `ConsistencyDefault` (=0) and `ConsistencyFullyConsistent` (=1) constants.
- **`authz.WithConsistency(ctx, mode)`** — context helper that derives a child ctx carrying the override.
- **`authz.GetConsistency(ctx)`** — returns the mode set on ctx, or `ConsistencyDefault` if not set. Read by the engine internally.
- **3 new e2e tests** covering: full-consistency override on userset expiration (filters expired tuple), full-consistency override on direct-subject expiration (sanity), full-consistency override on non-expiring tuple (happy path).

### Changed

- **`*spicedb.Engine.getConsistencySnapshot`** — refactored to take `ctx context.Context`. Switches on `authz.GetConsistency(ctx)`: `ConsistencyFullyConsistent` returns wire `Consistency_FullyConsistent`; default branch preserves existing recent-token-or-nil logic. 6 internal call sites in Check / Lookup / Read paths updated to pass ctx.

### Caller pattern

```go
// Default behavior — engine uses recent-token-or-nil:
err := folder.CheckTenantedBrowse(ctx, input)

// Force full consistency for security-sensitive check:
ctx = authz.WithConsistency(ctx, authz.ConsistencyFullyConsistent)
err := folder.CheckTenantedBrowse(ctx, input)
```

The override applies to every read-side method called with that ctx. Caller scope it at the request boundary; all downstream Check/Lookup/Read inherits transparently.

### Verified

- All 4 e2e packages pass.
- Codegen idempotent at the new baseline (zero diff vs v1.7.0).
- `go build ./...` + `go vet ./...` clean.

### Discovered during implementation

AUZ-011 Discoveries hypothesized that `AtExactSnapshot` consistency masks wall-clock expiration on userset tuples — testing showed CheckPermissionUserset returned granted on expired userset tuples under default consistency. Empirical re-verification during AUZ-014 with the same fixture and timing showed expired userset tuples are filtered under BOTH default and FullyConsistent modes. SpiceDB enforces wall-clock expiration regardless of the snapshot revision pin. AUZ-014's value is independent of the AUZ-011 hypothesis: caller-controlled per-call consistency override for security-sensitive workloads where the engine's time-based default policy isn't strong enough.

### Deferred

- **`AtLeastAsFresh` / `AtExactSnapshot` modes for callers** (per SPEC-009 C7). The engine already uses `AtExactSnapshot` internally for read-your-own-writes; surfacing token-based modes to callers needs separate plumbing for caller-supplied ZedTokens, observability for token freshness, and stale-token rejection semantics. Future SPEC.
- **Engine-level global default** — kept per-call for explicit control. A future `Engine.SetDefaultConsistency` could short-circuit if real demand surfaces.

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
