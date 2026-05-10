# [SPEC-013] OPA Go Builtins Codegen

| Field      | Value                                            |
|------------|--------------------------------------------------|
| Status     | Accepted                                         |
| Created    | 2026-05-10                                       |
| Author     | Danh Tran                                        |
| Implements | docs/scope-opa-go-builtins-codegen.md            |

---

## Overview

This SPEC operationalises the codegen feature that emits per-package `opa.gen.go` files containing OPA custom-builtin registrations for every generated `Check<Perm>` and `Lookup<Perm>Resources` method. The codegen is opt-in via a new `--emit-opa` CLI flag. Builtins follow a uniform shape — 3-arg `(subj string, res string, ctx object) → bool` for Check and 2-arg `(subj string, ctx object) → []string` for Lookup — with caveat context always required at the call site (callers pass `{}` when no caveat applies). The closure body converts the Rego context map to `*structpb.Struct` via `structpb.NewStruct(m)` (per A1), dispatches to the typed Check / Check-with-caveat or Lookup variant, and maps the typed return to OPA's `ast.Term` shape (per A2).

**What this component does:** Add `--emit-opa` flag to `cmd/authzed-codegen/main.go`. Add `OPATemplate []byte` embed in `internal/templates/embed.go` backed by a new `internal/templates/opa.go.tmpl`. Add `Generator.GenerateOPASource(...)` method in `internal/generator/generator.go` that produces one `opa.gen.go` per package after the existing per-namespace generation. Generated file declares `func RegisterSpiceDBBuiltins(r *rego.Rego, engine authz.Engine, ctx context.Context)` plus internal helpers `termToStructpb` and `astValueToInterface`. Per-method closures in `RegisterSpiceDBBuiltins` register one `rego.Function3` per Check method and one `rego.Function2` per Lookup method — alphabetically sorted for deterministic output. Engine errors propagate as `types.NewErr` (Rego eval fails — distinguishable from policy `false` denial). Caveat-context conversion uniformly handles all AUZ-018 caveat parameter types via structpb; SpiceDB's server-side CEL evaluator coerces structpb fields to caveat parameter types at evaluation time (per A3). Round-trip regenerates byte-identically; `--emit-opa` off leaves existing output unchanged.

