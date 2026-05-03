# [ADR-002] Wildcard Codegen: Wildcards Sub-Struct on Objects Struct over Magic ID and Public Methods

| Field      | Value                       |
|------------|-----------------------------|
| Status     | Accepted                    |
| Date       | 2026-05-03                  |
| Deciders   | Danh Tran                   |
| Scope      | authzed-codegen generator + pkg/authz runtime |
| Depends on | ADR-001                     |

---

## Context

AuthZED schemas can declare wildcard-eligible relations: `relation viewer: bookingsvc/user:*`. The `:*` token means "any subject of this type" — server-side, you grant the relation to all users of that type by writing a single tuple with `ObjectId: "*"` instead of one tuple per user. The existing fixture uses this on `bookingsvc/employee.viewer` (per A1). Real-world schemas use wildcards for public/anonymous-access patterns; the AUZ-001 review estimated they appear in 30-50% of production schemas.

Since AUZ-001, the adapter at `internal/generator/adapter.go` preserves a `RelationView.HasWildcard bool` from `core.AllowedRelation.PublicWildcard != nil` (per A2). The current generator emits no codegen for it — `EmployeeViewerObjects { User []User }` looks identical to a non-wildcard relation. Callers cannot grant `viewer` to `user:*` through the type-safe API; they would have to bypass the stub and call `engine.CreateRelations(...)` with `[]authz.ID{"*"}`, losing the discoverability that codegen exists to provide.

Two design questions need pinning:

1. **API shape.** What does a caller write to grant a wildcard relation through generated code?
2. **Adapter data model.** Schemas can mix wildcard and non-wildcard subject types in one relation (`viewer: A | B:* | C`). The current `HasWildcard bool` cannot represent this — we'd know "some type is a wildcard" but not which.

This ADR picks both. Read-side support (how do you discover whether a wildcard tuple exists when reading relations?) is deferred — see Consequences Negative.

## Options

### Option A — Public-prefixed methods on the resource

Generate paired methods per (relation, wildcard-eligible type):

```go
func (e Employee) PublishViewerToUser(ctx context.Context) error
func (e Employee) UnpublishViewerFromUser(ctx context.Context) error
```

The verb "publish" is opinionated (alternatives: `MakeViewerPublicToUser`, `GrantViewerToAllUsers`). Each wildcard-eligible type adds two new methods.

### Option B — Magic ID constant

Expose a wildcard ID constant per type and reuse the existing `Create<Rel>Relations` flow:

```go
var UserAll User = "*"
employee.CreateViewerRelations(ctx, EmployeeViewerObjects{
    User: []User{UserAll},
})
```

Zero new methods; the wildcard is a special-valued ID. Callers can also accidentally pass `UserAll` to `Check<Permission>(ctx, CheckXInputs{User: []User{UserAll}})` — Go's type system cannot distinguish a wildcard from a concrete ID (per A3).

### Option C — Wildcards sub-struct on the existing Objects struct

Extend the per-relation Objects struct with a Wildcards field that opts in per-type:

```go
type EmployeeViewerObjects struct {
    User      []User
    Wildcards EmployeeViewerWildcards
}
type EmployeeViewerWildcards struct {
    User bool
}

employee.CreateViewerRelations(ctx, EmployeeViewerObjects{
    Wildcards: EmployeeViewerWildcards{User: true},
})
```

Adds one new struct type per relation that has any wildcard-eligible type; bool field per such type. Symmetric across Create and Delete.

### Option D — Engine-level Grant/RevokePublic methods

Add to the `Engine` interface in `pkg/authz/`; no generated-code change:

```go
authz.GetEngine(ctx).GrantPublicRelation(
    ctx,
    authz.Resource{Type: TypeEmployee, ID: authz.ID("emp1")},
    authz.Relation(EmployeeViewer),
    TypeUser,
)
```

Caller assembles Resource and Type by hand. The schema-to-Go discoverability ("which relations on Employee accept wildcards?") is lost — caller must read the `.zed` source or remember.

## Options Comparison

