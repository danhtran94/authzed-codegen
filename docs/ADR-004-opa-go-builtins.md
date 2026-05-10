# [ADR-004] First Tier 2 Codegen Extension: OPA Go Builtins over CEL Bindings and Rego HTTP Helpers

| Field      | Value                       |
|------------|-----------------------------|
| Status     | Accepted                    |
| Date       | 2026-05-10                  |
| Deciders   | Danh Tran                   |
| Scope      | authzed-codegen generator + Tier 2 integration extension |
| Depends on | RFC-001                     |

---

## Context

RFC-001 defined three tiers for policy-engine integration. Tier 1 (embedded SpiceDB) ships unchanged. Tier 2 catalogues three codegen-extension candidates — CEL bindings, OPA Rego helpers (standalone OPA via HTTP), and OPA Go builtins (embedded OPA) — gated by R2 ("scheduled only when concrete user demand exists").

The project owner has selected one Tier 2 pattern as the short-term codegen direction. R2 qualifies under: (a) deployment shape — Go-first stack with embedded policy engine; (b) method count — currently ~30 generated Check / Lookup methods across `bookingsvc`, `menusvc`, `extsvc`; (c) integration target's runtime — `github.com/open-policy-agent/opa/rego` at the latest stable version.

This ADR picks among the three Tier 2 options. The decision is which one to build first — patterns not chosen remain available for future R2 qualification under RFC-001.

The discriminating drivers descend directly from RFC-001's analysis:
1. Does the pattern support multi-rule composition (the only OPA capability that justifies OPA over CEL — per A2)?
2. Does the pattern reuse the existing `authz.Engine` gRPC connection without forcing HTTP-only access (per A5)?
3. Does the pattern fit a single-binary deployment without requiring a separate OPA process / sidecar (RFC-001 § Single-binary all-embedded)?

## Options

### Option A — Pattern 2: Embedded CEL bindings

The generator emits a `cel.gen.go` per package containing `cel.Function` registrations. The caller embeds `cel-go`, registers the bindings on a CEL environment, and evaluates expressions in user code that invoke generated Check / Lookup methods.

```go
env, _ := cel.NewEnv(bookingsvc.CELFunctions(engine, ctx)...)
prg, _ := env.Program(ast)
result, _ := prg.Eval(map[string]any{"user_id": "alice", "doc_id": "doc-42"})
```

CEL is the same engine SpiceDB caveats use server-side, so the language is familiar to existing users (per A1). Evaluation is single-expression; multi-rule composition does not exist in CEL. Each binding is ~13 lines per Check method. Adds `cel-go` as a runtime dependency on user code.

### Option B — Pattern 3: OPA Rego helpers (standalone OPA via HTTP)

The generator emits `.rego` modules per package that use Rego's `http.send` to call SpiceDB's HTTP API. The caller runs OPA as a separate process / sidecar, loads the generated Rego as part of a bundle, and writes user-facing Rego policies that import the helpers.

```rego
package authz
import data.spicedb.bookingsvc

allow {
    bookingsvc.check_doc_view(input.user.id, input.resource.id)
}
```

No new Go runtime dependency on user code. Each helper is ~15-25 lines per Check method (HTTP request body, headers, response parsing). Calls go through SpiceDB's HTTP gateway, not gRPC — adds an HTTP hop and bypasses the existing `authz.Engine` gRPC connection.

### Option C — Pattern 4: OPA Go builtins (embedded OPA)

The generator emits an `opa.gen.go` per package containing Go custom-builtin registrations. The caller embeds `github.com/open-policy-agent/opa/rego`, registers the builtins, and evaluates Rego policies in-process. The OPA HTTP server is exposable alongside via `runtime.NewRuntime` for polyglot clients (per A3).

```go
r := rego.New(rego.Query("data.authz.allow"), rego.Module("policy.rego", policy))
bookingsvc.RegisterSpiceDBBuiltins(r, engine, ctx)
result, _ := r.Eval(ctx)
```

Reuses the existing `authz.Engine` gRPC connection — no HTTP hop. Adds `opa/rego` as a runtime dependency on user code. Each registration is ~13 lines per Check method. The caller gets full Rego features (multi-rule composition, bundle distribution, decision logs) via OPA's library APIs (per A2).

### Option D — Build all three concurrently

Ship CEL bindings, Rego helpers, and OPA Go builtins in parallel jobs. Triples the codegen surface area in one cycle. Patterns 2 and 3 lack the R2 qualification that Pattern 4 has. Bundling them alongside delays Pattern 4's ship by the time needed to design CEL and Rego template surfaces — without addressing a confirmed need for either.

## Options Comparison

| Driver | A: CEL bindings | B: Rego/HTTP | C: OPA Go builtins | D: All three |
|--------|-----------------|--------------|--------------------|--------------|
| Multi-rule composition | ✗ single expression | ✓ | ✓ | mixed |
| Reuses authz.Engine gRPC | ✓ | ✗ HTTP only | ✓ | mixed |
| Single-binary fit | ✓ | ✗ requires sidecar | ✓ | mixed |
| Polyglot HTTP API option | ✗ | ✓ native | ✓ via runtime.NewRuntime | ✓ |
| OPA tooling (bundles, logs) | ✗ | ✓ | ✓ | ✓ |
| New runtime dep | cel-go | none | opa/rego | all three |
| Boilerplate per Check | ~13 lines | ~15-25 lines | ~13 lines | sum |
| Codegen scope | New CEL template | New Rego template | New OPA template | Three templates |