**What this component does not do:** Add codegen for `Create<Rel>Relations` (write side effects don't fit Rego). Add separate `_with_context` variant builtins (Shape A from scope) — uniform 3-arg / 2-arg shape replaces it. Generate per-caveat typed marshalling code — `structpb.NewStruct(m)` is the universal conversion path. Wire the OPA HTTP server via `runtime.NewRuntime` — caller's domain. Configure decision logs / bundle distribution / Discovery. Surface `LookupResult.Conditional` entries in Rego — Lookup builtins return `Definite` only with a Go doc comment naming the limitation. Add build-tag opt-in inside generated files — `--emit-opa` is the opt-in mechanism. Modify `pkg/authz/` runtime contract — generated bindings call existing `Engine` methods unchanged. Modify `internal/generator/adapter.go` — caveat-aware variant detection already exists per AUZ-007.

---

## Interface Contracts

### CLI surface — `cmd/authzed-codegen/main.go`

New flag added to the existing `flag.NewFlagSet`:

```go
emitOPA := fs.Bool("emit-opa", false, "emit opa.gen.go per package with OPA custom-builtin registrations for Check/Lookup methods (adds opa/rego runtime dep on consumers)")
```

After the existing per-namespace generation loop (`Generator.GenerateObjectSource(...)`), invoke:

```go
if *emitOPA {
    for _, pkg := range generatedPackages {
        if err := g.GenerateOPASource(string(templates.OPATemplate), pkg); err != nil {
            return fmt.Errorf("generate OPA source for %s: %w", pkg.Namespace, err)
        }
    }
}
```

Where `generatedPackages` is the existing per-package collection — caveat awareness travels in the same `*DefinitionView` already used for the per-namespace generation.

### Template embed — `internal/templates/embed.go`

Add a sibling embed declaration to the existing `ObjectTemplate`:

```go
//go:embed opa.go.tmpl
var OPATemplate []byte
```

### Generator method — `internal/generator/generator.go`

```go
// GenerateOPASource emits opa.gen.go for the given package. The package
// must already be adapted (DefinitionViews resolved) by AdaptDefinitions
// + GenerateObjectSource. The generated file imports the package's own
// types (User, Folder, etc.) and the authz.Engine interface; it does not
// duplicate type definitions.
//
// The output is sorted by (Resource, Permission) name pairs for
// deterministic round-trip behavior (per C1).
func (g *Generator) GenerateOPASource(templateText string, pkg *PackageView) error
```

Where `*PackageView` is the existing per-package aggregation already used by the per-namespace generation. No new resolver work; the template walks the existing data.

### Generated file — `<package>/opa.gen.go`

Package-scoped exports:

```go
// Package <pkg> generated by authzed-codegen — DO NOT EDIT.
package <pkg>

import (
    "context"
    "fmt"

    "github.com/open-policy-agent/opa/ast"
    "github.com/open-policy-agent/opa/rego"
    "github.com/open-policy-agent/opa/types"
    "google.golang.org/protobuf/types/known/structpb"

    "<module>/pkg/authz"
)

// RegisterSpiceDBBuiltins registers SpiceDB-backed Rego builtins on r.
//
// Builtin signatures:
//   <pkg>.check_<resource>_<perm>(subject_id, resource_id, caveat_context) -> bool
//   <pkg>.lookup_<resource>_<perm>_resources(subject_id, caveat_context) -> []string
//
// Pass {} for caveat_context when no caveat applies. Engine errors
// propagate as types.NewErr (Rego eval fails) — distinct from policy
// denial. Lookup builtins return LookupResult.Definite only;
// Conditional entries are not surfaced to Rego.
//
// The closure captures engine and ctx by reference. Recreate the
// registration per request OR commit to a long-lived ctx with
// cancellation. The codegen does not pick the pattern; the SPEC's
// Sequence section documents both.
func RegisterSpiceDBBuiltins(r *rego.Rego, engine authz.Engine, ctx context.Context)
```

Internal helpers (unexported, file-local):

```go
// termToStructpb converts an OPA ast.Term holding an Object to a
// *structpb.Struct. Returns (nil, nil) for empty objects — the caller
// dispatches to the no-caveat Engine path when this is nil. Returns a
// non-nil error if the term is not an Object or contains values not
// representable in structpb (e.g. non-string keys, sets).
func termToStructpb(t *ast.Term) (*structpb.Struct, error)

// astValueToInterface walks an ast.Value and produces the Go any
// equivalent suitable for structpb.NewStruct. Coverage:
//   ast.Null    -> nil
//   ast.Boolean -> bool
//   ast.Number  -> int64 if integral; float64 otherwise
//   ast.String  -> string
//   *ast.Array  -> []any (recursive)
//   ast.Object  -> map[string]any (recursive; non-string keys dropped)
//   ast.Set     -> error (sets cannot cross to structpb)
func astValueToInterface(v ast.Value) (any, error)
```

Per-method closure shape (one per Check method; one per Lookup method):

```go
// Check<Resource><Permission> binding — registered alphabetically per (Resource, Permission)
rego.Function3(
    &rego.Function{
        Name: "<pkg>.check_<resource>_<perm>",
        Decl: types.NewFunction(
            types.Args(
                types.S,
                types.S,
                types.NewObject(nil, types.NewDynamicProperty(types.S, types.A)),
            ),
            types.B,
        ),
    },
    func(_ rego.BuiltinContext, subjTerm, resTerm, ctxTerm *ast.Term) (*ast.Term, error) {
        subjID, ok := subjTerm.Value.(ast.String)
        if !ok {
            return nil, types.NewErr("expected subject_id string, got %T", subjTerm.Value)
        }
        resID, ok := resTerm.Value.(ast.String)
        if !ok {
            return nil, types.NewErr("expected resource_id string, got %T", resTerm.Value)
        }
        caveatCtx, err := termToStructpb(ctxTerm)
        if err != nil {
            return nil, types.NewErr("invalid caveat_context: %v", err)
        }

        var granted bool
        if caveatCtx == nil {
            granted, err = engine.Check<Resource><Permission>(ctx,
                <SubjectType>{ID: string(subjID)},
                <ResourceType>{ID: string(resID)})
        } else {
            granted, err = engine.Check<Resource><Permission>WithCaveat(ctx,
                <SubjectType>{ID: string(subjID)},
                <ResourceType>{ID: string(resID)},
                caveatCtx)
        }
        if err != nil {
            return nil, types.NewErr("engine.Check<Resource><Permission>: %v", err)
        }
        return ast.BooleanTerm(granted), nil
    },
)(r)
```

Lookup closure shape:

```go
rego.Function2(
    &rego.Function{
        Name: "<pkg>.lookup_<resource>_<perm>_resources",
        Decl: types.NewFunction(
            types.Args(
                types.S,
                types.NewObject(nil, types.NewDynamicProperty(types.S, types.A)),
            ),
            types.NewArray(nil, types.S),
        ),
    },
    func(_ rego.BuiltinContext, subjTerm, ctxTerm *ast.Term) (*ast.Term, error) {
        subjID, ok := subjTerm.Value.(ast.String)
        if !ok {
            return nil, types.NewErr("expected subject_id string, got %T", subjTerm.Value)
        }
        caveatCtx, err := termToStructpb(ctxTerm)
        if err != nil {
            return nil, types.NewErr("invalid caveat_context: %v", err)
        }

        var result authz.LookupResult
        if caveatCtx == nil {
            result, err = engine.Lookup<Resource><Permission>Resources(ctx,
                <SubjectType>{ID: string(subjID)})
        } else {
            result, err = engine.Lookup<Resource><Permission>ResourcesWithCaveat(ctx,
                <SubjectType>{ID: string(subjID)},
                caveatCtx)
        }
        if err != nil {
            return nil, types.NewErr("engine.Lookup<Resource><Permission>Resources: %v", err)
        }

        // result.Definite only; result.Conditional dropped per scope Out of Scope item 8
        ids := make([]*ast.Term, len(result.Definite))
        for i, id := range result.Definite {
            ids[i] = ast.StringTerm(string(id))
        }
        return ast.ArrayTerm(ids...), nil
    },
)(r)
```

### Adapter — `internal/generator/adapter.go`

**No changes.** Caveat-applicability data is already preserved on each `*DefinitionView` per AUZ-007 / AUZ-018. The template determines whether to emit the `WithCaveat` dispatch branch by walking the existing view.

### Runtime contract — `pkg/authz/`

**No changes.** Generated bindings call `engine.Check<X>` / `engine.Check<X>WithCaveat` / `engine.Lookup<X>Resources` / `engine.Lookup<X>ResourcesWithCaveat` through the existing typed interface (per A4).

---

## Data Shapes

### Caveat-context type mapping

The caveat-context object passed from Rego maps through structpb to SpiceDB's CEL caveat parameter types as follows. SpiceDB's CEL evaluator handles per-type coercion server-side (per A3); the codegen does not generate per-caveat marshalling.

| Rego value | OPA `ast.Value` | Go intermediate | structpb stored as | SpiceDB CEL caveat param types accepted |
|---|---|---|---|---|
| `true` / `false` | `ast.Boolean` | `bool` | `BoolValue` | `bool` |
| Integer `42` | `ast.Number` | `int64` | `NumberValue` (lossless for ≤ 2^53) | `int`, `uint`, `double` |
| Float `3.14` | `ast.Number` | `float64` | `NumberValue` | `double`, `int` (truncates), `uint` (truncates) |
| `"hello"` | `ast.String` | `string` | `StringValue` | `string`, `bytes` (base64-decoded), `duration`, `timestamp`, `ipaddress` |
| `null` | `ast.Null` | `nil` | `NullValue` | any nullable; CEL caveat param treated as absent |
| `[1, 2, 3]` | `*ast.Array` | `[]any` | `ListValue` | `list<T>` (CEL coerces elements per T) |
| `{"k": "v"}` | `ast.Object` | `map[string]any` | `Struct` (recursive) | `map<K, V>` |
| Set `{1, 2}` | `ast.Set` | error | — | unrepresentable; binding returns `types.NewErr` |

Caveat parameter types from AUZ-018 — `duration`, `timestamp`, `ipaddress` — accept Rego strings: `"1h30m"`, `"2026-05-10T14:30:00Z"`, `"10.0.0.0/24"` respectively. SpiceDB's CEL evaluator parses these at evaluation time. The codegen emits no special handling for these types; structpb stores them as `StringValue` and SpiceDB does the rest.

### LookupResult extraction

```
authz.LookupResult {
    Definite    []ID                       ← extracted as []string
    Conditional []LookupConditionalEntry   ← dropped (per C5)
}
```

The Lookup builtin returns `Definite` as a Rego `[]string`. Conditional entries are dropped silently with a Go doc comment naming the limitation on each Lookup binding.

---

## Sequence

Per-request lifecycle of one Rego eval that invokes a generated builtin:

```
Caller goroutine
   │
   ▼
   r := rego.New(rego.Query(...), rego.Module(...))
   <pkg>.RegisterSpiceDBBuiltins(r, engine, ctx)   ◀ closure captures engine + ctx
   prepared, _ := r.PrepareForEval(ctx)
   result, _ := prepared.Eval(ctx, rego.EvalInput(...))
   │
   ▼
   Rego compiler resolves builtin names against registered set
   │
   ▼
   Rego runtime invokes registered closure for `<pkg>.check_X_Y(...)`
   │   args: subjTerm (ast.Term), resTerm (ast.Term), ctxTerm (ast.Term)
   ▼
   Closure:
     1. subjTerm.Value.(ast.String) → string subject_id
     2. resTerm.Value.(ast.String)  → string resource_id
     3. termToStructpb(ctxTerm)     → *structpb.Struct or nil
   │
   ▼
   Dispatch:
     caveatCtx == nil → engine.Check<X><Y>(captured_ctx, ...) ──┐
     caveatCtx != nil → engine.Check<X><Y>WithCaveat(...)     ──┤
                                                                │
   ▼                                                            │
   Engine performs gRPC Check via existing client ◀─────────────┘
   │
   ▼
   Engine returns (bool, error)
   │
   ▼
   Closure mapping:
     err != nil → return nil, types.NewErr(...)  ◀ Rego eval fails
     err == nil → return ast.BooleanTerm(granted), nil
   │
   ▼
   Rego runtime continues policy evaluation with the bool result
```

Two valid context-propagation patterns the caller chooses:

**Pattern P1 — Per-request registration** (default for the e2e test, SC9):
```
Caller request → new rego.Rego → RegisterSpiceDBBuiltins(r, engine, requestCtx) → Eval → discard
```
Advantage: requestCtx is fresh; deadlines and tracing propagate.
Cost: registration overhead on every request (~100µs for ~30 builtins per package).

**Pattern P2 — Long-lived registration**:
```
Server start → rego.Rego → RegisterSpiceDBBuiltins(r, engine, serverCtx) → reuse for many Eval calls
```
Advantage: amortized registration cost across requests.
Cost: serverCtx must outlive every Eval; cancellation propagates to all in-flight Evals.

---

## Errors

| Error class | Trigger | Caller recovery |
|---|---|---|
| `types.NewErr("expected subject_id string, got %T")` | Rego call passes non-string for subject_id | Caller fixes Rego policy — pass a string |
| `types.NewErr("expected resource_id string, got %T")` | Rego call passes non-string for resource_id (Check builtins only) | Caller fixes Rego policy |
| `types.NewErr("invalid caveat_context: <reason>")` | `termToStructpb` returns error — non-Object term, set value, non-string key | Caller fixes Rego policy — pass an object literal `{}` |
| `types.NewErr("engine.Check<X>: <wrapped>")` | Engine call fails — gRPC error, SpiceDB error, context cancellation | Caller distinguishes from policy-denial-as-bool by checking eval error; retry / log / fail open per app policy |
| `types.NewErr("engine.Lookup<X>Resources: <wrapped>")` | Engine Lookup call fails | Same as Check |

Engine errors do **not** silently convert to `false` (per C4). A Rego policy reading the builtin's result sees either a bool result OR the eval fails with a typed error. This preserves the distinction between "policy correctly denies" and "system couldn't evaluate the policy."

---

## Constraints

- **C1 — Deterministic output ordering.** Builtins are registered in the generated `RegisterSpiceDBBuiltins` body sorted alphabetically by `(ResourceTypeName, PermissionName)` for Check methods, then sorted alphabetically by the same pair for Lookup methods. Round-trip regression (scope SC7) requires byte-identical regeneration; non-deterministic ordering breaks this immediately.

- **C2 — Public-API-only OPA imports.** Generated `opa.gen.go` and the codegen template import only from `github.com/open-policy-agent/opa/{ast,rego,types}` and `google.golang.org/protobuf/types/known/structpb`. No `internal/` package import — those are not stable API surface. SpiceDB upgrades that change `internal/` are not contract-affecting (per A5).

- **C3 — Uniform structpb conversion.** All caveat-context conversion goes through `termToStructpb` + `structpb.NewStruct`. The codegen does not emit per-caveat-type marshalling (e.g. no special path for duration / timestamp / ipaddress). SpiceDB's CEL evaluator handles type coercion server-side per A3. Implication: a Rego policy passing the wrong shape (e.g. integer when string expected) fails at SpiceDB's caveat eval, not at the binding boundary. The error surfaces as `types.NewErr("engine.Check<X>: caveat evaluation: ...")`.

- **C4 — Engine errors propagate as `types.NewErr`.** A failing Engine call never returns `ast.BooleanTerm(false)` from the closure. The Rego eval fails; callers reading the eval result must check for runtime errors separately from policy result. Documented in `RegisterSpiceDBBuiltins` Go doc.

- **C5 — Lookup returns `Definite` only.** Each Lookup binding emits a Go doc comment stating "returns LookupResult.Definite as []string; Conditional entries are dropped — caveat-aware Lookup with conditional surfacing is out of scope per scope-opa-go-builtins-codegen.md Out of Scope item 8."

- **C6 — OPA version pinned in `go.mod`.** The repo's `go.mod` pins `github.com/open-policy-agent/opa` at a specific version (named in the implementation job's Discoveries). Round-trip determinism (C1) depends on stable OPA AST/term layout; version drift surfaces as a dependency-upgrade PR, not a silent codegen diff.

