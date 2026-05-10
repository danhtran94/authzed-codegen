# Scope: example/opa-embed all-embedded demo

| Field   | Value      |
|---------|------------|
| Status  | Accepted   |
| Created | 2026-05-10 |
| Author  | Danh Tran  |

---

## Problem

RFC-001 §Single-binary all-embedded deployments names the composition: app + embedded SpiceDB (Pattern 1) + embedded OPA (Pattern 4) + codegen output as one Go binary. AUZ-019 shipped the codegen for Pattern 4 (`SpiceDBBuiltins`); no runnable demo exercises the composition end-to-end. The only validation today is `example/authzed/extsvc/extsvc_opa_test.go`, which uses testcontainers (separate SpiceDB process) and `rego.New` directly (no HTTP server). That artifact verifies the BUILTIN works; it does not demonstrate the all-embedded deployment shape users would actually adopt (per A3 + A4).

A user wanting to evaluate Pattern 4 today must:
- Read RFC-001 + ADR-004 + SPEC-013 (architectural background)
- Read `extsvc_opa_test.go` (builtin registration shape)
- Cross-reference SpiceDB embed examples in upstream repos (per A1)
- Cross-reference OPA `runtime.NewRuntime` documentation (per A2)
- Compose those four sources into a working stack themselves

This scope bounds a runnable single-binary demo at `example/opa-embed/` that does the composition, exposes the OPA HTTP server (per RFC-001 §Pattern 4 polyglot HTTP API option), and ships a worked Rego policy mixing RBAC + SpiceDB ReBAC. Future RFC-001 documentation work (the README pattern docs, separate scope) references this demo as the canonical worked example.

Concrete artifacts:

- `example/opa-embed/main.go` — single Go entry point; embedded SpiceDB + embedded OPA + HTTP server
- `example/opa-embed/policy/policy.rego` — sample Rego policy mixing RBAC + SpiceDB ReBAC + deny-override
- `example/opa-embed/README.md` — run instructions, sample curl invocations, ASCII architecture diagram
- `example/schema.zed` — reused unchanged; demo loads it at startup
- No changes to `cmd/authzed-codegen`, `internal/`, `pkg/authz/`, or generated `<entity>.gen.go` / `opa.gen.go` files

## Success Criteria

1. New file `example/opa-embed/main.go` exists with a `func main()`. Verifiable: `ls example/opa-embed/main.go && grep -c "^func main" example/opa-embed/main.go` returns `1`.

2. `go run ./example/opa-embed` starts a single process and binds an OPA HTTP listener. Verifiable: from a separate shell while the process runs, `curl -sf -o /dev/null -w "%{http_code}" http://127.0.0.1:8181/health` returns `200`. The process accepts `--port` (or `-port`) flag overriding the default `:8181`.

3. The process embeds SpiceDB in-process: no external SpiceDB binary is required to run. Verifiable: with no SpiceDB env vars set and no Docker daemon running, `go run ./example/opa-embed --port 18181 &` starts cleanly (process does not exit non-zero within 5s); `curl -sf http://127.0.0.1:18181/health` returns 200. The embedded SpiceDB uses MemDB datastore (per A1).

4. The process loads `example/schema.zed` at startup, applies it to the embedded SpiceDB, and seeds at least 2 relationship tuples using generated `Create<Rel>Relations` methods (e.g. one folder.viewer for a known user). Verifiable: a curl POST to OPA's `/v1/data/<package>/allow` with input matching a seeded grant returns `{"result": true}`.

5. The process registers OPA custom builtins via `extsvc.SpiceDBBuiltins(engine, ctx)` (and bookingsvc / menusvc equivalents) on a `runtime.NewRuntime` instance. Verifiable: `grep -c "SpiceDBBuiltins" example/opa-embed/main.go` returns at least `1`.

6. A sample Rego policy at `example/opa-embed/policy/policy.rego` mixes RBAC + SpiceDB ReBAC. The policy declares `package authz`, `default allow := false`, plus at least one `allow` rule that calls a SpiceDB builtin (e.g. `extsvc.check_folder_browse`) AND at least one `allow` rule that checks an `input.user.role` field (RBAC) AND at least one `deny` rule that overrides allows. Verifiable: `grep -E "package authz|default allow|extsvc\\.check|input\\.user\\.role|deny" example/opa-embed/policy/policy.rego` matches all four anchors.

7. `example/opa-embed/README.md` exists and documents (a) how to run, (b) at least 2 sample curl invocations — one returning granted (true) and one returning denied (false) — and (c) an ASCII architecture diagram showing the in-process composition. Verifiable: `grep -c "^# \\|## " example/opa-embed/README.md` returns at least `3` (sections); `grep -c "curl " example/opa-embed/README.md` returns at least `2`.

8. `go build ./...` exits 0 (the demo builds alongside the rest of the module).

9. `go vet ./...` exits 0.

10. Existing round-trip regression unaffected: `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed && git diff --quiet example/authzed/` exits 0.

11. Optional smoke test at `example/opa-embed/main_test.go` boots the process in-test, sends one HTTP query, asserts `200 + result == true` (or `false`) for a known seeded grant. Verifiable: `go test ./example/opa-embed/...` exits 0 OR skips with a documented sentinel when the test cannot run (e.g. port in use). [Marked optional — ship without if smoke-testing the embedded loop is too fiddly inside a unit test.]

## Out of Scope

