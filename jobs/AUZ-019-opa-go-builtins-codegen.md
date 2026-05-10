<!-- approved -->

# AUZ-019: OPA Go builtins codegen

| Field      | Value                                              |
|------------|----------------------------------------------------|
| Status     | Done                                               |
| Created    | 2026-05-10                                         |
| Assignee   | Danh Tran                                          |
| Source     | docs/spec-013-opa-go-builtins-codegen.md           |
| Blocked by | —                                                  |

## Goal

Implement Pattern 4 (OPA Go builtins) codegen per SPEC-013. The generator gains a `--emit-opa` CLI flag that, when set, emits `opa.gen.go` per package alongside the existing `<entity>.gen.go` files. Generated bindings expose every `Check<Perm>` and `Lookup<Perm>Resources` method as an OPA custom builtin via `RegisterSpiceDBBuiltins(r *rego.Rego, engine authz.Engine, ctx context.Context)`. Uniform 3-arg / 2-arg signatures with `caveat_context` always required at the call site (callers pass `{}` when no caveat applies). The example fixtures (`bookingsvc`, `menusvc`, `extsvc`) regenerate with `opa.gen.go` committed; round-trip remains byte-identical. New e2e test in `extsvc` exercises both no-caveat and with-caveat paths against a live SpiceDB testcontainer.

## What Stays Unchanged

- `pkg/authz/` — runtime contract is connection-target-agnostic; bindings call existing methods unchanged
- `internal/generator/adapter.go` — caveat-applicability data already preserved on `*DefinitionView` per AUZ-007
- Existing `<entity>.gen.go` files — codegen output unchanged when `--emit-opa` is off (scope SC8)
- Existing e2e tests for `bookingsvc`, `menusvc`, `extsvc` — not modified; new test is sibling
- Schema fixtures (`example/schema.zed`) — no schema changes for this job
- Wildcard handling, Conditional Lookup result surfacing — out of scope per spec C5 + scope Out of Scope

## Workstreams

### 1. Dependency + scaffold

Adds the OPA dependency and the embed scaffold so subsequent workstreams compile.

| # | Task | File | Status |
|---|------|------|--------|
| 1.1 | `go get github.com/open-policy-agent/opa@<latest stable v1.x>`; record exact version in Discoveries | `go.mod` | [x] |
| 1.2 | `go mod tidy` after adding the dep | `go.sum` | [x] |
| 1.3 | Add `//go:embed opa.go.tmpl` + `var OPATemplate []byte` declaration | `internal/templates/embed.go` | [x] |
| 1.4 | Create empty `internal/templates/opa.go.tmpl` (filled in WS2) | `internal/templates/opa.go.tmpl` | [x] |

**Key details:** Per SPEC C6, the OPA version is recorded in this job's Discoveries section. Per A5, target latest stable v1.x; verify the public API list in SPEC C2 (`ast`, `rego`, `types` symbols) is unchanged at the chosen version.

### 2. Template body

Implements the per-package codegen output. Single template walks the per-package data and emits all builtins + helpers.

| # | Task | File | Status |
|---|------|------|--------|
| 2.1 | Template header: package declaration, imports (`ast`, `rego`, `types`, `structpb`, `pkg/authz`) | `internal/templates/opa.go.tmpl` | [x] |
| 2.2 | Template body: `RegisterSpiceDBBuiltins` function with per-Check `rego.Function3` registrations sorted alphabetically by `(Resource, Permission)` | same | [x] |
| 2.3 | Template body: per-Lookup `rego.Function2` registrations sorted alphabetically by `(Resource, Permission)` | same | [x] |
| 2.4 | Template body: unexported helper `termToStructpb(t *ast.Term) (*structpb.Struct, error)` per SPEC Interface Contracts | same | [x] |
| 2.5 | Template body: unexported helper `astValueToInterface(v ast.Value) (any, error)` covering Bool / Number / String / Null / Array / Object / Set per SPEC Interface Contracts | same | [x] |
| 2.6 | Template logic: dispatch on `caveatCtx == nil` → `engine.Check<X>` else → `engine.Check<X>WithCaveat`; same for Lookup | same | [x] |
| 2.7 | Template logic: emit Go doc comment on each Lookup binding stating "returns LookupResult.Definite as []string; Conditional entries are dropped" per SPEC C5 | same | [x] |

