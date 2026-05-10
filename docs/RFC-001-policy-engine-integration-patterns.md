# [RFC-001] Policy engine integration patterns

| Field      | Value      |
|------------|------------|
| Status     | Accepted   |
| Version    | v1         |
| Impact     | MEDIUM     |
| Priority   | LOW        |
| Created    | 2026-05-10 |
| Author     | danhtran94 |
| Depends on | —          |
| Blocks     | —          |

---

## TL;DR

`authzed-codegen` emits typed Go bindings around SpiceDB's relationship graph, but users deploying these bindings often want to integrate with policy engines — embedded CEL evaluators, OPA Rego runtimes, or embedded SpiceDB itself — and the codegen's role in each integration is currently unwritten. This RFC catalogues four integration patterns, names which work with the current generator unchanged versus which would require codegen extensions, and defines the criteria a codegen extension must meet before being scheduled. The position is conservative: the current codegen needs no changes for embedded SpiceDB or external-policy-engine deployments, and codegen support for CEL / OPA bindings is gated on confirmed user demand. Future ADRs proposing CEL or OPA codegen features derive their justification from the rules in this RFC.

## Context

The codegen project's current scope (per `.claude/CLAUDE.md`) is to read a SpiceDB schema and emit typed Go stubs for `Check<Permission>`, `Lookup<Permission>Resources`, and `Create<Relation>Relations`. Generated code wraps `pkg/authz/` runtime; users connect to a SpiceDB instance via gRPC.

