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
| Expiration (`with expiration`)         | ✓ — per-tuple TTL via `Expirations` sub-struct on `<Rel>Objects`; auto-switches to `OPERATION_TOUCH`; combines with caveats |
| Sub-relation references (`foo#bar`)    | ✓ — typed userset write field (`<TypeName><PascalSubRel>`) on `<Rel>Objects`; userset Check input field; `SubRelation` on metadata struct |

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

## Expiration

Schemas declaring `use expiration` at the top can mark relations with `with expiration`. Tuples carry per-tuple TTL via `OptionalExpiresAt`; SpiceDB filters expired entries server-side from Check / Lookup / Read. The codegen surfaces a `*time.Time` field per expiring allowed type on a new `Expirations` sub-struct (parallel to `Wildcards` and `Caveats`):

```hcl
use expiration

definition extsvc/folder {
    relation expiring_viewer: extsvc/user with expiration
    permission expiring_browse = expiring_viewer
}
```

```go
expiresAt := time.Now().Add(1 * time.Hour)
folder.CreateExpiringViewerRelations(ctx, extsvc.FolderExpiringViewerObjects{
    User: []extsvc.User{user},
    Expirations: extsvc.FolderExpiringViewerExpirations{
        User: &expiresAt,
    },
})
```

Combined with caveats — `relation gated: extsvc/user with extsvc/tenant_match and expiration` — both `Caveats` and `Expirations` sub-structs are populated independently. The codegen routes through `CreateRelationsWithExpiration` (auto-switching to `OPERATION_TOUCH` because un-garbage-collected expired tuples may collide on tuple identity). See `docs/spec-004-expiration-codegen.md`.

## Read with Metadata

`Read<Rel><Type>Relations` returns `[]<Rel><Type>Relation` — a typed metadata struct per tuple carrying the subject ID alongside the caveat name, decoded caveat context, and expiration timestamp:

```go
type FolderTenantedViewerUserRelation struct {
    ID            extsvc.User
    CaveatName    string         // "" when no caveat is attached
    CaveatContext map[string]any // nil when no caveat or empty pre-context
    ExpiresAt     *time.Time     // nil when no per-tuple TTL
}
```

The metadata fields are nil/empty for plain relations; they populate from SpiceDB's `Relationship.OptionalCaveat` and `Relationship.OptionalExpiresAt` for trait-bearing tuples. Use cases — admin/audit UIs that need to surface "user X has access via tenant=acme until 2026-Q4" without bypassing the codegen.

For callers that just want the IDs (matching the pre-v1.4.0 shape):

```go
rels, _ := folder.ReadViewerUserRelations(ctx)
users := authz.IDsOf(rels)  // []User
```

`authz.IDsOf` is a generic helper; type inference resolves the typed slice from the single positional argument.

Wildcard reads return the same metadata struct alongside the presence bool:

```go
meta, isWildcard, err := folder.ReadGuestUserWildcard(ctx)
if isWildcard && meta.ExpiresAt != nil {
    // public-for-everyone-until-timestamp pattern
}
```

See `docs/spec-005-read-with-metadata.md` for the full Engine surface and constraints (no auto-decoded `<Caveat>Args`, slice materialization vs streaming, wildcard split discipline).

## Sub-relation References

Schemas declaring `relation X: Type#SubRelation` grant access via inheritance — anyone reaching `Type#SubRelation` (typically a permission or relation on the target type) is implicitly granted on the resource. The codegen surfaces userset writes as a typed field on `<Rel>Objects`:

```hcl
definition extsvc/team {
    relation owner: extsvc/user
    relation manager: extsvc/user
    permission admin = owner + manager
}

definition extsvc/folder {
    relation collab: extsvc/team#admin
    permission collab_view = collab
}
```

```go
// Grant team t1's admin set as a collaborator. SpiceDB stores
// (folder:f1, collab, team:t1#admin) — the wire keeps the team ID
// as the anchor; user resolution happens at Check time.
folder.CreateCollabRelations(ctx, extsvc.FolderCollabObjects{
    TeamAdmin: []extsvc.Team{team},
})
```

Common-case Check (does user u1 have access?) — SpiceDB walks the userset chain server-side:

