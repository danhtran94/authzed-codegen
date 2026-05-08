# authzed-codegen

Type-safe Go bindings for [AuthZED / SpiceDB](https://authzed.com/) schemas.
Each `definition` block in a `.zed` file becomes a `.gen.go` with typed
constructors, relation writers, and per-permission `Check` / `Lookup`
methods over the runtime engine in `pkg/authz/`.

## Example

Given a schema:

```hcl
definition menusvc/order {
    relation creator: menusvc/user | menusvc/customer
    relation belongs_company: menusvc/company
    permission write = creator + creator->manage + belongs_company->manage
}
```

The codegen produces typed bindings:

```go
order := menusvc.Order("o-1")
user  := menusvc.User("u-1")

if err := order.CreateCreatorRelations(ctx, menusvc.OrderCreatorObjects{
    User: []menusvc.User{user},
}); err != nil {
    return err
}

ok, err := order.CheckWrite(ctx, menusvc.CheckOrderWriteInputs{
    User: []menusvc.User{user},
})
```

Each method dispatches through the `authz.Engine` interface; the SpiceDB
client lives in `pkg/authz/spicedb/`.

## Install

```sh
go install github.com/danhtran94/authzed-codegen/cmd/authzed-codegen@latest
```

## Usage

```sh
authzed-codegen --output <out-dir> <schema.zed>
```

One `.gen.go` is emitted per `definition` block, grouped by namespace
(`menusvc/order` → `<out-dir>/menusvc/order.gen.go`). See `example/` for
a complete schema and its generated output.

## Schema Support

| Construct                              | Status                                                                                          |
|----------------------------------------|-------------------------------------------------------------------------------------------------|
| Union (`+`), arrow (`->`)              | ✓                                                                                               |
| Wildcard relations (`type:*`)          | ✓ — `Wildcards` sub-struct on `<Rel>Objects`; sibling `Read<Rel><Type>Wildcard` read methods    |
| Intersection (`&`), exclusion (`-`)    | ✓                                                                                               |
| Caveats (`with <caveat>`)              | ✓ — typed `<Pascal>Args` per caveat; nested `Caveats` sub-struct on `<Rel>Objects` and `Check<Perm>Inputs`; multi-caveat-per-permission supported |
| Expiration (`with expiration`)         | ✗ rejected at adapt time                                                                        |
| Sub-relation references (`foo#bar`)    | ✗ rejected at adapt time                                                                        |

Parsing delegates to `github.com/authzed/spicedb/pkg/schemadsl/compiler` —
any schema SpiceDB accepts will parse. The codegen layer is narrower;
rejected constructs surface schema-relative errors before any output is
written. Rationale: `docs/ADR-001-parser-migration.md`.

## Caveats

Relations and allowed types declared `with <caveat>` generate a typed
`<CaveatPascal>Args` struct per caveat (one per namespace) plus a
`Caveats` sub-struct on the relation's `<Rel>Objects` and the
permission's `Check<Perm>Inputs`. Scalar fields (`*string`, `*int`,
`*bool`, `*float64`) are pointer-typed so callers can defer individual
parameters to check time; container fields (`[]string`, `[]byte`, `map`)
stay direct (nil = unset).

```hcl
caveat extsvc/tenant_match(tenant string) {
    tenant == "acme"
}

definition extsvc/folder {
    relation tenanted_viewer: extsvc/user with extsvc/tenant_match
    permission tenanted_browse = tenanted_viewer
}
```

Pre-bind the policy at write time (caveat travels with the tuple):

```go
folder.CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
    User: []extsvc.User{user},
    Caveats: extsvc.FolderTenantedViewerCaveats{
        User: &extsvc.TenantMatchArgs{Tenant: new("acme")},
    },
})
```

Or defer all binding to check time (write attaches the caveat name with
no pre-context; check supplies the value):

```go
folder.CreateTenantedViewerRelations(ctx, extsvc.FolderTenantedViewerObjects{
    User: []extsvc.User{user},
    // Caveats omitted — write-time pre-context is nil
})

ok, err := folder.CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
    User: []extsvc.User{user},
    Caveats: extsvc.CheckFolderTenantedBrowseCaveats{
        TenantMatch: &extsvc.TenantMatchArgs{Tenant: new("acme")},
    },
})
```

Per-key precedence is per SpiceDB's wire model: write-time values win on
collision, unbound keys fall through to check-time. Permissions reaching
2+ distinct caveats are supported — `Check<Perm>Caveats` gets one field
per unique caveat, the generated method merges all non-nil entries into
one wire `Context`. Cross-caveat parameter-name collisions (two caveats
declaring the same key) are detected at codegen and emit a clear error.

`Lookup<Perm><Type>Resources` and `Lookup<Perm><Type>Subjects` thread
caveat context through too — for caveat-reaching permissions, both
methods accept a `Caveats` argument (positional for Subjects, on the
existing input struct for Resources) and route through
`LookupResourcesWithCaveat` / `LookupSubjectsWithCaveat`.
`CONDITIONAL_PERMISSION` results are filtered out of the returned slice,
matching `Check<Perm>`'s collapse-to-deny semantics.

See `docs/spec-002-caveat-codegen.md` and `docs/spec-003-write-time-caveat-codegen.md`.

## Behavior Notes

- **Permission chains.** `Check<Permission>Inputs` exposes the full set
  of input types reachable through arrow expressions in referenced
  permissions, including cross-definition arrows. Cycles
  (`permission p = p + q`) exit non-zero with `cycle detected`.
- **Wildcards.** `Create<Rel>Relations` accepts `Wildcards{User: true}`
  regardless of which permissions reference the relation. AuthZED's
  guidance is to grant wildcards only on read-side relations (e.g.
  `viewer`) to avoid universal write access; the codegen does not enforce
  this — callers own the discipline.

## Verification

Round-trip the fixture (regression bar for the codegen itself):

```sh
go run ./cmd/authzed-codegen --output example/authzed example/schema.zed
git diff --quiet example/authzed/
```

End-to-end tests exercise the generated stubs against a real SpiceDB
container via `testcontainers-go`. The harness lives in
`pkg/authz/spicedbtest/`; the test packages are
`example/authzed/{bookingsvc,menusvc,extsvc}` and `pkg/authz/spicedb/`.

```sh
go test ./pkg/authz/spicedb/... ./example/authzed/...
```

Tests skip cleanly when Docker is unavailable.

## License

MIT — see [LICENSE](LICENSE).