| Driver | A: Public methods | B: Magic ID | C: Wildcards sub-struct | D: Engine method |
|--------|-------------------|-------------|-------------------------|------------------|
| Type safety vs concrete IDs | Compile + runtime | Runtime only | Compile + runtime | N/A |
| IDE discoverability | Strong | Weak (grep) | Strong | Weak |
| Symmetric Create/Delete | Two methods | Reuses existing | Reuses existing | Two methods |
| Mixed wildcard + concrete in one call | Two calls | Single slice | Single struct | Two calls |
| Codegen template change | Medium | Tiny | Small | Zero |
| Adapter data model change | Per-type list | Per-type list | Per-type list | Per-type list |
| Read-side extension path | Add Read method | Magic ID in slice | Add to struct | Add Engine method |
| Caller cognitive cost | Pick verb name | Remember constant | Discover Wildcards field | Assemble args |

## Decision

The generator emits a `<Resource><Relation>Wildcards` sub-struct on the existing `<Resource><Relation>Objects` struct, with one `bool` field per wildcard-eligible subject type, consumed symmetrically by `Create<Rel>Relations` and `Delete<Rel>Relations`. The adapter changes `RelationView.AllowedTypes []string` to `[]AllowedType` where `AllowedType{Namespace string, IsWildcard bool}` so per-type wildcard data survives end-to-end.

## Consequences

**Consequences Positive**

- The type system distinguishes wildcard from concrete-ID grants. A caller cannot accidentally pass a wildcard marker to `CheckView` because `CheckEmployeeViewInputs` has no `Wildcards` field — only Create/Delete grow one. The engine itself enforces the same rule server-side (per A5: `CheckPermission` rejects `ObjectId: "*"` with `WildcardNotAllowedErr`), so the type-safety claim has both compile-time and runtime backing.
- IDE autocomplete on `EmployeeViewerObjects{}` reveals both `User` (slice) and `Wildcards` (struct), so a developer reading the generated stub discovers the wildcard option without consulting the schema.
- `Create<Rel>Relations` and `Delete<Rel>Relations` stay symmetric: the same Wildcards field reads true to grant, true to revoke. No new methods to remember.
- A relation declaring `viewer: A | B:* | C` produces `Objects { A []A, B []B, C []C, Wildcards { B bool } }` — the Wildcards struct opts in only for types that are actually wildcard-eligible. Mixed schemas express cleanly.

**Consequences Negative**

- Read-side support is unspecified by this ADR. `Read<Rel><Type>Relations(ctx) ([]Type, error)` currently returns concrete IDs only; whether the relation is granted to `*` is not surfaced. SpiceDB's `ReadRelationships` returns the wildcard tuple with `Subject.Object.ObjectId == "*"` unchanged (per A3), so the data is available — only the API shape is open. **ADR-003 ships in tandem with this ADR's job to pin the read-side**, evaluating three candidate shapes: sentinel `Type("*")` mixed into the slice, paired `(ids []Type, isPublic bool, err error)`, or sibling `Read<Rel><Type>Wildcard(ctx) (bool, error)`. The implementation order is ADR-002 → ADR-003 → AUZ-003 (covering both decisions). Without the read-side ADR, the grant API ships with a discoverable but not-yet-readable hole; users will hit it within hours of adoption.
- The adapter signature change (`AllowedTypes []string` → `[]AllowedType`) is a breaking change to one internal struct. `internal/generator/generator.go` consumes `RelationView.AllowedTypes` in `relationFromView` and the template iterates `$rel.AllowedTypes` in 4 places — both must update in lockstep with the adapter change.
- The Wildcards bool encoding loses one bit of state vs richer alternatives: it can express "grant or not" but not "delete only the wildcard, leave concrete grants alone in a single call" without an extra round-trip. A caller wanting "revoke wildcard but keep User=[alice, bob]" must compose Create/Delete carefully — no template-level guard prevents passing `Wildcards{User: false}` to Create and silently no-oping (per A4).
- The runtime engine at `pkg/authz/spicedb/crud.go:CreateRelations` already accepts arbitrary `authz.ID` strings, so the wildcard write works wire-level with `[]authz.ID{"*"}`. The wildcard literal must be re-exported from `pkg/authz` as `authz.WildcardID = "*"` (or equivalent typed constant) and consumed via that name in the template. Importing `github.com/authzed/spicedb/pkg/tuple` into the generated code to reach `tuple.PublicWildcard` directly would pull spicedb's transitive closure into every consumer binary just to read a constant — violating the runtime-vs-build-time boundary that `.golangci.yml`'s `pkg-no-internal` rule defends in the opposite direction. The codegen template now constructs the wildcard slice internally; bugs in the template path (e.g. the `Wildcards.User` branch forgetting to handle empty resource ID) produce wire-correct but semantically wrong tuples that SpiceDB accepts silently.
- Operator policy ("only grant wildcards on relations referenced in read permissions" — per A7) is not enforced. The codegen has the data needed to enforce it: AUZ-002's resolver knows which permissions each relation flows into, so an adapt-time check could refuse `Wildcards{...: true}` on relations that flow into write-named permissions. **Deferred to a future "wildcard guardrails" ADR**; this ADR ships with README documentation as the only surface for the warning. The deferral is deliberate — the heuristic for "what counts as a write permission" (name pattern? annotation? schema convention?) is a separate design question that does not block the grant API.