```go
// u1 must be owner or manager of t1 for this Check to grant.
ok, _ := folder.CheckCollabView(ctx, extsvc.CheckFolderCollabViewInputs{
    TeamAdmin: []extsvc.Team{team},  // userset-as-subject input
})
```

Permissions reaching userset allowed types expose userset input fields on `Check<Perm>Inputs`. The userset-as-subject Check (rare case) matches the literal userset reference — useful for "does this group itself have permission?" admin/audit tooling. Direct-subject Check (the common case) walks the chain transparently when the schema includes both branches.

Read-side rows surface a `SubRelation` field on the metadata struct — empty for direct subjects, non-empty for userset references. Mixed schemas (`relation viewer: user | team#admin`) produce distinct Read methods per subject type (`ReadViewerUserRelations` and `ReadViewerTeamRelations`); each returns disjoint rows.

See `docs/spec-006-sub-relation-references.md` for the wire-level walkthrough, the rare-case Check semantics (literal-match vs chain-walking), and the deferred Lookup-with-userset-results work.

## Conditional Permission

SpiceDB returns `CONDITIONAL_PERMISSION` when a caveat reaches the Check chain but the request is missing parameter context. `Check<Perm>` paths surface this as a typed error so callers can distinguish recoverable failures (missing context) from hard denies:

```go
err := folder.CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
    User: []extsvc.User{user},
    // Caveats omitted — caller forgot to supply tenant
})

switch {
case err == nil:
    // granted

case errors.Is(err, authz.ErrConditionalPermission):
    var cpe *authz.ConditionalPermissionError
    errors.As(err, &cpe)
    // cpe.MissingKeys == ["tenant"] — fetch from request context and retry

case errors.Is(err, authz.ErrPermissionDenied):
    // hard deny — user genuinely lacks permission
}
```

The typed error's custom `Is` method matches both `ErrConditionalPermission` (for the rich-signal opt-in path) and `ErrPermissionDenied` (for backward compat with existing deny checks). Callers that only care about "denied vs. granted" keep working unchanged.

Lookup paths return a typed `LookupResult` partitioning definite grants from conditional grants — the same recovery hint surfaces on both Check and Lookup. Caller pattern:

```go
result, err := folder.LookupTenantedBrowseUserSubjects(ctx, caveats)
// result.Definite — confirmed grants
// result.Conditional — partial grants; each has MissingKeys for caller to fetch and retry

for _, c := range result.Conditional {
    fetched := fetchTenantContext(c.MissingKeys)
    // retry Check / Lookup with the fetched context
}
```

Per-type `<Type>LookupResult` and `<Type>ConditionalLookupEntry` structs are generated once per object type and shared across every Lookup method returning that type. Wildcard subject methods (`Lookup<Perm><Type>WildcardSubjects`) keep their `(bool, error)` signature — they check `result.Definite` for the wildcard sentinel internally.

See `docs/spec-007-conditional-permission-signal.md` for the Check path, `docs/spec-008-lookup-conditional-surfacing.md` for the Lookup path.

## Consistency

The `*spicedb.Engine` defaults to a time-based consistency policy: pin to `AtExactSnapshot` when a recent write token exists (read-your-own-writes), fall through to SpiceDB's `MinimumLatency` otherwise. For security-sensitive checks where stale reads are unacceptable, opt into `FullyConsistent`:

```go
// Default behavior — recent-token-or-nil from the engine's time-based policy:
err := folder.CheckTenantedBrowse(ctx, input)

// Force fresh evaluation — bypasses cached snapshot:
ctx = authz.WithConsistency(ctx, authz.ConsistencyFullyConsistent)
err := folder.CheckTenantedBrowse(ctx, input)
```

The override is per-call via context. Caller scopes it at the request boundary; all downstream Check / Lookup / Read methods called with that ctx honor the mode automatically. Zero codegen change — ctx already flows through every generated method.

Token-based modes (`AtLeastAsFresh`, `AtExactSnapshot` with caller-supplied tokens) are deferred — the engine already uses `AtExactSnapshot` internally for read-your-own-writes. See `docs/spec-009-consistency-mode-opt-in.md`.

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
