# opa-embed — all-embedded policy-decision demo

A runnable single-binary demo of the all-embedded deployment shape from
[RFC-001](../../docs/RFC-001-policy-engine-integration-patterns.md): one Go
process composing **SpiceDB** (relationship graph), the **OPA Rego runtime**
(policy evaluation), and the **generated `SpiceDBBuiltins`** from
[AUZ-019](../../jobs/AUZ-019-opa-go-builtins-codegen.md), exposing an HTTP
endpoint that returns policy decisions.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  opa-embed (one Go process)                                 │
│                                                             │
│  HTTP :8181                                                 │
│   ├─ GET  /health               → 200                       │
│   └─ POST /v1/data/authz/allow  → {"result": <bool>}        │
│           │                                                 │
│           ▼                                                 │
│   rego.New(extsvc.SpiceDBBuiltins(engine, ctx)...,          │
│            rego.Module("policy.rego", …),                   │
│            rego.Input(request.input)).Eval()                │
│           │                                                 │
│           ▼                                                 │
│   policy.rego  ──┬─ RBAC:  input.user.role == "admin"       │
│                  ├─ ReBAC: extsvc.check_folder_browse(…)    │
│                  └─ deny:  blocklist override               │
│                          │                                  │
│                          ▼ (the ReBAC builtin calls)        │
│   authz.Engine.CheckPermission(…)  ── gRPC ──┐              │
└──────────────────────────────────────────────┼─────────────┘
                                                │
                              ┌─────────────────▼──────────────┐
                              │  SpiceDB (testcontainer)        │
                              │  MemDB datastore                │
                              │  schema = example/schema.zed    │
                              │  seeded: folder:demo-folder     │
                              │          #viewer@user:alice,bob │
                              └────────────────────────────────┘
```

The SpiceDB-backed policy eval goes through this demo's own HTTP handler
(`rego.New(...)` per request), **not** OPA's `runtime.NewRuntime` —
OPA's runtime has no hook for per-instance custom builtins, so a plain
`net/http` server doing the Rego eval is both simpler and sufficient.
See [AUZ-020 Discoveries](../../jobs/AUZ-020-opa-embed-demo.md#discoveries--decisions-during-implementation).

## Run

From the **repo root** (the demo reads `example/schema.zed` by relative path):

```sh
go run ./example/opa-embed
# → starts a SpiceDB testcontainer, applies the schema, seeds relationships,
#   binds :8181

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

**Granted — carol has the admin role (RBAC leg), even though she has no SpiceDB grant:**

```sh
curl -s -X POST localhost:8181/v1/data/authz/allow \
  -d '{"input":{"user":{"id":"carol","role":"admin"},"resource":{"id":"demo-folder"}}}'
# => {"result":true}
```

**Denied — bob has a viewer grant but no admin role, and queries a folder he can't see:**

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
- **Local-only, no auth** — the HTTP endpoint binds without TLS or
  authentication. Production hardening (auth, TLS, observability, rate
  limiting) is the deployer's responsibility; this is a starting template.
- **Not OPA's standard server** — this demo runs a plain `net/http` server, not
  `runtime.NewRuntime`, so OPA's standard endpoints (`/v1/policies`,
  `/v1/data` for non-SpiceDB queries, decision logs, bundles) are not present.
  Adding them is straightforward if needed — see RFC-001 §Pattern 4.

## What this demonstrates

- ✓ Generated `<package>.SpiceDBBuiltins(engine, ctx)` wired into a Rego eval
- ✓ A single policy file mixing RBAC + ReBAC + a deny override
- ✓ SpiceDB consulted as a primitive *from inside* a Rego policy
- ✓ One binary; HTTP-exposed policy decisions
- ✓ Generated `Create<Rel>Relations` used to seed the graph at startup