- **C7 — `--emit-opa` is the only opt-in switch.** Without the flag, `cmd/authzed-codegen` produces the same output as the current commit (scope SC8). No build tags inside generated `.go` files. No environment variables. No config file. The flag is the contract for "I want OPA bindings."

---

## Unresolved Questions

(none)

Two design choices were considered and resolved in this SPEC:

- **OPA version target** — resolved by C6 (pinned in `go.mod`; specific version recorded in the implementation job's Discoveries; version drift acceptable as long as C2's listed APIs remain stable).
- **Empty-object detection in `termToStructpb`** — resolved by returning `(nil, nil)` only for `obj.Len() == 0`. Objects containing null values (`{"k": null}`) are non-empty and pass through to structpb; SpiceDB's CEL evaluator interprets null as "field absent" — filtering at the binding boundary would mask caller intent.

---

## Assumptions

- **A1 [VERIFIED]:** `google.golang.org/protobuf/types/known/structpb.NewStruct(map[string]any) (*Struct, error)` accepts `bool`, `int*`, `uint*`, `float*`, `string`, `[]byte`, `[]any`, `map[string]any`, `nil`. Evidence: stdlib documentation at https://pkg.go.dev/google.golang.org/protobuf/types/known/structpb#NewStruct; existing usage in `pkg/authz/spicedb/crud.go` per AUZ-007 confirms.

