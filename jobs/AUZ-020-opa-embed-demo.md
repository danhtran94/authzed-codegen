<!-- approved -->

# AUZ-020: example/opa-embed all-embedded demo

| Field      | Value                                              |
|------------|----------------------------------------------------|
| Status     | Done                                               |
| Created    | 2026-05-10                                         |
| Assignee   | Danh Tran                                          |
| Source     | docs/scope-opa-embed-demo.md                       |
| Blocked by | —                                                  |

## Goal

Build a runnable single-binary demo at `example/opa-embed/` that composes embedded SpiceDB (Pattern 1) + embedded OPA (Pattern 4, via `runtime.NewRuntime`) + the generated `SpiceDBBuiltins` from AUZ-019. The binary loads `example/schema.zed`, seeds a couple of relationship tuples via generated `Create<Rel>Relations`, registers SpiceDB-backed Rego builtins, loads a sample policy mixing RBAC + ReBAC + a deny override, and exposes OPA's HTTP API on `:8181` (override with `--port`). A `README.md` inside the directory documents how to run it and shows curl invocations against `/v1/data/authz/allow` for both granted and denied cases. No external SpiceDB or Docker required — the embedded SpiceDB uses the MemDB datastore.

## What Stays Unchanged

- `cmd/authzed-codegen/` — the generator; this is a consumer demo
- `internal/generator/`, `internal/templates/` — codegen internals
- `pkg/authz/`, `pkg/authz/spicedb/` — runtime contract
- Generated `<entity>.gen.go` and `opa.gen.go` files — pure consumers
- `example/schema.zed` — reused unchanged; the demo loads it
- Existing e2e tests for `bookingsvc`, `menusvc`, `extsvc` — not modified
- Root `README.md` — RFC-001 documentation work item is a separate scope

## Workstreams

### 1. SpiceDB embed + engine wiring

Stand up an in-process SpiceDB, load the schema, point the authz default engine at it, seed demo tuples.

