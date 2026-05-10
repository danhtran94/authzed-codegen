# opa-embed — all-embedded policy-decision demo (on OPA's runtime)

A runnable single-binary demo of the all-embedded deployment shape from
[RFC-001](../../docs/RFC-001-policy-engine-integration-patterns.md): one Go
process composing **SpiceDB** (relationship graph), **OPA's runtime**
(`runtime.NewRuntime` — the standard OPA server), and the **generated
SpiceDB builtins** from
[AUZ-019](../../jobs/AUZ-019-opa-go-builtins-codegen.md) /
[AUZ-021](../../jobs/AUZ-021-opa-global-builtins.md), serving policy
decisions over OPA's standard HTTP API.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  opa-embed (one Go process)                                 │
│                                                             │
│  startup:                                                   │
│    spicedbtest.Start(schema) → SpiceDB testcontainer        │
│    authz.SetDefaultEngine(engine)                           │
│    seed: folder:demo-folder#viewer@user:{alice,bob}         │
│    extsvc.RegisterSpiceDBBuiltinsGlobal(engine, ctx)  ──┐   │
│    bookingsvc.RegisterSpiceDBBuiltinsGlobal(...)       ─┤ into OPA's
│    menusvc.RegisterSpiceDBBuiltinsGlobal(...)          ─┘ process-global
│           │                                                 │ builtin registry
│           ▼                                                 │
│    runtime.NewRuntime(Params{Addrs: [":8181"],              │
│                              Paths: ["…/policy"]})          │
│      └─► compiler reads the global builtin universe ───┐    │
│      └─► loads policy.rego → data.authz                │    │
│           │                                            │    │
│           ▼                                            │    │
│    OPA HTTP :8181                                      │    │
│     ├─ GET  /health               → 200                │    │
│     ├─ GET  /v1/policies          → policy.rego        │    │
│     └─ POST /v1/data/authz/allow  → {"result":<bool>}  │    │
│            │                                           │    │
│            ▼ evals data.authz.allow:                   │    │
│      policy.rego  ──┬─ RBAC:  input.user.role=="admin" │    │
│                     ├─ ReBAC: extsvc.check_folder_browse(…)─┘
│                     └─ deny:  blocklist override            │
│                          │                                  │
│                          ▼ (the ReBAC builtin calls)        │
│      authz.Engine.CheckPermission(…)  ── gRPC ──┐           │
└─────────────────────────────────────────────────┼───────────┘
                                                  │
                          ┌───────────────────────▼────────────────┐
                          │  SpiceDB (testcontainer)               │
                          │  MemDB datastore                       │
                          │  schema = example/schema.zed           │
                          └────────────────────────────────────────┘
```

The SpiceDB builtins are registered into OPA's **process-global** registry
(`ast.Builtins` + the topdown function map) via
`RegisterSpiceDBBuiltinsGlobal` **before** `runtime.NewRuntime` — which
builds its compiler at construction time and reads that universe. The
per-instance form (`SpiceDBBuiltins(...) []func(*rego.Rego)`) would be
invisible to the runtime; the global form is what it picks up. See
[AUZ-021](../../jobs/AUZ-021-opa-global-builtins.md).

## Run

From the **repo root** (the demo reads `example/schema.zed` and
`example/opa-embed/policy/` by relative path):

```sh
go run ./example/opa-embed
# → starts a SpiceDB testcontainer, applies the schema, seeds relationships,
#   registers the SpiceDB builtins globally, starts OPA's server on :8181

go run ./example/opa-embed --port 9000   # use a different port
```

Docker must be running — SpiceDB runs in a testcontainer (MemDB datastore,
so state is lost on restart). If Docker is unavailable the demo prints a
message and exits cleanly.

## Sample queries

The demo seeds `extsvc/folder:demo-folder#viewer@extsvc/user:alice` (and
`bob`). The policy grants `data.authz.allow` when the user is an `admin`
(RBAC) **or** has the SpiceDB `folder.browse` permission (ReBAC), **and**
is not on the blocklist (`banned-user`).

**Granted — alice is a viewer of demo-folder (ReBAC leg):**

```sh
curl -s -X POST localhost:8181/v1/data/authz/allow \
  -d '{"input":{"user":{"id":"alice","role":"viewer"},"resource":{"id":"demo-folder"}}}'
# => {"result":true}
```

**Granted — carol has the admin role (RBAC leg), even with no SpiceDB grant:**

```sh
curl -s -X POST localhost:8181/v1/data/authz/allow \
  -d '{"input":{"user":{"id":"carol","role":"admin"},"resource":{"id":"demo-folder"}}}'
# => {"result":true}
```

**Denied — bob has a viewer grant but queries a folder he can't see:**

```sh
curl -s -X POST localhost:8181/v1/data/authz/allow \
  -d '{"input":{"user":{"id":"bob","role":"viewer"},"resource":{"id":"some-other-folder"}}}'
# => {"result":false}
```

**Denied — deny override beats even the admin role:**

```sh
curl -s -X POST localhost:8181/v1/data/authz/allow \
  -d '{"input":{"user":{"id":"banned-user","role":"admin"},"resource":{"id":"demo-folder"}}}'
# => {"result":false}
```

**The loaded policy (OPA standard endpoint):**

```sh
curl -s localhost:8181/v1/policies
# => the parsed policy.rego, as JSON
```

**Health:**

```sh
curl -s -o /dev/null -w "%{http_code}\n" localhost:8181/health
# => 200
```

## Limitations

- **Docker required** — SpiceDB runs in a testcontainer. The truly-no-Docker
  in-process embed would need an `authzed-go` API (`NewClientWithConn`) that
  doesn't exist in v1.9.0; building it ourselves is disproportionate for a demo.
- **No persistence** — the MemDB datastore loses all relationships on restart.
  Swap to postgres / spanner in the SpiceDB config for a real deployment.
- **Local-only, no auth** — OPA's server binds without TLS or authentication
  here. Production hardening (auth, TLS, observability, rate limiting) is the
  deployer's responsibility; this is a starting template.
- **Global builtin registration is process-wide** — `RegisterSpiceDBBuiltinsGlobal`
  mutates OPA's global registry. Call it once at startup before the runtime is
  built. (For embedded `rego.New` use that doesn't go through `runtime.NewRuntime`,
  the per-instance `SpiceDBBuiltins() []func(*rego.Rego)` form avoids global state.)

## What this demonstrates

- ✓ Generated `<package>.RegisterSpiceDBBuiltinsGlobal(engine, ctx)` wired into `runtime.NewRuntime`
- ✓ OPA's standard server (`/v1/data`, `/v1/policies`, `/health`) with SpiceDB builtins resolved
- ✓ A single policy file mixing RBAC + ReBAC + a deny override
- ✓ SpiceDB consulted as a primitive *from inside* a Rego policy
- ✓ One binary; OPA-standard HTTP-exposed policy decisions
- ✓ Generated `Create<Rel>Relations` used to seed the graph at startup