- **A2 [EXTERNAL FACT]:** OPA's `github.com/open-policy-agent/opa/rego.Function3` (and `Function1`, `Function2`) registration takes a `*rego.Function` definition with `Decl *types.Function` and a closure `func(rego.BuiltinContext, *ast.Term, *ast.Term, *ast.Term) (*ast.Term, error)`. Evidence: package documentation at https://pkg.go.dev/github.com/open-policy-agent/opa/rego.

- **A3 [VERIFIED]:** SpiceDB's CEL caveat evaluator coerces structpb fields to caveat parameter types at evaluation time — duration / timestamp / ipaddress accept string forms; numeric types coerce between int / uint / double. Evidence: `pkg/caveats/types/{basic,ipaddress,custom}.go` in `github.com/authzed/spicedb@v1.52.0` register CEL converters per type; AUZ-018 caveat parameter expansion exercises duration / timestamp / ipaddress fixtures end-to-end.

- **A4 [VERIFIED]:** `pkg/authz/Engine` exposes `Check<X>(ctx, subj, res) (bool, error)`, `Check<X>WithCaveat(ctx, subj, res, *structpb.Struct) (bool, error)`, `Lookup<X>Resources(ctx, subj) (LookupResult, error)`, `Lookup<X>ResourcesWithCaveat(ctx, subj, *structpb.Struct) (LookupResult, error)` for every generated Resource × Permission pair. Evidence: existing generated `<entity>.gen.go` files in `example/authzed/{bookingsvc,menusvc,extsvc}/` after AUZ-007 (caveat-aware variants) and AUZ-013 (LookupResult shape).

- **A5 [HYPOTHESIS]:** OPA library upgrades within the v1.x major version preserve the public API listed in C2 (`ast`, `rego`, `types` packages — specific symbols enumerated). Verification deferred — confirm at implementation time by inspecting the OPA changelog between the pinned version and the latest. If a public API breaks, C2 lists the affected symbols; the SPEC needs revision, not the upgrade.

- **A6 [VERIFIED]:** `internal/templates/embed.go` declares each embedded template explicitly via `//go:embed <name>.tmpl` + a sibling `var <Name>Template []byte`. Evidence: `ObjectTemplate` and `SchemaTemplate` follow this pattern; `OPATemplate` matches without restructuring the embed setup.

## History

<!-- managed by `harness history-update` — do not hand-edit -->
