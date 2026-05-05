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
| Caveats (`with <caveat>`)              | ✗ rejected at adapt time                                                                        |
| Expiration (`with expiration`)         | ✗ rejected at adapt time                                                                        |
| Sub-relation references (`foo#bar`)    | ✗ rejected at adapt time                                                                        |

Parsing delegates to `github.com/authzed/spicedb/pkg/schemadsl/compiler` —
any schema SpiceDB accepts will parse. The codegen layer is narrower;
rejected constructs surface schema-relative errors before any output is
written. Rationale: `docs/ADR-001-parser-migration.md`.

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