- **Production deployment guidance.** Reason: the demo is a starting template, not a deploy guide; production hardening (auth, TLS, secret management, observability) is the user's responsibility.
- **Persistent SpiceDB datastore.** Reason: MemDB is sufficient for a runnable demo; postgres / spanner / sqlite setup is a separate concern. Document in README that the demo loses state on restart.
- **HTTP authentication on the OPA endpoint.** Reason: demo is local-only; binding to `127.0.0.1` is the access-control mechanism. Adding token-based auth is a deploy concern.
- **OPA decision logs / bundle distribution / Discovery plugin.** Reason: caller's domain — the demo wires the minimum runtime to expose HTTP.
- **Multi-tenant fixtures or multi-binary variants.** Reason: scope creep; one binary, one schema, one policy.
- **Caveat-aware permission checks in the demo policy.** Reason: keeps the policy readable for first-time readers; users wanting caveat examples reference `extsvc_opa_test.go`.
- **Wildcard relations in the demo policy.** Reason: same readability rationale.
- **Changes to `cmd/authzed-codegen`, `internal/generator/`, `pkg/authz/`, or generated `<entity>.gen.go` / `opa.gen.go` files.** Reason: this is a consumer demo; the generator stays untouched.
- **README updates to the project root README.** Reason: tracked separately under RFC-001's documentation work item; this scope ships only the demo + its in-directory README.
- **CHANGELOG entry.** Reason: the demo is documentation; if a CHANGELOG entry is wanted, it's a one-line note added in the implementation job rather than a scope deliverable.
- **A SPEC document.** Reason: demo's API surface is `func main() + Rego file`; a SPEC adds ceremony without informing implementation. The scope's Success Criteria are the contract.

## Risks

- **SpiceDB embed setup may have undocumented prerequisites** (gRPC config, datastore initialization order, schema-write timing relative to relationship-write). Mitigation: follow authzed/examples PR #10 verbatim as the starting template (per A1); capture any setup divergence in the implementation job's Discoveries section. If embed setup proves fragile, document the failing path in README and ship.

- **OPA `runtime.NewRuntime` config surface is large** (config struct with bundles, decision logs, server, plugins, paths). Mitigation: use the minimum config — `Params.Addrs = [":8181"]`, no bundles, no plugins, in-process module loading via `Params.Paths` or programmatic policy loading. Document the chosen config shape in main.go comments.

- **HTTP port collision in dev environments** when a developer is running another service on `:8181`. Mitigation: `--port` flag overriding the default; document the override in README. SC2's `--port 18181` test path validates the flag works.

- **Demo gets stale as upstream APIs evolve.** SpiceDB and OPA may change embed entry points or registration shapes. Mitigation: keep the demo's surface small (one main.go, ≤300 LoC). Add a CI smoke step (run `go run ./example/opa-embed --port=<random>` for 5s, check it exits cleanly via SIGTERM) so breakage is caught at PR time. The smoke step is a follow-up if not in this scope.

- **Generated `extsvc.SpiceDBBuiltins` requires `*rego.Rego`; `runtime.NewRuntime` produces a `*Runtime` with an internal `*rego.Rego` accessed via `Manager()` or similar.** The wiring path may be non-obvious. Mitigation: in the implementation job, confirm the runtime → rego options pathway during WS1 (probably `runtime.Params.Builtins` or via `manager.Plugin` registration). If `runtime.NewRuntime` doesn't expose builtins registration cleanly, fall back to a manual server setup using `server.New` instead.

## Assumptions

- **A1 [VERIFIED]:** SpiceDB supports embedded mode via `github.com/authzed/spicedb` Go library with in-process gRPC and MemDB datastore. Evidence: authzed/examples PR #10 (RFC-001 A1); `WithGRPCAuthFunc` disabled is the documented pattern for in-process callers.

- **A2 [VERIFIED]:** OPA exposes its HTTP server from embedded mode via `runtime.NewRuntime` with `Params.Addrs`. Evidence: `github.com/open-policy-agent/opa/runtime` package per RFC-001 A2; `Runtime.StartServer` returns control to the caller after the listener is bound.

- **A3 [VERIFIED]:** Generated `<package>.SpiceDBBuiltins(engine authz.Engine, ctx context.Context) []func(*rego.Rego)` is available for `bookingsvc`, `menusvc`, `extsvc`. Evidence: `example/authzed/{bookingsvc,menusvc,extsvc}/opa.gen.go` shipped in AUZ-019; signature pinned at SPEC-013 Interface Contracts.

- **A4 [HYPOTHESIS]:** A single demo variant with HTTP exposure satisfies the documentation goal; users wanting in-process-only mode (no HTTP server) can adapt by removing the `runtime.StartServer` call. Verification deferred — if multiple users report needing both variants, a follow-up scope adds an `--no-http` flag or a sibling demo.

- **A5 [EXTERNAL FACT]:** `example/schema.zed` parses as a valid SpiceDB schema and is the canonical fixture for this project. Evidence: `cmd/authzed-codegen` consumes it on every regen; existing e2e tests apply it to testcontainer SpiceDB without errors.

- **A6 [HYPOTHESIS]:** Wiring `SpiceDBBuiltins(...)` into `runtime.NewRuntime`'s rego instance works through the runtime's exposed options (likely `Params.Builtins` or via a plugin manager). Verification deferred to the implementation job's WS1; if no clean path exists, the mitigation in Risks lists `server.New` as the fallback.

## History

<!-- managed by `harness history-update` — do not hand-edit -->