**Key details:** Per SPEC C1, alphabetical sort is required for round-trip determinism. The template must NOT iterate Go map keys directly without sorting — Go map iteration is intentionally non-deterministic. Use sorted slices of `(Resource, Permission)` pairs.

### 3. Generator method + CLI wiring

Wires the template emission into the existing generator + CLI.

| # | Task | File | Status |
|---|------|------|--------|
| 3.1 | Add `func (g *Generator) GenerateOPASource(tmplStr string) error` + `OPAPackageView` struct + `groupByPackage` helper in a new file (split from `generator.go` for separation of OPA-specific generator code) | `internal/generator/opa.go` | [x] |
| 3.2 | `OPAPackageView` struct exposes `PackageName string`, `Definitions []*DefinitionView`; `groupByPackage` buckets `g.Definitions` by `ObjectType.Prefix`, sorts each bucket's defs by `Name`, sorts each def's `Permissions` by `Name` for SPEC C1 deterministic output | same | [x] |
| 3.3 | Add `--emit-opa` bool flag to the existing `flag.FlagSet` (defaults to `false`) | `cmd/authzed-codegen/main.go` | [x] |
| 3.4 | Wire post-namespace generation loop: if `*emitOPA`, iterate generated packages and call `g.GenerateOPASource(string(templates.OPATemplate), pkg)` per package | same | [x] |

**Key details:** Per spec SC8, omitting `--emit-opa` produces output identical to today's commit. Test this by running the codegen WITHOUT the flag and verifying `git diff --quiet example/authzed/` after regeneration.

### 4. Fixture round-trip

Regenerates the example outputs with `--emit-opa` and commits the new `opa.gen.go` files.

| # | Task | File | Status |
|---|------|------|--------|
| 4.1 | Run `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed` | (regenerate) | [x] |
| 4.2 | Verify `example/authzed/bookingsvc/opa.gen.go` exists and contains `RegisterSpiceDBBuiltins` + per-Check / per-Lookup builtins | `example/authzed/bookingsvc/opa.gen.go` | [x] |
| 4.3 | Verify `example/authzed/menusvc/opa.gen.go` exists with same shape | `example/authzed/menusvc/opa.gen.go` | [x] |
| 4.4 | Verify `example/authzed/extsvc/opa.gen.go` exists with same shape | `example/authzed/extsvc/opa.gen.go` | [x] |
| 4.5 | Re-run codegen; verify `git diff --quiet example/authzed/` exits 0 (byte-identical regeneration per scope SC7) | (verify) | [x] |
| 4.6 | Run codegen WITHOUT `--emit-opa`; verify diff against pre-job commit shows ONLY the absence of `opa.gen.go` files (scope SC8) | (verify) | [x] |

**Key details:** The round-trip determinism check (4.5) is the regression bar. Any non-determinism in the template (map iteration, time-based ordering, random IDs) will surface here.

### 5. e2e test

Exercises generated bindings against a live SpiceDB testcontainer.

| # | Task | File | Status |
|---|------|------|--------|
| 5.1 | New test file using existing `spicedbtest.Start(ctx, schema)` pattern | `example/authzed/extsvc/extsvc_opa_test.go` | [x] |
| 5.2 | Test setup: start testcontainer; create `rego.Rego` instance; call `extsvc.RegisterSpiceDBBuiltins(r, engine, ctx)` | same | [x] |
| 5.3 | Test case A — no-caveat path: write a tuple granting access; Rego policy invokes `extsvc.check_folder_view(uid, rid, {})`; assert result equals direct `engine.CheckFolderView(...)` | same | [x] |
| 5.4 | Test case B — with-caveat path: write a caveat-aware tuple from `extsvc/schema.zed`; Rego policy invokes `extsvc.check_<X>(uid, rid, {<param>: <value>})` exercising at least one AUZ-018 caveat parameter type (`duration`, `timestamp`, or `ipaddress`); assert result equals direct `engine.Check<X>WithCaveat(...)` | same | [x] |
| 5.5 | Test case C — Lookup no-caveat path: invoke `extsvc.lookup_folder_view_resources(uid, {})`; assert result list equals `engine.LookupFolderViewResources(...).Definite` | same | [x] |
| 5.6 | Skip handling: use `spicedbtest.ErrDockerUnavailable` sentinel for clean skip when Docker absent (matches existing test pattern) | same | [x] |

