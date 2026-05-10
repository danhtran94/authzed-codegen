<!-- approved -->

# AUZ-021: OPA global builtin registration + runtime.NewRuntime demo

| Field      | Value                                              |
|------------|----------------------------------------------------|
| Status     | Done                                               |
| Created    | 2026-05-10                                         |
| Assignee   | Danh Tran                                          |
| Source     | jobs/AUZ-019-opa-go-builtins-codegen.md            |
| Blocked by | —                                                  |

<!-- Parent-job follow-up: AUZ-019 shipped the per-instance SpiceDBBuiltins; -->
<!-- AUZ-020's research confirmed runtime.NewRuntime needs GLOBAL builtin -->
<!-- registration (rego.RegisterBuiltinN). This job adds the global variant -->
<!-- AND rewrites example/opa-embed to use runtime.NewRuntime (dropping the -->
<!-- plain net/http.Server) so the demo runs on OPA's standard server. -->

## Goal

Two coupled changes:

1. **Codegen** — add `RegisterSpiceDBBuiltinsGlobal(engine authz.Engine, ctx context.Context)` to each generated `opa.gen.go`, alongside the existing `SpiceDBBuiltins(engine, ctx) []func(*rego.Rego)`. The global variant calls `rego.RegisterBuiltin3` / `rego.RegisterBuiltin2` with the same builtin decls + closures, registering them in OPA's process-global registry (`ast.Builtins` + topdown map) so any compiler built afterward — including `runtime.NewRuntime`'s — includes them.

2. **Demo** — rewrite `example/opa-embed/main.go` to use `runtime.NewRuntime` instead of the plain `net/http.Server`. The demo calls `RegisterSpiceDBBuiltinsGlobal(engine, ctx)` for each package BEFORE `runtime.NewRuntime`, loads `policy/policy.rego` via `Params.Paths`, and starts OPA's standard server (`/health`, `/v1/data/authz/allow`, `/v1/policies`, etc.). The plain `http.ServeMux` + hand-written handlers are removed. Same `policy.rego`, same curl recipes (OPA's `/v1/data` contract already matches what the demo exposed).

Round-trip regenerates byte-identically; a new e2e test in `extsvc` registers globally and confirms a fresh `rego.New` with no per-instance options still resolves `extsvc.check_folder_browse`; the demo's manual run check exercises OPA's standard endpoints. SPEC-013 + AUZ-019 + AUZ-020 Discoveries get forward references.