## Assumptions

- **A1 [VERIFIED]:** The existing fixture uses a wildcard relation. Evidence: `example/schema.zed` line 21 (`relation viewer: bookingsvc/user:*`); generated output at `example/authzed/bookingsvc/employee.gen.go` confirms today's codegen drops the wildcard data without emitting any wildcard-grant API.
- **A2 [VERIFIED]:** The adapter preserves wildcard data on `RelationView.HasWildcard`. Evidence: `internal/generator/adapter.go:flattenAllowedTypes` (line ~104) sets `hasWildcard = true` when `ar.GetPublicWildcard() != nil`.
- **A3 [EXTERNAL FACT]:** SpiceDB accepts `ObjectId: "*"` on `SubjectReference` as the wildcard marker for `WriteRelationships` operations. The constant is exported as `tuple.PublicWildcard = "*"`. Evidence: `github.com/authzed/spicedb@v1.52.0/pkg/tuple/structs.go:20` (constant); `internal/relationships/validation.go:195-201` (write-side validation constructs `AllowedPublicNamespaceWithCaveat` when `rel.Subject.ObjectID == tuple.PublicWildcard`). `ReadRelationships` returns the wildcard tuple with `Subject.Object.ObjectId == "*"` unchanged.
- **A4 [HYPOTHESIS]:** Callers will not commonly need a single-call atomic operation like "revoke the wildcard, keep concrete grants" or "grant wildcard, keep concrete subjects untouched". Verification deferred — informed by the existing API which only supports bulk Create or bulk Delete; if the assumption breaks, a follow-up ADR introduces granular operations.
- **A5 [EXTERNAL FACT]:** SpiceDB's `CheckPermission` handler rejects wildcard subjects with `WildcardNotAllowedErr`. The engine validates this server-side: a CheckPermission call with `ObjectId: "*"` cannot succeed; wildcards are resolved server-side by matching concrete subject IDs against granted wildcard tuples. Evidence: `github.com/authzed/spicedb@v1.52.0/internal/graph/check.go` — `if req.Subject.ObjectId == tuple.PublicWildcard { return checkResultError(NewWildcardNotAllowedErr(...)) }`. Implication: the codegen Decision (no `Wildcards` field on `Check<X>Inputs`) is enforced by the engine, not just by Go's type system.
- **A6 [EXTERNAL FACT]:** Caveats are prohibited on wildcard subjects. SpiceDB rejects writes of the form `user:* with somecaveat` at validation time. Evidence: `github.com/authzed/spicedb@v1.52.0/internal/relationships/validation.go:208-241` — wildcard-subject writes bypass caveat flexibility and require exact `HasAllowedRelation` schema match without caveats. Implication: when caveat codegen lands (separate future ADR), the template must skip the Wildcards-vs-caveat combination at adapt time.
- **A7 [EXTERNAL FACT]:** The official AuthZED documentation recommends granting wildcards only to relations referenced in *read* permissions, not write permissions, to prevent universal write access. Evidence: https://authzed.com/docs/spicedb/concepts/schema (wildcard section). Implication: this ADR's API design does not enforce the recommendation — `Create<Rel>Relations` accepts `Wildcards{...: true}` regardless of whether `<Rel>` flows into a read or write permission. The constraint is operator policy; surfacing it in README documentation is sufficient.
- **A8 [VERIFIED]:** The `authzed-go` v1.9.0 client exposes no wildcard helpers, constants, or constructors. Evidence: code grep across `github.com/authzed/authzed-go@v1.9.0/` returned zero matches for wildcard-related symbols outside the proto-generated types. Implication: this ADR is free to define its own API pattern without diverging from an upstream convention.

## History

_Binary-owned by `harness history-update`. Do not hand-edit._