## Decision

The first Tier 2 codegen extension is Pattern 4 (OPA Go builtins) — the generator emits per-package OPA custom-builtin registrations that wrap each Check and Lookup method for invocation from Rego policies.

## Consequences

**Consequences Positive**

- Pattern 4 is the only option that simultaneously delivers multi-rule composition, reuse of the existing `authz.Engine` gRPC connection, and single-binary deployment fit. Patterns A and B each lack one of those three. This combination is what RFC-001 §Pattern 4 names as the structural advantage of the embedded OPA path.
- The HTTP API stays optional. Users wanting polyglot access wire `runtime.NewRuntime` and start the OPA HTTP server; Go-only callers skip it (per A3). Pattern B forces HTTP from day one.
- The codegen output reuses the existing `authz.Engine` interface unchanged (per A5). The generated registrations call `engine.Check<X>` directly; `pkg/authz/` gains no new runtime contract.
- The generated `opa.gen.go` follows the same per-package layout as today's generated files. The template extends the existing `internal/templates/` structure — same Go-output target, no new output format to maintain.

**Consequences Negative**

- Pattern 4 adds `github.com/open-policy-agent/opa` as a runtime dependency on every consumer that imports the generated package. The existing dependency closure for users is `authzed-go` plus standard library; OPA's transitive closure is substantial (decision-log plugins, bundle plugins, status plugins, HTTP server). Users who do not want OPA cannot import the generated package without pulling the whole graph. The mitigation — emitting OPA bindings under a build tag so users opt in at build time — is the standard Go pattern for optional codegen, but it is yet another knob in the generator's CLI surface (per A4).
- Context propagation across the OPA / SpiceDB boundary is awkward. CEL and Rego are designed for synchronous in-memory evaluation against an input map; SpiceDB calls need `context.Context` for deadlines and cancellation. The chosen shape — closure-capture `ctx` at registration time — forces the caller to recreate the function set per request OR commit to a long-lived ctx. Neither pattern is wrong; neither is friction-free; the codegen cannot pick for the caller.
- Latency is higher than CEL-only for trivial cases. A Rego policy evaluating one Check via the OPA Go builtin path pays Rego evaluation overhead (~1-3ms) on top of the gRPC call. CEL bindings pay only the gRPC call. For latency-sensitive paths with single-expression rules, Pattern A would be the right tool — but Pattern A is not what was asked for under R2.
- Rego is unfamiliar to teams that learned CEL through SpiceDB caveats. The codegen project's existing example services include CEL caveat fixtures; users adopting Pattern 4 must learn Rego on top of the CEL knowledge they already have. RFC-001 R3 requires a worked Rego example using one of the existing example services in the README documentation.
- Patterns A and B remain unbuilt. A user later needing CEL bindings (single-expression eval, no Rego runtime) or Rego helpers (sidecar OPA, polyglot stack) hand-rolls the wrappers per RFC-001's documented patterns until separate R2 qualification re-opens those Tier 2 options. The deferral is deliberate — Option D was rejected explicitly — and re-opening either requires the same R2 qualification process Pattern 4 went through.

## Assumptions

- **A1 [VERIFIED]:** CEL is the expression language SpiceDB caveats use server-side; the same `cel-go` library is embeddable in Go for client-side evaluation. Evidence: `internal/generator/adapter.go:caveatTypeToGo` already type-maps caveat parameters from CEL types (per AUZ-018); RFC-001 A3 cites this.
- **A2 [VERIFIED]:** Embedded OPA via `github.com/open-policy-agent/opa/rego` provides the same Rego runtime as the standalone `opa run` binary, including bundle plugins, decision-log plugins, and Discovery. Evidence: 2026-05-10 architectural exploration that produced RFC-001; verified by inspecting the public exports of `github.com/open-policy-agent/opa/runtime` (`NewRuntime`, `Runtime.StartServer`, `Params.Addrs`, plus the plugin interfaces in `github.com/open-policy-agent/opa/plugins`).
- **A3 [EXTERNAL FACT]:** OPA exposes its HTTP server from embedded mode via `runtime.NewRuntime` — same code path as `opa run --server`. Evidence: `github.com/open-policy-agent/opa/runtime` package; RFC-001 A2 cites this.
- **A4 [HYPOTHESIS]:** Users running Go-first stacks with embedded OPA tolerate the OPA dependency closure on their consumer binary, especially when the alternative is operating a separate OPA process. Verification: the project owner is the qualifying user per RFC-001 R2 and selected Pattern 4 with awareness of the dependency cost. If a future user reports build / binary-size pain from the OPA closure, the build-tag mitigation (per Consequences Negative item 1) is the trigger to ship.
- **A5 [VERIFIED]:** The existing `authz.Engine` interface is connection-target-agnostic — works identically against embedded and remote SpiceDB. Evidence: RFC-001 R1; `pkg/authz/spicedb/Engine` constructor accepts a `*authzed.Client` regardless of target.

## History

<!-- managed by `harness history-update` — do not hand-edit -->