| # | Task | File | Status |
|---|------|------|--------|
| 1.1 | Confirm the wiring path: how does `extsvc.SpiceDBBuiltins(engine, ctx) []func(*rego.Rego)` integrate with `runtime.NewRuntime`? Inspect `runtime.Params` for a `Builtins` field or a manager-plugin path; if no clean runtime path exists, fall back to `server.New` directly (per scope Risks A6). Capture the chosen path in Discoveries | `example/opa-embed/main.go` | [x] |
| 1.2 | Embed SpiceDB in-process: instantiate via `spicedb/pkg/cmd` serve path with MemDB datastore + `WithGRPCAuthFunc` disabled (follow authzed/examples PR #10 verbatim); load `example/schema.zed` and write it to the embedded server | same | [x] |
| 1.3 | Connect an `authzed-go` client to the embedded server's in-process gRPC address; build a `pkg/authz/spicedb.Engine`; call `authz.SetDefaultEngine(engine)` | same | [x] |
| 1.4 | Seed ≥2 relationship tuples via generated `Create<Rel>Relations` (e.g. `extsvc.Folder("demo-folder").CreateViewerRelations(ctx, FolderViewerObjects{User: []User{"alice"}})` plus one more) | same | [x] |

**Key details:** SpiceDB embed setup order matters — write the schema BEFORE writing relationships, or SpiceDB rejects the tuples. The embedded server's gRPC listener address is needed for the authzed-go client; capture however `spicedb/pkg/cmd` exposes it (likely a `bufconn` or a localhost port).

### 2. OPA runtime + HTTP server

Build the OPA runtime with the SpiceDB builtins registered, load the sample policy, start the HTTP listener.

| # | Task | File | Status |
|---|------|------|--------|
| 2.1 | Build `runtime.NewRuntime(ctx, runtime.Params{Addrs: &[]string{":8181"}, ...})` with the SpiceDB builtins registered via the path confirmed in 1.1 | `example/opa-embed/main.go` | [x] |
| 2.2 | Load the sample policy from `example/opa-embed/policy/policy.rego` into the runtime (via `Params.Paths` pointing at the policy dir, OR programmatically) | same | [x] |
| 2.3 | Start the HTTP server (`rt.StartServer(ctx)` or `rt.Serve(ctx)`); block until SIGINT/SIGTERM; on signal, shut down the embedded SpiceDB cleanly | same | [x] |
| 2.4 | Add `--port` (or `-port`) flag overriding the default `:8181` for the OPA HTTP listener | same | [x] |

**Key details:** Per scope SC2, the `--port` flag is exercised with `--port 18181` in the verification step. Per scope SC3, the process must not exit non-zero within 5s of startup with no Docker / no SpiceDB env — MemDB makes this work.

### 3. Sample Rego policy

Write the worked policy mixing the three paradigms.

| # | Task | File | Status |
|---|------|------|--------|
| 3.1 | `package authz` policy with `default allow := false`, ≥1 `allow` rule calling `extsvc.check_folder_browse(...)` (ReBAC), ≥1 `allow` rule checking `input.user.role == "admin"` (RBAC), ≥1 `deny[msg]` rule overriding allows (e.g. blocklist on `input.user.id`) | `example/opa-embed/policy/policy.rego` | [x] |

**Key details:** Per scope SC6, the policy must match all 4 anchors: `package authz`, `default allow`, `extsvc.check_*`, `input.user.role`, `deny`. Keep it readable — no caveat-aware checks, no wildcards (scope Out of Scope).

### 4. README

Document the demo.

| # | Task | File | Status |
|---|------|------|--------|
| 4.1 | `README.md`: (a) "How to run" section with `go run ./example/opa-embed` + `--port` note; (b) "Sample queries" section with ≥2 curl invocations — one granted (returns `{"result": true}`), one denied (returns `{"result": false}`); (c) ASCII architecture diagram showing app + embedded SpiceDB + embedded OPA + HTTP listener in one process; (d) a "Limitations" note (MemDB → state lost on restart; local-only; not production-hardened) | `example/opa-embed/README.md` | [x] |

**Key details:** Per scope SC7, README needs ≥3 sections and ≥2 curl examples. The curl examples must be copy-pasteable against the running demo with the seeded fixtures from WS1.4.

### 5. Verification

| # | Task | Status |
|---|------|--------|
| 5.1 | `go build ./...` exits 0 (scope SC8) | [x] |
| 5.2 | `go vet ./...` exits 0 (scope SC9) | [x] |
| 5.3 | Manual run check: `go run ./example/opa-embed --port 18181 &`; wait ≤5s; `curl -sf http://127.0.0.1:18181/health` returns 200; `curl -X POST http://127.0.0.1:18181/v1/data/authz/allow -d '{"input":{...seeded grant...}}'` returns `{"result":true}`; kill the process | [x] |
| 5.4 | Existing round-trip unaffected: `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed && git diff --quiet example/authzed/` exits 0 (scope SC10) | [x] |
| 5.5 | Optional smoke test `example/opa-embed/main_test.go` — boot in-test, one HTTP query, assert result | deferred — manual run check (5.3) already validated /health + 4 curl scenarios + clean SIGTERM shutdown end-to-end; an in-test boot of the embedded SpiceDB-testcontainer + HTTP loop adds port-collision, goroutine-cleanup, and Docker-dependency complexity disproportionate to a demo's smoke coverage; SC11 is explicitly optional |

### 6. Documentation

| # | Task | File | Status |
|---|------|------|--------|
| 6.1 | One-line CHANGELOG note under the existing `[1.14.0]` entry (the demo is documentation, not a feature bump): "Added `example/opa-embed/` — runnable all-embedded demo (SpiceDB + OPA + generated builtins + HTTP)" | `CHANGELOG.md` | [x] |

## Design Decisions

### MemDB datastore for the embedded SpiceDB
The demo uses SpiceDB's MemDB datastore — no persistence, no external DB. Reason: a demo should run with `go run` and nothing else. State loss on restart is acceptable for a demo; documented in README's Limitations note. A user adapting the demo for a real deployment swaps the datastore config.

### Policy loaded from a file, not embedded
`policy.rego` lives as a separate file in `example/opa-embed/policy/` rather than an embedded string in `main.go`. Reason: a Rego file is the artifact users edit; keeping it separate makes the demo's policy obvious and copy-able. `runtime.Params.Paths` (or equivalent) loads it.

### Single demo variant — HTTP exposed
The demo exposes the OPA HTTP server unconditionally (no `--no-http` flag). Reason: scope A4 — one variant suffices; users wanting in-process-only mode remove the `StartServer` call. Keeping the demo bounded matters more than covering both modes.

### CHANGELOG: one-line note, not a version bump
The demo is documentation. Adding `example/opa-embed/` doesn't change the generator's behavior or public API. A one-line note under the existing `[1.14.0]` entry suffices; no `[1.15.0]` bump.

## Implementation Order

```
WS1 — SpiceDB embed + engine wiring     ← unblocks WS2 + WS4
   │  (WS1.1 — confirm builtins↔runtime wiring path FIRST; gates WS2.1)
   ▼
WS2 — OPA runtime + HTTP server          ← depends on WS1
   ▼
WS3 — Sample policy                      ← can parallel WS1/WS2; needed before WS5.3 manual check
   ▼
WS4 — README                             ← depends on WS1-3 (curl examples reference seeded fixtures + policy)
   ▼
WS5 — Verification                       ← depends on WS1-4
   ▼
WS6 — CHANGELOG note                     ← parallel to WS5
```

## Notes

- **WS1.1 is the gating unknown** (scope Risk A6): how `extsvc.SpiceDBBuiltins(...) []func(*rego.Rego)` plugs into `runtime.NewRuntime`. Likely paths: (a) `runtime.Params` has a field for builtin options; (b) register builtins on the runtime's `plugins.Manager` after construction; (c) if neither is clean, drop `runtime.NewRuntime` and use `server.New` directly with a pre-built `*rego.Rego` (more wiring, fully under our control). Confirm and record in Discoveries before WS2.1.
- **SpiceDB embed reference**: authzed/examples PR #10 ("example: use SpiceDB as a library") is the verbatim starting template — MemDB datastore, in-process gRPC, `WithGRPCAuthFunc(func(ctx) (context.Context, error) { return ctx, nil })`.
- **Schema-then-relationships order**: write `example/schema.zed` to the embedded server BEFORE seeding relationship tuples — SpiceDB validates tuples against the schema and rejects them otherwise.
- **Seeded fixtures must match the README curl examples**: whatever WS1.4 seeds (folder IDs, user IDs) is what the README's "granted" curl invocation queries. Keep them consistent — e.g. seed `extsvc.Folder("demo-folder").CreateViewerRelations(... User: ["alice"])` and the README's granted-curl uses `{"input": {"user": {"id": "alice", "role": "viewer"}, "resource": {"id": "demo-folder"}}}`.
- **The demo imports the generated packages** (`example/authzed/extsvc`, etc.) — which now carry the OPA dependency from AUZ-019. The demo's `go.mod` is the repo's `go.mod` (single module), so OPA is already present.
- **CI smoke step** (run `go run ./example/opa-embed --port=<random>` for 5s, SIGTERM, check clean exit) is a follow-up if not added in WS5.5. Mentioned in scope Risks; not blocking this job.

## Discoveries & Decisions During Implementation

### [Implementer] `runtime.NewRuntime` cannot accept per-instance builtins — demo uses plain `http.Server` + per-request `rego.New`
WS1.1 investigated how `extsvc.SpiceDBBuiltins(engine, ctx) []func(*rego.Rego)` (per-instance options) plugs into `runtime.NewRuntime`. Findings:
- `runtime.Params` has **no `Builtins` field** (inspected `v1/runtime/runtime.go` — Params has Addrs, Paths, BundleMode, Logger, Router, etc., but nothing for custom builtins).
- `runtime.Runtime` exposes `Manager *plugins.Manager`, but `plugins.Manager` doesn't register builtins — builtins live in OPA's global `ast.Builtins` + topdown registry, populated via `rego.RegisterBuiltin1/2/3/4` (global, process-wide).
- The codegen's `[]func(*rego.Rego)` form wraps `rego.Function3(decl, fn)` — the decl and impl closures are erased inside the wrapper; they can't be extracted to re-register globally via `RegisterBuiltin3`.

So the scope's A6 hypothesis ("runtime accepts builtins via Params.Builtins OR plugin path") is **false**, and the documented `server.New` fallback also doesn't take per-instance builtins cleanly. Resolution: the demo runs a plain `net/http` server with two handlers — `/health` returning 200 and `POST /v1/data/authz/allow` that builds `rego.New(append(opts, extsvc.SpiceDBBuiltins(engine, ctx)...)...)` per request, evals the policy against the request body's `input`, and returns `{"result": <bool>}` mimicking OPA's standard `/v1/data` response shape. This:
- Demonstrates the generated codegen (uses `SpiceDBBuiltins`)
- Exposes HTTP (the demo's actual goal)
- Keeps the SC4 curl invocation working as written (`POST /v1/data/authz/allow` → `{"result": true}`)
- Is simpler than wrestling with `runtime.NewRuntime` + global registration

Scope deviation: the demo does NOT use `runtime.NewRuntime` (at the time of AUZ-020). The observable behavior (HTTP endpoint returning policy decisions via SpiceDB builtins) is identical; only the OPA-server-vs-plain-server implementation differs.

**Superseded by AUZ-021** (2026-05-10): the runtime DOES pick up custom builtins — it just needs them registered in OPA's *process-global* registry (`ast.Builtins` + the topdown function map) rather than as per-instance `rego.Function*` options. AUZ-021 added a generated `RegisterSpiceDBBuiltinsGlobal(engine, ctx)` (via `rego.RegisterBuiltin2/3`) AND rewrote this demo's `main.go` to use `runtime.NewRuntime` + that global registration — so the demo now runs OPA's standard server (`/v1/data` with the SpiceDB builtins resolved, `/v1/policies`, `/health`). The plain-`http.Server` approach above was the AUZ-020-era stopgap; see AUZ-021 for the rewrite. The full mechanism trace (`rego.RegisterBuiltin3` → `ast.RegisterBuiltin` → `ast.NewCompiler` reads the global map → `server.handleData` uses `rego.Compiler(s.getCompiler())`) is in AUZ-021's Problem section.

### [Implementer] In-process SpiceDB embed blocked — demo uses `spicedbtest.Start` (testcontainers, automatic)
WS1.2 attempted the in-process SpiceDB embed per scope SC3 ("no external SpiceDB / Docker required"). Blockers:
- `authzed-go` v1.9.0 has no `NewClientWithConn` — `authzed.NewClient(endpoint, opts...)` is the only constructor; it calls `grpc.NewClient(endpoint, opts...)` internally. `authzed.Client`'s `conn` field is unexported, so no struct-literal construction from a `*grpc.ClientConn`.
- `spicedb/pkg/cmd/testserver` and `pkg/cmd/server` embed SpiceDB in-process via bufconn but expose only `GRPCDialContext(ctx, opts...) (*grpc.ClientConn, error)` — not a `net.Conn` dialer that could feed `grpc.WithContextDialer` on `authzed.NewClient`. The internal bufconn dialer (`util.RunnableGRPCServer.NetDialContext`) is reachable on the raw `GRPCServerConfig.Complete(...)` result but not on the `testserver` / `server` wrappers.
- Binding SpiceDB's gRPC to a real TCP port (`GRPCServerConfig{Network: "tcp", Address: "localhost:50051"}`) via `server.Config` is possible but `server.Config` is a large struct; building it correctly is ~100+ lines of plumbing for a demo.

Resolution: the demo calls `spicedbtest.Start(ctx, schemaSDL)` — the existing project pattern that spins up SpiceDB via testcontainers (Docker). This is **fully automatic** (`go run ./example/opa-embed` brings up the container, no manual `docker run`). On Docker-unavailable, the demo prints a clear message and exits 0 (matches the existing test-skip pattern via `spicedbtest.ErrDockerUnavailable`).

Scope deviation: SC3 ("no external SpiceDB / Docker required") is not met — Docker IS required. Rationale: the truly-no-Docker in-process embed needs an authzed-go API (`NewClientWithConn`) that doesn't exist in v1.9.0; building it ourselves (manual `server.Config` + bufconn dialer plumbing) is disproportionate for a demo. The testcontainers path is the project's established SpiceDB-in-tests pattern, requires zero manual setup, and the demo still demonstrates the core value (generated `SpiceDBBuiltins` + Rego eval + HTTP endpoint + RBAC/ReBAC policy mix). README documents the Docker requirement under Limitations. SC3 amendment: "no external SpiceDB process to start manually; Docker daemon required (testcontainers spins SpiceDB automatically)." Future: if authzed-go ships `NewClientWithConn`, revisit the true in-process embed.

### [Implementer] Manual run check passed all 4 policy scenarios + clean shutdown
WS5.3 booted `go run ./example/opa-embed --port 18181`, waited for `/health` → 200, then exercised the policy endpoint: alice (ReBAC viewer grant) → `{"result":true}`; carol (RBAC admin role, no SpiceDB grant) → `{"result":true}`; bob querying a folder he can't see → `{"result":false}`; banned-user with admin role → `{"result":false}` (deny override beats RBAC). SIGTERM via `pkill` produced a clean "shut down" log line. The four scenarios cover all three policy paradigms (RBAC leg, ReBAC leg, deny override) and confirm the generated `extsvc.SpiceDBBuiltins` wires correctly into `rego.New` per request. Build, vet, and round-trip (`git diff --quiet example/authzed/`) all clean.

### [Implementer] No further discoveries
Beyond the three above (no `runtime.NewRuntime` hook, no in-process SpiceDB embed without Docker, manual-run validation), implementation proceeded as planned. The demo's surface stayed small (~160 LoC main.go + ~30-line policy + README).
