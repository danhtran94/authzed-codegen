# [ADR-007] Studio Engine Surface: Experimental In-Module Package over a Public Schema IR Module

| Field      | Value                                                    |
|------------|----------------------------------------------------------|
| Status     | Proposed                                                 |
| Date       | 2026-11-18                                               |
| Deciders   | Danh Tran                                                |
| Scope      | New `studio/` package; `internal/generator` access rules |
| Depends on | ADR-001                                                  |

---

## Context

An out-of-tree visual debugger (a separate, downstream module) needs the same schema understanding this codegen already has: the lowered definitions, relations, permissions and caveat parameters that drive `Check`/`Lookup` stubs and the `--emit-opa` builtins. Its headless features are a runtime builtin catalog (the dynamic equivalent of `opa.gen.go`), cross-layer lint diagnostics, and check/lookup traces.

That understanding lives in `internal/generator` (`AdaptDefinitions`, `DefinitionView`, `PermissionView`, `ParamSpec`), which only code rooted under the `/v2` module path can import (per A1). `pkg/authz` exposes the runtime contract only (no schema model), and the architecture rules forbid it from importing `internal/` (per A2).

The fork is where a public surface over that lowering should live, and how much compatibility it promises. There is one known consumer today and no confirmed external consumers of this module at all (per A3), so any public schema API designed now is designed against a single use case.

## Options

### Option A — Experimental package `studio/` inside the root module

New package `github.com/danhtran94/authzed-codegen/v2/studio`, no separate `go.mod`. It imports `internal/generator` directly (per A1) and exports its own consumer-shaped types (builtin catalog entries, diagnostics, trace steps); `internal` types never appear in its exported API (per A4). Package doc declares it EXPERIMENTAL with no compatibility guarantee, the same convention `golang.org/x/exp` uses (per A5).

- Zero refactor: `AdaptDefinitions` stays the single lowering entry point for both codegen and `studio` (per A6).
- No new dependencies: the headless engine needs only `spicedb` and `opa`, which the root `go.mod` already requires (per A7).
- One release flow: `studio` ships with every `v2.x` tag.
- But: `studio`'s exported API formally sits under the module's v2 SemVer; the experimental notice is a documented convention, not a toolchain-enforced boundary.

### Option B — Nested module at `v2/studio` with its own `go.mod`, versioned v0.x

Same package content as Option A, but released independently as `github.com/danhtran94/authzed-codegen/v2/studio` at v0.x, so breaking changes carry no SemVer obligation.

- v0 semantics are toolchain-honest rather than documented.
- But: every `internal/generator` change that `studio` depends on needs a coordinated root tag plus a `v2/studio/v0.x.y` tag and a `require` bump.
- But: the repository directory a remote `go get` resolves for this module path is unverified for this repo's major-branch layout (per A8).

### Option C — Public schema IR module, generator migrated onto it

A new nested module (e.g. `.../schema`, v0.x) defines a general-purpose IR — definitions, relations, a permission operator tree, caveats with declared types. `internal/generator` is rewritten to consume it; the `*View` types become template adapters over the IR.

- Third parties get a documented schema model, and the IR preserves operator structure, arrow functions and declared caveat types that the current views drop (per A9).
- But: a one-to-two-week generator refactor gated on the fixture round-trip, before any downstream feature exists.
- But: a general-purpose public API designed with exactly one consumer (per A3).
- But: every IR change becomes a two-module release (schema tag, then root bump).

### Option D — Downstream module re-lowers SpiceDB protos itself

The debugger calls `compiler.Compile` and walks `core.NamespaceDefinition` on its own; this repo exposes nothing new.

- No change to this repository.
- But: `flattenAllowedTypes` / `lowerSetOperationChild` semantics (caveat disambiguation, userset sub-relations, wildcard bits, functioned arrows) are reimplemented, and the debugger's catalog drifts from what codegen emits.

## Options Comparison

| Driver                         | A: In-module `studio/` | B: Nested v0 module   | C: Public schema IR   | D: Downstream re-lowering |
|--------------------------------|------------------------|-----------------------|-----------------------|---------------------------|
| Access to lowering             | Direct `internal/`     | Direct `internal/`    | Via public IR         | None — reimplemented      |
| Upfront cost                   | ~0                     | Low                   | 1–2 weeks             | ~0 here, high downstream  |
| Release flow                   | Single tag             | Coordinated tags      | Coordinated tags      | Independent               |
| Compatibility promise          | Documented experimental| v0, toolchain-enforced| v0, toolchain-enforced| None                      |
| Lowering single source         | Yes                    | Yes                   | Yes                   | No                        |
| Reusable by third parties      | No                     | No                    | Yes                   | No                        |

## Decision

Headless engine code for out-of-tree tooling lives in the experimental package `github.com/danhtran94/authzed-codegen/v2/studio` inside the root module, consuming `internal/generator` directly and exporting only its own types.

## Consequences

**Consequences Positive**