**Key details:** Per scope SC9, the e2e is in `extsvc` only. `bookingsvc` and `menusvc` are covered by the round-trip regression in WS4; expanding e2e to all three would triple test runtime without proportional value.

### 6. Verification

Full project verification matches `.claude/CLAUDE.md` build-verify loop.

| # | Task | Status |
|---|------|--------|
| 6.1 | `go build ./...` exits 0 | [x] |
| 6.2 | `go vet ./...` exits 0 (scope SC10) | [x] |
| 6.3 | `go mod tidy` produces no diff | [x] |
| 6.4 | `go test ./pkg/authz/spicedb/... ./example/authzed/...` passes (or skips cleanly without Docker) | [x] |
| 6.5 | Round-trip regression: `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed && git diff --quiet example/authzed/` exits 0 | [x] |

### 7. Documentation

Out of scope per spec; tracked separately under RFC-001 documentation work item. CHANGELOG entry only.

| # | Task | File | Status |
|---|------|------|--------|
| 7.1 | Add CHANGELOG entry under `[1.14.0]` (or next semver) describing `--emit-opa` flag + generated `opa.gen.go` shape | `CHANGELOG.md` | [x] |
| 7.2 | README integration patterns section | — | deferred — separate scope per RFC-001 documentation work item |
| 7.3 | DEPLOYMENT.md changes | — | deferred — no deployment surface affected; codegen is build-time |

## Design Decisions

### Per-package vs per-namespace emission
Generated `opa.gen.go` is **per-package** (one file per Go package directory), not per-namespace (one file per SpiceDB definition). This matches the existing layout where each package's `<entity>.gen.go` files are siblings within the package directory. `RegisterSpiceDBBuiltins` is exported once per package; users register all builtins in the package with one call.

### Sort key for builtin ordering
Builtins are sorted by `(ResourceTypeName, PermissionName)` alphabetically. Resource type comes first (so all `Check<Folder>*` group together) followed by permission name. Lookup builtins follow the same pair after the Check block. This makes diffs readable when a new permission is added.

### Caveat-aware dispatch logic
The template emits per-method dispatch based on whether the underlying method has a `WithCaveat` variant in the existing typed codegen:

- Methods **with** a caveat-aware variant → emit `if caveatCtx == nil { ... } else { ... WithCaveat ... }` dispatch
- Methods **without** a caveat-aware variant → emit no-caveat call only; ignore `caveatCtx` argument; emit Go doc comment "caveat_context is ignored — no caveat-aware variant in this schema"

The template walks `*DefinitionView` to determine which path per Check / Lookup method. Always-required `caveat_context` at the call site (SPEC scope) means the binding signature stays uniform across both cases; only the body branch differs.

### CHANGELOG version bump
Per the project's semver-real commitment from v1.10, this addition is **MINOR (v1.14.0)**: new feature, backwards-compatible (default `--emit-opa=false` preserves existing output). Changelog entry under `[1.14.0]` describing the new flag, the generated `opa.gen.go` shape, the OPA dependency cost, and the e2e test addition.

## Implementation Order

```
WS1 — Setup (deps + embed scaffold)        ← unblocks WS2 + WS3
WS2 — Template body                         ← depends on WS1; can develop without WS3 wiring
WS3 — Generator method + CLI flag           ← depends on WS1; integrates with WS2 once template exists
WS4 — Fixture round-trip                    ← depends on WS2 + WS3 complete
WS5 — e2e test                              ← depends on WS4 (test imports the generated package)
WS6 — Verification                          ← depends on WS5
WS7 — Documentation (CHANGELOG)             ← can run parallel to WS6
```

## Notes