## Problem

    Today (AUZ-019 + AUZ-020):
      SpiceDBBuiltins(engine, ctx) → []func(*rego.Rego)
        └─► rego.New(opts...)            ✓ sees the builtins
        └─► runtime.NewRuntime(params)   ✗ no Params hook for func(*rego.Rego) options
      → example/opa-embed uses a plain net/http.Server doing per-request rego.New,
        NOT OPA's runtime — so no /v1/policies, decision logs, bundles, etc.

    After AUZ-021:
      RegisterSpiceDBBuiltinsGlobal(engine, ctx)
        └─► rego.RegisterBuiltin3(decl, impl)
              → ast.RegisterBuiltin (global ast.Builtins + ast.BuiltinMap)
              → topdown.RegisterBuiltinFunc (global topdown map)
      → runtime.NewRuntime(params) builds plugins.Manager → ast.NewCompiler(),
        whose builtin universe is read from the global ast.BuiltinMap ✓
      → server.handleData → rego.New(rego.Compiler(manager's compiler), ...) ✓
      → example/opa-embed runs OPA's standard server; POST /v1/data/authz/allow
        evals data.authz.allow with the SpiceDB builtins resolved ✓

Mechanism verified by inspecting (AUZ-020 research, see those Discoveries): `v1/rego/rego.go` `RegisterBuiltin3` body; `v1/ast/builtins.go` `RegisterBuiltin`; `v1/ast/compile.go` compiler builtin map; `v1/server/server.go` `rego.Compiler(s.getCompiler())`; `v1/runtime/runtime.go` `Params` (no `Builtins` field) + `plugins.New` → `ast.NewCompiler()`.

## What Stays Unchanged

- `SpiceDBBuiltins(engine, ctx) []func(*rego.Rego)` — the per-instance function stays exactly as-is; the global variant is additive
- `internal/generator/opa.go` — `GenerateOPASource` gains a `dict` template helper (WS1.4); `groupByPackage` and the overall structure unchanged
- `cmd/authzed-codegen/main.go` — `--emit-opa` flag unchanged; same output trigger
- `pkg/authz/`, `internal/generator/adapter.go` — untouched
- Existing `<entity>.gen.go` files — untouched
- `example/opa-embed/policy/policy.rego` — the policy file itself is unchanged (`package authz`, RBAC + ReBAC + deny); only how the demo loads it changes (was: `rego.Module` string in main.go; now: `Params.Paths` pointing at the policy dir)
- `spicedbtest.Start` as the demo's SpiceDB source — still testcontainers (the in-process SpiceDB embed is still blocked by authzed-go v1.9.0; unchanged from AUZ-020)
- The demo's curl recipes — OPA's `/v1/data/<path>` POST contract (`{"input": {...}}` → `{"result": <value>}`) matches what the plain handler exposed; existing README curls work as-is

## Workstreams

### 1. Template — add the global-registration function

| # | Task | File | Status |
|---|------|------|--------|
| 1.1 | Add `func RegisterSpiceDBBuiltinsGlobal(engine authz.Engine, ctx context.Context)` to `opa.go.tmpl`, after `SpiceDBBuiltins` — for each Check method emit `rego.RegisterBuiltin3(&rego.Function{Name: "...", Decl: ...}, func(...) {...})`; for each Lookup emit `rego.RegisterBuiltin2(...)`. Same decl + closure bodies as the per-instance `rego.Function3`/`Function2` stanzas; reuse the file-local helpers (`parseSubject`, `termToStructpb`, `astValueToInterface`, `structpbToMap`, `checkResultToTerm`, error sentinels) | `internal/templates/opa.go.tmpl` | [x] |
| 1.2 | Go doc on `RegisterSpiceDBBuiltinsGlobal`: mutates OPA's process-global builtin registry; **call once at startup before any concurrent compilation** (`ast.RegisterBuiltin` is not concurrency-safe — OPA's source warns of "concurrent map read/write panics"); double-calling appends duplicate entries to `ast.Builtins`; unlocks `runtime.NewRuntime` / the standard `/v1/data` endpoint; the per-instance `SpiceDBBuiltins` remains the no-global-state choice for `rego.New` callers | same | [x] |
| 1.3 | Factor the Check/Lookup decl + closure bodies into shared `{{ define }}` blocks (`checkDecl` / `checkImpl` / `lookupDecl` / `lookupImpl`) so `SpiceDBBuiltins` and `RegisterSpiceDBBuiltinsGlobal` reference the SAME fragments — single source of truth; bodies can't drift | `internal/templates/opa.go.tmpl` | [x] |
| 1.4 | Register a `dict` template helper in `GenerateOPASource` (builds `map[string]any` from alternating key/value args) so `{{ template "checkImpl" (dict "Pkg" $.PackageName "Def" $def "Perm" $perm) }}` can pass the multi-field payload into the `define` blocks | `internal/generator/opa.go` | [x] |

**Key details:** Per SPEC-013 C1, the global-variant registrations emit in the same alphabetical `(Resource, Permission)` order as the per-instance variant. `groupByPackage` already sorts; the template iterates the same sorted slices.

### 2. Regenerate fixtures

| # | Task | File | Status |
|---|------|------|--------|
| 2.1 | `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed` | (regenerate) | [x] |
| 2.2 | Verify each `example/authzed/{bookingsvc,menusvc,extsvc}/opa.gen.go` declares BOTH `SpiceDBBuiltins` AND `RegisterSpiceDBBuiltinsGlobal` | `example/authzed/*/opa.gen.go` | [x] |
| 2.3 | Round-trip: re-run codegen; `git diff --quiet example/authzed/` exits 0 | (verify) | [x] |

### 3. e2e test — global registration path

| # | Task | File | Status |
|---|------|------|--------|
| 3.1 | New test `TestOPA_GlobalRegistration` in `extsvc_opa_test.go`: call `extsvc.RegisterSpiceDBBuiltinsGlobal(sb.Engine, ctx)` ONCE; seed a `folder.viewer` grant; build a fresh `rego.New(rego.Query(...), rego.Module(...))` with **no** `SpiceDBBuiltins` options; eval a policy invoking `extsvc.check_folder_browse(...)`; assert `true`. Proves the global registry path resolves the builtin without per-instance wiring | `example/authzed/extsvc/extsvc_opa_test.go` | [x] |
| 3.2 | Comment in that test flagging global-state hygiene: `RegisterSpiceDBBuiltinsGlobal` appends to `ast.Builtins` — if the package later adds more global-registration tests, register once in `TestMain` or guard with `sync.Once`. A single call here is fine | same | [x] |

### 4. Demo rewrite — `example/opa-embed` on `runtime.NewRuntime`

| # | Task | File | Status |
|---|------|------|--------|
| 4.1 | Rewrite `main.go`: read schema → `spicedbtest.Start` → `authz.SetDefaultEngine(sb.Engine)` → seed via generated `Create<Rel>Relations` (all unchanged); THEN call `extsvc.RegisterSpiceDBBuiltinsGlobal(sb.Engine, ctx)` + `bookingsvc.RegisterSpiceDBBuiltinsGlobal(...)` + `menusvc.RegisterSpiceDBBuiltinsGlobal(...)` BEFORE building the runtime | `example/opa-embed/main.go` | [x] |
| 4.2 | Build `rt, err := runtime.NewRuntime(ctx, runtime.Params{Addrs: &[]string{fmt.Sprintf(":%d", port)}, Paths: []string{"example/opa-embed/policy"}, GracefulShutdownPeriod: 5, EnableVersionCheck: false, ...minimum...})`; log the listen addr | same | [x] |
| 4.3 | Start OPA's server: `rt.StartServer(ctx)` (confirm exact method/signature during impl — `v1/runtime/runtime.go`); block until ctx is cancelled (SIGINT/SIGTERM via `signal.NotifyContext`); on cancel, shut down the runtime and `sb.Close` | same | [x] |
| 4.4 | Remove the plain `net/http` server, `http.ServeMux`, `/health` + `/v1/data/authz/allow` hand-written handlers, the `//go:embed policy/policy.rego` + `rego.Module` per-request eval, and the per-request `rego.New` build — OPA's runtime provides `/health` and `/v1/data/...` natively | same | [x] |
| 4.5 | `--port` flag unchanged (default `:8181`) | same | [x] |
| 4.6 | Update `example/opa-embed/README.md`: architecture diagram now shows OPA's runtime (not a plain http.Server); note the demo uses `RegisterSpiceDBBuiltinsGlobal` + `runtime.NewRuntime`; the curl recipes are unchanged (OPA's `/v1/data` contract matches); add `/v1/policies` to the "what's exposed" list; keep the Limitations note (Docker required for SpiceDB; MemDB → no persistence; local-only) | `example/opa-embed/README.md` | [x] |