Recent architectural exploration (2026-05-10 session, this RFC's source) surfaced four deployment shapes users may want:

### Pattern 1 — Embedded SpiceDB

SpiceDB runs in-process as a Go library. The `authzed/spicedb` project supports `MemDB` / sqlite / postgres / spanner / mysql datastores; the in-process gRPC server is configured with `WithGRPCAuthFunc` disabled because both ends share the process. Use cases: single-tenant on-prem, CLI tools, dev environments, edge appliances.

### Pattern 2 — Embedded CEL evaluator with SpiceDB-callable bindings

User code embeds `cel-go`. Codegen would emit `cel.Function` bindings that wrap each `Check` / `Lookup` method, allowing CEL expressions in user code to invoke SpiceDB checks as primitives. Use cases: customer-defined rule languages, gateway authz with mixed JWT + SpiceDB conditions.

### Pattern 3 — OPA Rego helpers (standalone OPA)

OPA runs as a separate process / sidecar. Codegen would emit `.rego` modules that use `http.send` to call SpiceDB's HTTP API. Use cases: polyglot stacks, compliance teams contributing Rego bundles, Envoy / k8s sidecar deployments.

### Pattern 4 — OPA Go builtins (embedded OPA)

User imports `github.com/open-policy-agent/opa/rego`. Codegen would emit Go custom-builtin registrations that call the typed `authz.Engine` through the existing gRPC connection. The OPA HTTP server can be exposed alongside in-process eval via `runtime.NewRuntime` — same Rego runtime as the standalone binary, accessed via Go API. Use cases: Go-first shops wanting full OPA features (bundles, decision logs, multi-rule composition) without operating a separate OPA service.

### What works today vs what would need codegen work

Currently no codegen support exists for patterns 2-4. Pattern 1 works without codegen changes — the generated `authz.Engine` calls the SpiceDB gRPC client identically whether the server is in-process or remote.

Evidence the architectural decision space matters: SpiceDB's `pkg/cmd` package exposes embed entry points specifically for Go integration (per A1); OPA's `runtime.NewRuntime` Go API explicitly supports HTTP server exposure from embedded mode (per A2); CEL is the same language SpiceDB caveats use server-side, making client-side CEL bindings semantically aligned with caveat evaluation (per A3). Each integration has been requested or referenced in upstream documentation and example repos.

The decision space splits into two questions:

- Which patterns work with the codegen unchanged? (Answer: pattern 1.)
- Which patterns would require codegen extensions, and under what conditions are those extensions justified?

This RFC answers the second question by stating criteria. It does not commit the project to building any specific extension.

## Proposal

The codegen project supports policy-engine integration patterns at three tiers.

### Tier 1 — No codegen changes required

Pattern 1 (embedded SpiceDB) requires no codegen extensions. The generated `authz.Engine` connects to a `*authzed.Client`, which works identically against in-process gRPC and remote gRPC. The codegen project ships **documentation** of the embedded SpiceDB pattern in `README.md` so users can adopt it without reading SpiceDB's example repo.

### Tier 2 — Codegen extensions gated on confirmed demand

Patterns 2-4 require codegen-emitted output that doesn't exist today. Each is mechanical wrapping of the current Check / Lookup / Create methods, but the operational cost is non-trivial:

| Pattern | Output format | New runtime dep on user code | Estimated boilerplate per method |
|---------|---------------|------------------------------|----------------------------------|
| 2 — CEL bindings | `cel.gen.go` Go file | `cel-go` | ~13 lines |
| 3 — Rego helpers | `.rego` text file | none (OPA pulls bundle) | ~15-25 lines |
| 4 — OPA Go builtins | `opa.gen.go` Go file | `opa/rego` | ~13 lines |

Codegen extension for any Tier 2 pattern is scheduled only when at least one user reports a concrete deployment requiring it. Until then, the project ships **documentation patterns** in `README.md` showing how users hand-roll the bindings, with worked examples against `bookingsvc` / `menusvc` / `extsvc`.

### Tier 3 — Out of scope (with embedded-only nuance)

The codegen project does not extend SpiceDB's caveat CEL environment with custom types or functions. The capability splits along deployment shape (per A5):

- **Deployed SpiceDB binary (remote gRPC)** — no mechanism exists. The binary uses the package-level `types.Default.TypeSet`; no flag / config / plugin path injects custom types. Forking SpiceDB is the only path.
- **Embedded SpiceDB (Pattern 1)** — public APIs (`NewTypeSet`, `RegisterCustomType`, `RegisterMethodOnDefinedType`, `RegisterCustomCELOptions`, `NewEnvironmentWithTypeSet`) make custom CEL type / function registration possible. The codegen project does not currently emit registration code. Adding such support is a potential future extension under separate R2 qualification.

### Single-binary all-embedded deployments

The all-embedded deployment shape (app + embedded SpiceDB + embedded OPA + codegen output, one Go binary) is a **deployment composition**, not a codegen feature. Users compose Tier 1 (embedded SpiceDB, no codegen change) with their choice of Tier 2 pattern (or no policy engine at all). The codegen output is deployment-shape-agnostic.

The migration story (start embedded, extract to distributed services later) preserves the codegen output across both deployment shapes — same generated `Engine`, swap connection target.

## Implementation

N/A at the RFC level — this RFC sets criteria. It does not itself produce code.

Triggered follow-up work:

1. **README documentation update** covering Tier 1 (embedded SpiceDB pattern with a worked example) and the documented Tier 2 hand-roll patterns (CEL bindings, OPA Rego helpers, OPA Go builtins). Scope note + job to follow.
2. **ADR-004 — Pattern 4 (OPA Go builtins) scheduled as first Tier 2 codegen extension.** As of 2026-05-10 the project owner has selected Pattern 4 as the short-term codegen direction. This qualifies under R2 — the project owner is the demand signal at this project's scale. The follow-up ADR records the decision among the three Tier 2 patterns; a SPEC defines the codegen contract; a job implements. Patterns 2 (CEL bindings) and 3 (OPA Rego helpers) remain at "documented hand-roll" until separate R2 qualification.
3. **No work scheduled for patterns 2 and 3.** R2 still applies; these stay in the documented-hand-roll tier until a separate qualifying demand surfaces.

## Rules

R1 — The generated `authz.Engine` works identically against embedded and remote SpiceDB. No codegen changes are required for Tier 1 deployments. Enforced by `pkg/authz/spicedb/Engine` connecting via the `*authzed.Client` interface, which is connection-target-agnostic.

R2 — Codegen extensions for Tier 2 patterns are scheduled only when a concrete user demand exists. A demand qualifies when it states (a) the deployment shape, (b) the method count to be wrapped, and (c) the integration target's runtime version (`cel-go` or `opa` semantic version). Policy-only.

R3 — Documentation of integration patterns (Tier 1 and Tier 2) lives in `README.md`. Each pattern's documentation includes a worked example using one of the existing example services (`bookingsvc`, `menusvc`, `extsvc`). Enforced by reviewer at PR time.

R4a — For deployed SpiceDB binaries, no external mechanism exists to extend the caveat CEL environment with custom types or functions. The codegen project does not attempt server-side CEL extension targeting deployed SpiceDB. Enforced by SpiceDB's binary using package-level `types.Default.TypeSet` with no plugin / config / flag injection path (per A5).

R4b — For embedded SpiceDB (Pattern 1), custom CEL type / function registration is technically possible via SpiceDB's public Go APIs (`pkg/caveats/types.RegisterCustomType`, `RegisterMethodOnDefinedType`, `RegisterCustomCELOptions`). The codegen project does not currently emit registration code; this is a deliberately deferred extension, not an architectural prohibition. Future ADRs proposing this capability cite this RFC's R2 demand qualification.

R5 — All-embedded single-binary deployments require no codegen-specific support. The codegen output is deployment-shape-agnostic. Enforced by R1 (Engine works identically) and the unchanged generator output.

R6 — Future ADRs proposing Tier 2 codegen extensions reference this RFC's R2 demand qualification. ADRs without a qualifying demand are rejected at the Decision section. Policy-only until a chain-link gate is added to `harness validate-docs`.

## Impact

- **`README.md`** — requires a new section documenting Tier 1 (embedded SpiceDB) and the Tier 2 hand-roll patterns. Follow-up scope note + job.
- **`docs/ADR-001-parser-migration.md`** — unchanged. Concerns parser backend, not policy-engine integration.
- **`docs/ADR-002-wildcard-codegen.md`** — unchanged. Concerns wildcard-relation codegen, not policy-engine integration.
- **`docs/ADR-003-wildcard-read-side.md`** — unchanged. Same domain as ADR-002.
- **Existing SPECs (`spec-001` through `spec-012`)** — unchanged. All concern codegen mechanics for SpiceDB schema constructs; none touch external policy engines.
- **`pkg/authz/`** — unchanged. The runtime contract is connection-agnostic; embedded SpiceDB works through the existing `Engine` interface.
- **`internal/generator/`** — unchanged unless and until Tier 2 demand qualifies (per R2). At that point, a new SPEC defines the codegen extension.
- **Future ADRs** — any ADR proposing a Tier 2 codegen extension cites this RFC and demonstrates demand qualification per R2.
- **`example/` services** — unchanged. The fixture round-trip remains the codegen regression bar.

## Assumptions

- **A1 [EXTERNAL FACT]:** SpiceDB supports embedded mode via the `github.com/authzed/spicedb` Go library with in-process gRPC and configurable datastore (MemDB, sqlite, postgres, spanner, mysql). Evidence: authzed/examples PR #10 ("example: use SpiceDB as a library"), merged 2023-06-29. URL: https://github.com/authzed/examples/pull/10
- **A2 [EXTERNAL FACT]:** OPA exposes its HTTP server via `runtime.NewRuntime` Go API — same code path as `opa run --server`. Evidence: `github.com/open-policy-agent/opa/runtime` package with `Params.Addrs` field and `Runtime.StartServer` method.
- **A3 [EXTERNAL FACT]:** CEL is the expression language used by SpiceDB caveats server-side. The same `cel-go` library is embeddable in Go for client-side evaluation. Evidence: `internal/generator/adapter.go:caveatTypeToGo` already type-maps caveat parameters from CEL types (per AUZ-018).
- **A4 [HYPOTHESIS]:** No specific user has yet reported a Tier 2 deployment requiring codegen support. Verification: no issues / discussions / direct user feedback on the matter as of 2026-05-10. If a user surfaces a Tier 2 demand, R2 qualification applies.
- **A5 [VERIFIED] (dual reality):** SpiceDB exposes public Go APIs to register custom CEL types and functions on a `*types.TypeSet` — `RegisterCustomType`, `RegisterMethodOnDefinedType`, `RegisterCustomCELOptions`, `RegisterBasicType`, `RegisterGenericType`. The mechanism is register-before-freeze: caller constructs a TypeSet, registers, calls `Freeze()`, passes to caveat compilation via `NewEnvironmentWithTypeSet`. **For embedded SpiceDB (Pattern 1)**, this is reachable — the caller controls startup. **For deployed SpiceDB binary (remote gRPC)**, the binary uses package-level `types.Default.TypeSet` with no flag / config / plugin injection path; custom registration is unreachable without forking. Evidence:
    - `pkg/caveats/types/registration.go:117` — `RegisterCustomType[T CustomType](ts *TypeSet, keyword string, baseCelType *cel.Type, converter typedValueConverter, opts ...cel.EnvOption)`
    - `pkg/caveats/types/registration.go:130` — `RegisterMethodOnDefinedType(ts *TypeSet, baseType *cel.Type, name string, args []*cel.Type, returnType *cel.Type, binding func(arg ...ref.Val) ref.Val)`
    - `pkg/caveats/types/registration.go:140` — `RegisterCustomCELOptions(ts *TypeSet, opts ...cel.EnvOption)`
    - `pkg/caveats/types/typeset.go:54` — `NewTypeSet()` returns mutable, unfrozen TypeSet
    - `pkg/caveats/env.go:29` — `NewEnvironmentWithTypeSet(ts *types.TypeSet)` accepts caller TypeSet
    - `grep -rln "RegisterCustom" $GOMODCACHE/github.com/authzed/spicedb@v1.52.0/` returns only internal SpiceDB files — confirms no external caller path in v1.52.0
    - This finding splits the original blanket-rejection R4 into R4a (deployed SpiceDB — impossible without fork) and R4b (embedded SpiceDB — possible but deliberately deferred).

## History

<!-- managed by `harness history-update` — do not hand-edit -->