- **OPA version target**: latest stable v1.x as of implementation start (~2026-05). Recorded in Discoveries below at execution time. SPEC C6 names `go.mod` as the pin location.
- **`*PackageView` precedent**: WS3 task 3.2 may discover that the existing generator does NOT have a per-package aggregation today — generation runs per-namespace. If so, introduce a minimal `*PackageView` (Namespace string, Definitions []*DefinitionView) at the cmd-level wiring layer; do not refactor the existing per-namespace path. Capture the decision in Discoveries.
- **Public API verification (SPEC A5)**: at WS1.1 completion, run `go doc github.com/open-policy-agent/opa/rego` and confirm `Function1`, `Function2`, `Function3`, `Function`, `BuiltinContext` are exported at the chosen version. If any C2-listed symbol is unavailable, surface immediately — SPEC needs revision before WS2 starts.
- **Round-trip determinism debugging**: if WS4.5 fails, the most likely culprit is non-deterministic map iteration in the template. Search the template for `range` over maps; replace with a sorted-keys pre-pass.
- **Test fixture caveat selection**: `extsvc` has 3+ caveats per AUZ-018 (`within_window_d`, `before_deadline`, `from_subnet`). Pick one for WS5.4 — `from_subnet` (ipaddress) is the cleanest because it has obvious deny scenarios (different IP).

## Discoveries & Decisions During Implementation

### [Implementer] OPA version pinned at v1.16.1
`go get github.com/open-policy-agent/opa` resolved to **v1.16.1** at WS1.1 (2026-05-10). This is latest stable v1.x as of implementation; satisfies SPEC C6 / RFC-001 A2. Module path note: per-instance helpers `Function1` / `Function2` / `Function3` / `Function4` exist at `github.com/open-policy-agent/opa/v1/rego` but the legacy compat re-export at `github.com/open-policy-agent/opa/rego` exposes only the global `RegisterBuiltinN` style. Since SPEC scope requires per-instance registration on a `*rego.Rego` (`RegisterSpiceDBBuiltins(r, engine, ctx)`), the codegen imports the canonical `/v1/...` paths. SPEC C2 enumerated `github.com/open-policy-agent/opa/{ast,rego,types}`; the actual import paths used are `github.com/open-policy-agent/opa/v1/{ast,rego,types}`. Same Go module, just a thin compat-vs-canonical distinction.

### [Implementer] Generator file split — `opa.go` instead of `generator.go`
SPEC-013 originally placed `GenerateOPASource` inside `internal/generator/generator.go`. The implementation puts it in a new sibling file `internal/generator/opa.go` along with the `OPAPackageView` struct and `groupByPackage` helper. Reason: `generator.go` is already 545 lines; splitting OPA-specific code into its own file mirrors the per-feature organization pattern used elsewhere in the project (e.g. `adapter.go` is separate from `generator.go`). Task 3.1 file column updated.

### [Implementer] Subject argument shape — "type:id" string instead of separate type+id args
SPEC-013 Interface Contracts described Check builtins as `(subject_id string, resource_id string, caveat_context object) → bool`, implicitly assuming a single subject type per Check. Reading the existing typed Check method bodies (e.g. `CheckFolderBrowse`) revealed each Check accepts a typed `Inputs` struct with **multiple** subject-type slices (User, Group, Role, …) and dispatches per non-empty slice via `Engine.CheckPermission(ctx, Resource, Permission, SubjectType, []ID)`. The Rego binding therefore needs both subject TYPE and subject ID. Two design choices considered:
- 4-arg form `(subj_type, subj_id, res_id, ctx)` — type-safe at OPA boundary; deviates from SPEC's stated arity
- 3-arg form `("type:id", res_id, ctx)` — combines type+id; preserves SPEC's stated arity and Shape B's "single builtin always require ctx" pattern

Picked the 3-arg form. Format `subject` parameter is a `"type:id"` string; binding parses, validates, and calls `Engine.CheckPermission` directly (bypassing the typed Check methods). Lookup mirrors at 2-arg `("type:id", ctx)`. SPEC's stated arity preserved; format is a Discovery.

**Superseded by AUZ-022**: the `"type:id"` string forced `sprintf("extsvc/user:%s",[id])` in every policy and couldn't carry multiple subjects. AUZ-022 replaced it with a keyed object — `{"extsvc/user": id}` or `{"extsvc/user": [ids...], "extsvc/group": [...]}` — that mirrors the codegen's own typed `Check<X>Inputs{User, Group, Role}` shape, supports multi-subject (AND across present keys, matching the typed method), and gets compile-time value-type checking from OPA. The arity is unchanged (3-arg Check, 2-arg Lookup); only the first arg's type changed (string → object). 1.14.0 was unreleased, so the string form never shipped externally.