**Key details:** `runtime.Params.Paths` loads `.rego` files from the given filesystem paths — pointing at `example/opa-embed/policy` loads `policy.rego` into the runtime's store under `data.authz`. The demo still runs from the repo root (it already reads `example/schema.zed` by relative path). Disable `EnableVersionCheck` so the demo doesn't phone home. `runtime.NewRuntime` may also want a `Logger` / `ConsoleLogger` set to avoid noisy default logging — set a minimal logger if needed.

### 5. Documentation updates

| # | Task | File | Status |
|---|------|------|--------|
| 5.1 | SPEC-013 Interface Contracts: add `RegisterSpiceDBBuiltinsGlobal(engine authz.Engine, ctx context.Context)` as a sibling export to `SpiceDBBuiltins`, with the global-state + concurrency note; add Constraint C8 ("the global variant must be registered once at startup before concurrent compilation; `ast.RegisterBuiltin` is not concurrency-safe") | `docs/spec-013-opa-go-builtins-codegen.md` | [x] |
| 5.2 | AUZ-019 Discoveries: refine the "Exported API changed" entry — per-instance form was the right default; a global variant (AUZ-021) was added later for `runtime.NewRuntime` compatibility; drop the "SpiceDBBuiltinDecls() variant — out of scope here" sentence (AUZ-021 supersedes it with `RegisterSpiceDBBuiltinsGlobal`) | `jobs/AUZ-019-opa-go-builtins-codegen.md` | [x] |
| 5.3 | AUZ-020 Discoveries: refine the "`runtime.NewRuntime` cannot accept per-instance builtins" entry — clarify the runtime DOES pick up globally-registered builtins; AUZ-021 adds `RegisterSpiceDBBuiltinsGlobal` AND rewrites the demo to use `runtime.NewRuntime`; the plain-`http.Server` approach was the AUZ-020-era stopgap, superseded by AUZ-021 | `jobs/AUZ-020-opa-embed-demo.md` | [x] |