- `AdaptDefinitions` remains the only proto-to-model lowering in the codebase; codegen output and `studio`'s builtin catalog derive from the same `[]*DefinitionView` (per A6).
- The root `go.mod` gains no new `require` entries (per A7); UI, desktop-shell and SQL-connector dependencies stay in the downstream module.
- `internal/generator` keeps no external compatibility promise — its types change freely, and only `studio`'s exported types need review on change.
- The `pkg/authz` rule ("must not import `internal/`") stays intact, because `studio/` sits at the module root rather than under `pkg/`.

**Consequences Negative**

- The current views are lossy for a visual debugger (per A9):
  - `PermissionView.Expressions` is a flat list with no `+` / `&` / `-` operator tree, and `PermissionExpr` drops the `.any()` / `.all()` arrow function.
  - `ParamSpec.GoType` is Go syntax for templates, not a SpiceDB type. `ipaddress` surfaces as `*string`; `map<T>` and `list` of non-scalar elements surface as `any`.
  - `studio` features that need the operator tree or the declared caveat types (graph rendering, typed caveat forms) are blocked until the views are extended additively inside `internal/generator` (`docs/scope-permission-operator-tree.md`). `studio` does not read `core.*` protos itself, so `adapter.go` stays the only proto reader (per A2).
- `studio`'s exported API is covered by the module's v2 SemVer in the Go toolchain's eyes (per A5); a downstream consumer that ignores the EXPERIMENTAL notice and pins `v2.x` gets breaking changes inside a minor bump.
- `studio` code is published under this repository's MIT license; only headless, engine-level logic belongs here, and that boundary is enforced by review, not tooling.
- Exit conditions — any one triggers a superseding ADR that moves `studio` to a nested v0 module (Option B) or extracts a public IR (Option C):
  1. A second consumer of the schema model appears (an OpenFGA adapter, or an external tool requesting it).
  2. A `studio` API break is needed that would surprise a `v2.x` user who depends on it.
  3. Fields added to the views only for `studio` (e.g. the operator tree) start constraining what the templates may change, i.e. the views stop being template-shaped.
- Deferring Option C means the views grow additively for a second consumer they were not designed for. When an exit condition fires, the IR extraction is paid then, with `studio` as a second migration target on top of the generator.

## Assumptions

- **A1 [VERIFIED]:** Go's `internal/` rule is checked by import path, not by module boundary: a module at `example.com/repo/v2/studio` may import `example.com/repo/v2/internal/gen`, while `example.com/repo/studio` is rejected with `use of internal package ... not allowed`. Evidence: two-module test under `go.work` run during this ADR's drafting (2026-11-18); rule stated at https://pkg.go.dev/cmd/go#hdr-Internal_Directories.
- **A2 [VERIFIED]:** `pkg/authz` must not import `internal/`, and proto types from `spicedb/pkg/proto/core/v1` appear only in `adapter.go`. Evidence: `.claude/CLAUDE.md`, "Architecture rules".
- **A3 [VERIFIED]:** The module has no confirmed external Go-module consumers. Evidence: `gh repo view` — PUBLIC, 6 stars, 2 forks (2026-11-17); ADR-006 A6 code search (2026-11-13).
- **A4 [EXTERNAL FACT]:** A package outside the `internal/` tree cannot name an internal type, so a downstream module consuming `studio` needs exported types defined in `studio` itself. Evidence: https://pkg.go.dev/cmd/go#hdr-Internal_Directories.
- **A5 [EXTERNAL FACT]:** `golang.org/x/exp` publishes experimental packages under a documented "no compatibility guarantee" convention; the Go toolchain does not enforce that convention. Evidence: https://pkg.go.dev/golang.org/x/exp.
- **A6 [VERIFIED]:** `AdaptDefinitions` is the single lowering entry point; `cmd/authzed-codegen` calls it once. Evidence: `internal/generator/adapter.go:113`, `cmd/authzed-codegen/main.go:50`.
- **A7 [VERIFIED]:** The root module already requires `github.com/authzed/spicedb` and `github.com/open-policy-agent/opa`. Evidence: `go.mod` lines 8–9.
- **A8 [HYPOTHESIS]:** For a nested module at path `.../authzed-codegen/v2/studio` in a repo whose root uses the major-branch convention (no physical `v2/` directory), `go get` resolves the module from a `v2/studio/` subdirectory with tags `v2/studio/vX.Y.Z`. Verification deferred — only local `go.work` resolution was tested (A1); no remote tag was pushed.
- **A9 [VERIFIED]:** The current views drop permission operator structure, the arrow function, and the declared caveat parameter types. Evidence: `internal/generator/adapter.go:101-111` (`PermissionView` / `PermissionExpr` flat fields; `FunctionedTupleToUserset` lowers to a plain `PermExprArrow`, `adapter.go` `lowerSetOperationChild`); `caveatTypeToGo` (`adapter.go:396`) maps `ipaddress` to `*string` and `map` / non-scalar `list` elements to `any`, while `duration` / `timestamp` map to `*time.Duration` / `*time.Time` since AUZ-018. The `ParamSpec` doc comment at `adapter.go:91-95` still claims an `any` fallback for duration and timestamp; it is stale.

## History

| Ticket    | Title                                                                                | Created | Status   |
|-----------|--------------------------------------------------------------------------------------|---------|----------|
| (initial) | Studio Engine Surface: Experimental In-Module Package over a Public Schema IR Module |         | Proposed |