### [Implementer] Exported API changed: `SpiceDBBuiltins` returns options, replacing SPEC's mutate-r `RegisterSpiceDBBuiltins`
SPEC-013 Interface Contracts named the exported function `RegisterSpiceDBBuiltins(r *rego.Rego, engine, ctx)` — caller would create `r := rego.New(...)` then call `Register...(r, engine, ctx)` to mutate r. First WS5 e2e test run failed at policy compile time with `rego_type_error: undefined function extsvc.check_folder_browse`. Root cause: `rego.New(opts...)` builds the AST compiler with `WithBuiltins(r.builtinDecls)` BEFORE returning (line 1389 of `v1/rego/rego.go`); options applied via `Function3(...)(r)` AFTER `New` returns mutate `r.builtinDecls` but the compiler has already been built with whatever map state existed at New-time (empty). Fix: change the API to return registration options as a slice; caller passes them to `rego.New` directly:

```go
opts := []func(*rego.Rego){rego.Query(...), rego.Module(...)}
opts = append(opts, extsvc.SpiceDBBuiltins(engine, ctx)...)
r := rego.New(opts...)
```

Generated function renamed `RegisterSpiceDBBuiltins` → `SpiceDBBuiltins`; signature `(*rego.Rego, authz.Engine, context.Context)` → `(authz.Engine, context.Context) []func(*rego.Rego)`. SPEC-013 Interface Contracts and the per-Check / per-Lookup closure shapes update to reflect this. Updated CHANGELOG entry under [1.14.0] notes the divergence prominently. The verbal contract — "register SpiceDB builtins on a rego.Rego instance" — is preserved; only the mechanical shape of registration moves from method-call to options-slice.

**AUZ-021 follow-up**: the per-instance `SpiceDBBuiltins` form is the right default (no global state, per-call `engine`/`ctx` flexibility) but it's invisible to `runtime.NewRuntime`, which builds its compiler at construction time and reads only OPA's process-global builtin universe. AUZ-021 added a sibling `RegisterSpiceDBBuiltinsGlobal(engine, ctx)` that registers via `rego.RegisterBuiltin2/3` (global), unlocking `runtime.NewRuntime` / the standard `/v1/data` endpoint. Both functions reuse the same per-Check/per-Lookup decl + closure fragments (factored into shared `{{ define }}` template blocks). The earlier "SpiceDBBuiltinDecls() variant" idea floated in AUZ-020 is superseded by AUZ-021's cleaner `RegisterSpiceDBBuiltinsGlobal` shape.

### [Implementer] OPA import path: switched to canonical `/v1/...` after deprecation warning
First regeneration emitted `opa.gen.go` files importing `github.com/open-policy-agent/opa/{ast,rego,types}` per SPEC C2's enumerated paths. The IDE flagged each as `Deprecated: This package is intended for older projects transitioning from OPA v0.x ...` pointing at `github.com/open-policy-agent/opa/v1/{ast,rego,types}` as the recommended path. Both paths expose `Function1` / `Function2` / `Function3` / `Function4` and the closure types — the legacy is a thin compat re-export of the canonical. Decision: switch to `/v1/...` paths in the template and the e2e test import. SPEC C2's enumerated symbol list still applies; only the import path moves from legacy to canonical. Round-trip and tests remain green; build and vet clean. The legacy compat shim remains available for the lifetime of OPA v1.x but the deprecation warning is the upstream-authoritative signal that `/v1/...` is now the documented default.

### [Implementer] e2e caveat type chosen: `tenant_match` (string), not AUZ-018 (`duration`/`timestamp`/`ipaddress`)
Task 5.4 said "exercising at least one AUZ-018 caveat parameter type (`duration`, `timestamp`, or `ipaddress`)." The implementation uses the `tenant_match` caveat (string param). Rationale: tenant_match is the simplest caveat that's runtime-context-only (writes a tuple with empty pre-context, requires Check-time runtime context); AUZ-018 caveats (subnet/window/deadline) typically use pre-context attached at write time, so an OPA test passing an EMPTY `{}` would still grant via the pre-context — not exercising the with-caveat dispatch through the OPA binding. The architectural goal of SC9 is to verify the with-caveat path (`structpb.NewStruct(m)` conversion + `Engine.CheckPermissionWithCaveat`); tenant_match exercises this end-to-end. Per SPEC C3, structpb conversion is uniform across all caveat parameter types, so coverage of one string-param caveat validates the path; the per-type coercion is SpiceDB's responsibility (already covered by AUZ-018 typed-method e2e tests). Accept the deviation.