### 6. Verification

| # | Task | Status |
|---|------|--------|
| 6.1 | `go build ./...` exits 0 | [x] |
| 6.2 | `go vet ./...` exits 0 | [x] |
| 6.3 | `go mod tidy` produces no diff | [x] |
| 6.4 | `go test ./pkg/authz/spicedb/... ./example/authzed/...` passes (or skips cleanly without Docker) — includes the new `TestOPA_GlobalRegistration` | [x] |
| 6.5 | Round-trip: `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed && git diff --quiet example/authzed/` exits 0 | [x] |
| 6.6 | Demo manual run check: `go run ./example/opa-embed --port 18181 &`; wait for `/health` → 200; `POST /v1/data/authz/allow` with `{"input":{"user":{"id":"alice","role":"viewer"},"resource":{"id":"demo-folder"}}}` → `{"result":true}`; with `banned-user`/admin → `{"result":false}`; `GET /v1/policies` returns the loaded policy; SIGTERM → clean shutdown | [x] |

### 7. CHANGELOG

| # | Task | File | Status |
|---|------|------|--------|
| 7.1 | Update the `[1.14.0]` entry: (a) add a bullet — "Generated `opa.gen.go` also declares `RegisterSpiceDBBuiltinsGlobal(engine, ctx)` — registers the SpiceDB builtins in OPA's process-global registry so `runtime.NewRuntime` / the standard `/v1/data` endpoint see them; call once at startup (not concurrency-safe per `ast.RegisterBuiltin`); the per-instance `SpiceDBBuiltins()` remains the no-global-state option for `rego.New`"; (b) update the `example/opa-embed/` bullet — it now runs on `runtime.NewRuntime` (OPA's standard server, `/v1/data` + `/v1/policies` + `/health`), not a plain `net/http.Server` | `CHANGELOG.md` | [x] |

## Design Decisions

### Two functions, not one configurable function
`SpiceDBBuiltins` (per-instance, returns `[]func(*rego.Rego)`) and `RegisterSpiceDBBuiltinsGlobal` (global, returns nothing, mutates `ast.Builtins`) are separate exports. They have genuinely different return types and semantics; a "mode" parameter would muddle both. `rego.New` users take `SpiceDBBuiltins`; `runtime.NewRuntime` users call `RegisterSpiceDBBuiltinsGlobal` at startup.

### Demo rewritten to use `runtime.NewRuntime`
`example/opa-embed/main.go` drops the plain `net/http.Server` and uses OPA's `runtime.NewRuntime`. Reason: with `RegisterSpiceDBBuiltinsGlobal` available, the demo can show the *full* OPA server — `/v1/data` (with SpiceDB builtins resolved), `/v1/policies`, `/health` — which is what a real Pattern-4 deployment looks like. The AUZ-020-era plain-`http.Server` was a stopgap because the global variant didn't exist yet. The policy file and curl recipes are unchanged (OPA's `/v1/data` contract matches what the stopgap exposed).

### Closure bodies reused, not re-derived
The global variant's `rego.RegisterBuiltin3` calls use the SAME closure bodies as the per-instance `rego.Function3` calls. The only difference is the registration call. If the template factors the closure into a shared block, do so; if duplication is more readable, duplicate — but the bodies stay in lockstep.

### No `--emit-opa-global` flag
`RegisterSpiceDBBuiltinsGlobal` always emits alongside `SpiceDBBuiltins` when `--emit-opa` is set. It's a small additional function; its presence is harmless until called. A separate flag adds CLI surface for no benefit.

### SpiceDB stays on testcontainers in the demo
Unchanged from AUZ-020 — `spicedbtest.Start`. The truly-in-process SpiceDB embed is still blocked by `authzed-go` v1.9.0 lacking `NewClientWithConn`. This job changes the OPA side (plain server → runtime), not the SpiceDB side.

## Implementation Order

```
WS1 — Template: add RegisterSpiceDBBuiltinsGlobal     ← unblocks WS2
   ▼
WS2 — Regenerate fixtures                              ← depends on WS1
   ▼
WS3 — e2e test (global path)        ┐
WS4 — Demo rewrite (runtime.NewRuntime)  ┤ both depend on WS2 (import the regenerated package);
                                          │ WS4 needs the global variant present
   ▼                                      │
WS5 — Doc updates                         ┘ can parallel WS3/WS4
   ▼
WS6 — Verification (incl. demo run check) ← depends on WS1-5
   ▼
WS7 — CHANGELOG                            ← parallel to WS6
```

## Notes

- **`runtime.NewRuntime` setup gotchas** (confirm during WS4): exact name/signature of the server-start method (`StartServer` vs `Serve` — `v1/runtime/runtime.go`); whether `Params.Paths` wants a directory or individual `.rego` files; whether a `Logger` must be set to avoid default noisy logging; `Params.GracefulShutdownPeriod` units (seconds, per the field comment); `EnableVersionCheck` defaults to false (no phone-home) but set it explicitly for clarity. Capture surprises in Discoveries.
- **Global-state hygiene**: `rego.RegisterBuiltin3` appends to `ast.Builtins` — N calls = N duplicate entries per builtin. The demo calls `RegisterSpiceDBBuiltinsGlobal` once per package at startup; fine. The e2e test calls it once; fine. WS3.2's comment flags it for future test authors.
- **OPA version**: unchanged — `github.com/open-policy-agent/opa v1.16.1`; `RegisterBuiltin1/2/3/4` at the `/v1/rego` path alongside `Function1/2/3/4`.
- **Curl recipes unchanged**: OPA's runtime serves `POST /v1/data/<path>` with `{"input": {...}}` → `{"result": <value>}` — the exact contract the AUZ-020 plain handler mimicked. The README's existing curls work against the rewritten demo. New: `GET /v1/policies` returns the loaded `policy.rego`.

## Discoveries & Decisions During Implementation

### [Implementer] `{{ define }}`-block factoring worked; needed a `dict` template helper + a careful template comment
WS1.3 factored the Check/Lookup decl + closure bodies into four shared `{{ define }}` blocks (`checkDecl`, `checkImpl`, `lookupDecl`, `lookupImpl`) so `SpiceDBBuiltins` and `RegisterSpiceDBBuiltinsGlobal` reference identical fragments — no drift possible. Passing the multi-field payload (`Pkg`, `Def`, `Perm`) into a `define` block needs a `dict` helper (Go `text/template` `{{ template "name" arg }}` takes a single arg), so WS1.4 registered one in `GenerateOPASource`. Gotcha: the original top-of-file `{{/* ... {{ define }} ... */}}` comment failed to parse — Go's template parser tokenizes `{{ }}` *inside* `{{/* */}}` comments, so the literal `{{ define }}` in the prose was read as an empty define clause. Fixed by removing braces from the comment prose (`{{- /* Each define block below ... */ -}}`). The generated `opa.gen.go` indentation is funky (the `define` fragments carry hardcoded indent that doesn't match the call-site indent) but it compiles and is deterministic — same as the other non-gofmt'd generated files in this repo.

### [Implementer] Global-registration e2e proves the runtime path
`TestOPA_GlobalRegistration` registers `extsvc.RegisterSpiceDBBuiltinsGlobal(sb.Engine, ctx)` once, then builds a fresh `rego.New(rego.Query(...), rego.Module(...))` with **no** per-instance `SpiceDBBuiltins` options — and `extsvc.check_folder_browse(...)` still resolves and returns `true` against a seeded grant. This confirms the global registry path (`ast.RegisterBuiltin` + `topdown.RegisterBuiltinFunc`) is what `runtime.NewRuntime` picks up. Single call in the test; comment flags global-state hygiene (`ast.Builtins` is append-only) for future test authors.

### [Implementer] Demo on `runtime.NewRuntime` — `Serve` over `StartServer`, `Params.Paths` loads the policy dir, `EnableVersionCheck: false`
WS4 rewrote `example/opa-embed/main.go`: `RegisterSpiceDBBuiltinsGlobal` per package BEFORE `runtime.NewRuntime`, then `runtime.Params{Addrs: [":8181"], DiagnosticAddrs: [], Paths: ["example/opa-embed/policy"], GracefulShutdownPeriod: 5, EnableVersionCheck: false}`, then `rt.Serve(ctx)` (returns an error — better than `StartServer` which `os.Exit(1)`s on error). `Params.Paths` loading the policy dir places `policy.rego` at `data.authz`; OPA's standard `POST /v1/data/authz/allow` then evals `data.authz.allow`. Importing `opa/v1/runtime` pulled in the full OPA-server dependency closure (download, server, repl, presentation, ucast, containerd, fsnotify, …) — `go mod tidy` resolved it (~102 new go.mod/go.sum lines on top of AUZ-019's OPA dep). Manual run check: `/health` → 200; `POST /v1/data/authz/allow` → `{"result":true}` for alice (ReBAC) and carol (RBAC), `{"result":false}` for bob (folder he can't see) and banned-user (deny override beats RBAC); `GET /v1/policies` → the loaded `policy.rego`; SIGTERM → "Shutting down… / Server shutdown. / shut down". The plain `net/http` server, `//go:embed policy`, and per-request `rego.New` are removed.

### [Implementer] No further surprises
WS2 (regen — deterministic, both functions emitted in all 3 packages), WS5 (doc updates — SPEC-013 +C8 +as-shipped naming, AUZ-019 + AUZ-020 Discovery refinements), WS6 (build / vet / mod tidy / test all clean; round-trip deterministic), WS7 (CHANGELOG) proceeded as planned. The OPA dependency closure grew (full server vs just `rego`/`ast`/`types` for AUZ-019) — acceptable, the project is committed to Pattern 4 as a first-class feature and the demo intentionally uses the full OPA server. Round-trip vs the AUZ-019 commit: only the 3 `opa.gen.go` files changed (each gains `RegisterSpiceDBBuiltinsGlobal` + the `dict`-driven `define` restructuring); no `<entity>.gen.go` / `schema.gen.go` churn.
