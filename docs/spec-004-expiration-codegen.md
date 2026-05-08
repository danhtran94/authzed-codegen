# [SPEC-004] Expiration Codegen

| Field        | Value                                          |
|--------------|------------------------------------------------|
| Status       | Draft                                          |
| Created      | 2026-05-09                                     |
| Author       | Danh Tran                                      |
| Implements   | (extends ADR-001 rejection list — `with expiration`) |

---

## Overview

This SPEC defines the codegen and runtime changes needed to accept `with expiration` (and combined `with <caveat> and expiration`) on relation allowed types. Relations declared with the trait carry per-tuple `OptionalExpiresAt` timestamps; SpiceDB filters expired tuples server-side from Check / Lookup / Read evaluations and garbage-collects them on a background schedule. The codegen surfaces the timestamp as a typed `*time.Time` field per allowed type on a new `Expirations` sub-struct that mirrors the existing `Wildcards` and `Caveats` sub-structs introduced by AUZ-003 and AUZ-007.

**What this component does:** Accept the `use expiration` directive and the `with expiration` clause in `flattenAllowedTypes`. Add an `IsExpiring bool` field to `AllowedType`. Generate an `Expirations <RelName>Expirations` sub-struct on `<Rel>Objects` for any relation whose allowed types include at least one `with expiration`, with one `<TypeName> *time.Time` field per expiring allowed type (using the same `IDFieldName` disambiguation rule as `Caveats`). Add `Engine.CreateRelationsWithExpiration(ctx, to, relation, subject, ids, caveatName, caveatParams, expiresAt) error` to the runtime interface — the single new method covers the three combinations (expiration-only, caveat+expiration, expiration with no caveat-name). The generated `Create<Rel>Relations` routes per allowed type: expiration-bearing branches issue `OPERATION_TOUCH` via the new engine method (per A2 — TOUCH is required when an expired-not-yet-GC'd tuple may collide on identity); non-expiring branches stay on the existing `CreateRelations` / `CreateRelationsWithCaveat` paths.

**What this component does not do:** Modify Check / Lookup / Read paths — SpiceDB filters expired tuples server-side; the engine and generated code don't care (per A4). Surface `OptionalExpiresAt` from `Read<Rel><Type>Relations` responses (analogous to the AUZ-007 Gap C deferral for caveat metadata — see AUZ-007 Discoveries). Expose `OPERATION_DELETE` semantics on expiration-bearing relations beyond what already works (DELETE matches by 6-column tuple identity; expiration metadata is irrelevant to the match per AUZ-007 SPEC-003 A3). Surface `Constraints` as a unified sub-struct combining caveat + expiration — `Caveats` and `Expirations` stay parallel sub-structs on `<Rel>Objects` so each does one thing (per Design Decision below). Provide a server-side-time fallback or relative-time helpers — caller passes an absolute `time.Time` directly. Expose the SpiceDB experimental flag — schema authors enable expiration via `use expiration`, the codegen reads what `compiler.Compile()` produces.

---

## Interface Contracts

### `Engine` interface — new method

`pkg/authz/authz.go`:

```go
type Engine interface {
    // ... existing methods unchanged ...
    CreateRelationsWithExpiration(
        ctx context.Context,
        to Resource,
        relation Relation,
        subject Type,
        ids []ID,
        caveatName string,
        caveatParams map[string]any,
        expiresAt time.Time,
    ) error
}
```

The single new method covers three call shapes via parameter sentinels:

| Caller intent | `caveatName` | `caveatParams` | `expiresAt` |
|---|---|---|---|
| Expiration only | `""` | `nil` | required |
| Caveat + expiration | non-empty | optional (nil = defer) | required |
| Caveat only (existing path) | — | — | use `CreateRelationsWithCaveat` instead |
| Neither (existing path) | — | — | use `CreateRelations` instead |

`expiresAt` is `time.Time` (not `*time.Time`) — the method is only called when the relation has expiration, so a real value is always required. The codegen never invokes it without a timestamp; passing `time.Time{}` (zero) would produce a stored expiration of `0001-01-01T00:00:00Z`, which is in the past and thus immediately filtered. Documented in Constraints C2.

### `*spicedb.Engine` implementation

`pkg/authz/spicedb/crud.go`:

```go
func (e *Engine) CreateRelationsWithExpiration(
    ctx context.Context,
    to authz.Resource,
    relation authz.Relation,
    subject authz.Type,
    ids []authz.ID,
    caveatName string,
    caveatParams map[string]any,
    expiresAt time.Time,
) error {
    e.debugLog("Creating expiring relations: to=%v, relation=%v, subject=%v, ids=%v, caveat=%s, expiresAt=%v",
        to, relation, subject, ids, caveatName, expiresAt)

    var caveatCtx *structpb.Struct
    if caveatName != "" {
        var err error
        caveatCtx, err = serializeCaveatMap(caveatParams)
        if err != nil {
            return fmt.Errorf("serialize caveat params: %w", err)
        }
    }

    expiresPb := timestamppb.New(expiresAt)

    updates := make([]*v1.RelationshipUpdate, 0, len(ids))
    for _, id := range ids {
        rel := &v1.Relationship{
            Resource: &v1.ObjectReference{
                ObjectType: string(to.Type),
                ObjectId:   string(to.ID),
            },
            Relation: string(relation),
            Subject: &v1.SubjectReference{
                Object: &v1.ObjectReference{
                    ObjectType: string(subject),
                    ObjectId:   string(id),
                },
            },
            OptionalExpiresAt: expiresPb,
        }
        if caveatName != "" {
            rel.OptionalCaveat = &v1.ContextualizedCaveat{
                CaveatName: caveatName,
                Context:    caveatCtx,
            }
        }
        updates = append(updates, &v1.RelationshipUpdate{
            Operation:    v1.RelationshipUpdate_OPERATION_TOUCH,
            Relationship: rel,
        })
    }

    res, err := e.client.WriteRelationships(ctx, &v1.WriteRelationshipsRequest{Updates: updates})
    if err != nil {
        return err
    }
    e.setToken(res.WrittenAt.Token)
    return nil
}
```

`OPERATION_TOUCH` is hard-coded — per A2, expiring relations require TOUCH. Imports: `google.golang.org/protobuf/types/known/timestamppb` (already a transitive dep via `authzed-go`) and `time` from stdlib.

### Generated `<Rel>Objects` struct — new `Expirations` sub-struct

For a relation with at least one `with expiration` allowed type, `<Rel>Objects` gains an `Expirations <RelName>Expirations` field grouped after `Caveats` (which sits after `Wildcards`, which sits after the ID-slice fields). The sub-struct holds one `<TypeName> *time.Time` field per expiring allowed type, using the same `IDFieldName` disambiguation rule as `Caveats` (see SPEC-003 — when `(Namespace, IsWildcard)` collides with distinct caveats, fields are suffixed; same rule applies if expiration collides).

Single expiring allowed type (`relation token: extsvc/user with expiration`):

```go
type FolderTokenObjects struct {
    User        []User
    Expirations FolderTokenExpirations
}
type FolderTokenExpirations struct {
    User *time.Time
}
```

Combined caveat + expiration (`relation gated_token: extsvc/user with extsvc/tenant_match and expiration`):

```go
type FolderGatedTokenObjects struct {
    User        []User
    Caveats     FolderGatedTokenCaveats
    Expirations FolderGatedTokenExpirations
}
type FolderGatedTokenCaveats struct {
    User *TenantMatchArgs
}
type FolderGatedTokenExpirations struct {
    User *time.Time
}
```

Mixed (`relation member: user with expiration | group`):

```go
type FolderMemberObjects struct {
    User        []User
    Group       []Group
    Expirations FolderMemberExpirations
}
type FolderMemberExpirations struct {
    User *time.Time   // only the expiring branch appears
}
```

The `Expirations` sub-struct is **separate from** `Caveats` — each does one thing, mirroring how `Wildcards` and `Caveats` are independent. Combination is achieved by populating both sub-structs.

### Generated `Create<Rel>Relations` method — per-type routing

For each allowed type the body branches on three flags: caveat-bearing, expiring, or both. The four resulting routes each call exactly one engine method:

| Allowed-type flags | Route |
|---|---|
| Neither | `engine.CreateRelations(...)` (existing) |
| Caveat only | `engine.CreateRelationsWithCaveat(...)` (AUZ-007) |
| Expiration only | `engine.CreateRelationsWithExpiration(..., "", nil, expiresAt)` (NEW) |
| Caveat + expiration | `engine.CreateRelationsWithExpiration(..., caveatName, caveatCtx, expiresAt)` (NEW, same method) |

Generated body for the combined case:

```go
func (folder Folder) CreateGatedTokenRelations(ctx context.Context, objects FolderGatedTokenObjects) error {
    if len(objects.User) > 0 {
        var caveatCtx map[string]any
        if c := objects.Caveats.User; c != nil {
            caveatCtx = map[string]any{}
            if c.Tenant != nil {
                caveatCtx["tenant"] = *c.Tenant
            }
        }
        var expiresAt time.Time
        if e := objects.Expirations.User; e != nil {
            expiresAt = *e
        }
        err := authz.GetEngine(ctx).CreateRelationsWithExpiration(
            ctx,
            authz.Resource{Type: TypeFolder, ID: authz.ID(folder)},
            authz.Relation(FolderGatedToken),
            TypeUser,
            authz.IDs(objects.User),
            "extsvc/tenant_match", caveatCtx,
            expiresAt,
        )
        if err != nil {
            return err
        }
    }
    return nil
}
```

If `objects.Expirations.User` is nil, `expiresAt` is the zero `time.Time`. The wire stores `OptionalExpiresAt: 0001-01-01T00:00:00Z` (already past), so the tuple is filtered out at evaluation. Caller error — codegen doesn't gate this since the field is optional from the typed-struct perspective; runtime tests catch the zero-time mistake. Same trade-off as AUZ-007's permissive-nil semantics on caveat fields.

### Wildcard + expiration

Schema may declare `relation foo: extsvc/user:* with expiration`. The wildcard branch in `Create<Rel>Relations` mirrors the regular branch's expiration-aware routing:

```go
if objects.Wildcards.User {
    var expiresAt time.Time
    if e := objects.Expirations.User; e != nil {
        expiresAt = *e
    }
    err := authz.GetEngine(ctx).CreateRelationsWithExpiration(
        ctx, ..., []authz.ID{authz.WildcardID}, "", nil, expiresAt,
    )
    // ...
}
```

The same `Expirations.User` field is consumed by both branches when the allowed type is both wildcard and expiring (parallel to the AUZ-007 SPEC-003 C10 wildcard+caveat treatment).

### Adapter — accept `with expiration`

`internal/generator/adapter.go` `flattenAllowedTypes`:

```go
// BEFORE (current — AUZ-006/AUZ-007 era):
if ar.GetRequiredExpiration() != nil {
    return nil, fmt.Errorf("expiration traits are not supported (allowed type %q)", ar.GetNamespace())
}

// AFTER (SPEC-004):
isExpiring := ar.GetRequiredExpiration() != nil
// ... continues into AllowedType{} construction with the new IsExpiring field
```

New field on `AllowedType`:

```go
type AllowedType struct {
    Namespace       string
    IsWildcard      bool
    CaveatName      string
    IsExpiring      bool   // NEW
    IDFieldName     string
    CaveatFieldName string
    // (no separate ExpirationFieldName — IDFieldName disambiguation already handles
    //  same-namespace collisions; the Expirations sub-struct uses IDFieldName too)
}
```

### Template `FuncMap` — new `anyExpiring` helper

Mirrors `anyCaveat` and `anyWildcard`:

```go
"anyExpiring": func(types []AllowedType) bool {
    for _, t := range types {
        if t.IsExpiring {
            return true
        }
    }
    return false
},
```

Used to gate `Expirations` sub-struct emission. No other template helpers needed.

### `cmd/authzed-codegen/main.go` — no change

The CLI wiring established in AUZ-006 (`compiler.Compile()` already returns expiration metadata on `AllowedRelation.RequiredExpiration`) is sufficient. SPEC-004 reads the same field.

---

## Sequence

Runtime flow when a caller writes an expiring tuple:

```
caller code:

    folder.CreateGatedTokenRelations(ctx, extsvc.FolderGatedTokenObjects{
        User: []User{"u1"},
        Caveats: extsvc.FolderGatedTokenCaveats{
            User: &TenantMatchArgs{Tenant: new("acme")},
        },
        Expirations: extsvc.FolderGatedTokenExpirations{
            User: timePtr(time.Now().Add(1 * time.Hour)),
        },
    })
         │
         ▼
generated method body — per allowed type:

    for AllowedType{Namespace="extsvc/user", CaveatName="extsvc/tenant_match", IsExpiring=true}:
         │
         ├─► if len(objects.User) == 0          → skip branch
         │
         ├─► caveatCtx := nil
         │   if objects.Caveats.User != nil:
         │     caveatCtx = map[string]any{"tenant": *objects.Caveats.User.Tenant}
         │
         ├─► expiresAt := time.Time{}
         │   if objects.Expirations.User != nil:
         │     expiresAt = *objects.Expirations.User
         │
         └─► engine.CreateRelationsWithExpiration(
                 ctx,
                 authz.Resource{Type: TypeFolder, ID: authz.ID(folder)},
                 authz.Relation(FolderGatedToken),
                 TypeUser,
                 authz.IDs([]User{"u1"}),
                 "extsvc/tenant_match", caveatCtx,
                 expiresAt,
             )
                  │
                  ▼
*spicedb.Engine.CreateRelationsWithExpiration:
                  │
                  ├─► caveatCtx_struct, _ := serializeCaveatMap(caveatParams)  (if caveatName != "")
                  │
                  ├─► expiresPb := timestamppb.New(expiresAt)
                  │
                  ├─► build *v1.RelationshipUpdate with:
                  │     Operation:        OPERATION_TOUCH    ← key difference from CreateRelationsWithCaveat
                  │     Relationship:
                  │       Resource:           {ObjectType: "extsvc/folder", ObjectId: "f1"}
                  │       Relation:           "gated_token"
                  │       Subject:            {ObjectType: "extsvc/user",   ObjectId: "u1"}
                  │       OptionalCaveat:     {CaveatName: "...", Context: ...}     (if caveat)
                  │       OptionalExpiresAt:  2026-05-09T15:30:00Z                  (always set)
                  │
                  ├─► client.WriteRelationships(ctx, &Request{Updates: [...]})
                  │
                  ├─► on success: e.setToken(res.WrittenAt.Token)
                  │
                  └─► return nil
```

For check / lookup / read: **no client-side change**. SpiceDB filters expired tuples server-side per A4. The existing `Check<Perm>` / `Lookup<Perm>*` / `Read<Rel><Type>Relations` paths just see "the tuple isn't there anymore" once expiry passes — same behavior as if the tuple had been deleted.

```
codegen flow at build time (extends AUZ-007 SPEC-003 sequence):

    template execution (per relation)
         ├─► <Rel>Objects struct
         │     ├─► <TypeName> []<TypeName> field per allowed type
         │     ├─► Wildcards sub-struct if any allowed type is wildcard
         │     ├─► Caveats sub-struct if any allowed type is caveated
         │     └─► Expirations sub-struct if any allowed type is expiring  ◀── NEW (this SPEC)
         │
         └─► Create<Rel>Relations method body
               ├─► per allowed-type branch — 4 routing cases:
               │     {neither, caveat, expiration, caveat+expiration}
               │     ├─► if expiring: build expiresAt from objects.Expirations.<Type>
               │     │     └─► call CreateRelationsWithExpiration (TOUCH)
               │     └─► else: existing routes (CreateRelations or CreateRelationsWithCaveat)
               └─► per wildcard branch — same 4 routing cases applied to wildcard write
```

---

## Errors

| Error class | Trigger | Layer |
|---|---|---|
| `"expiration traits are not supported"` (PRE-EXISTING — REMOVED) | Schema declares `with expiration`. Currently rejected at adapt time; this SPEC removes the rejection. | Adapter (`flattenAllowedTypes`) |
| `"serialize caveat params: <wrapped>"` (existing) | `serializeCaveatMap` fails on protobuf-incompatible caveat values. Reused by the combined caveat+expiration path. | Engine (`*spicedb.Engine.CreateRelationsWithExpiration`) |
| Schema-level rejection: missing `use expiration` directive | Schema declares `with expiration` without the `use expiration` directive at the top. SpiceDB compiler rejects at `compiler.Compile()` time before codegen even sees the schema. | Pre-codegen (SpiceDB compiler) — passed through unwrapped via `panic(err)` from main.go |
| `OPERATION_CREATE` rejection on un-GC'd expired tuple | If `Create<Rel>Relations` issued OPERATION_CREATE against a tuple identity that has an un-garbage-collected expired tuple, SpiceDB errors. SPEC-004 avoids this by always using TOUCH for expiring relations (per A2). For NON-expiring relations the existing CREATE path is unchanged. | (Avoided by design — TOUCH-on-expiring routing) |
| SpiceDB wire rejection | `client.WriteRelationships` returns gRPC error — malformed timestamp, invalid caveat name, transport failure. | Engine — passed through unwrapped |

The codegen wrapper does NOT validate `expiresAt` is in the future. A zero-value `time.Time` (no `Expirations` field set) stores `0001-01-01T00:00:00Z`, immediately past, immediately filtered. Caller error surfaces as runtime "tuple not found in checks" — same trade-off as AUZ-007's permissive-nil on caveat fields. Tests should cover this case.

---

## Constraints

- **C1.** `OPERATION_TOUCH` is hard-coded in `CreateRelationsWithExpiration`. Per A2, expiration-bearing relations require TOUCH because un-garbage-collected expired tuples may collide on `(resource, relation, subject)` identity, and `OPERATION_CREATE` errors on existing tuples. The codegen routes expiring branches through this method automatically — caller doesn't pick the operation.

- **C2.** `expiresAt` parameter is `time.Time` (not `*time.Time`). Callers populate `Expirations.<TypeName>` as a pointer for nullability at the typed-struct surface, but the engine method takes a value because it's only called when the relation actually has expiration. The zero value `time.Time{}` is a valid Go value (`0001-01-01`) but would store an immediately-past expiration; the codegen does not gate this — runtime catches it as "tuple already expired."

- **C3.** `Expirations` sub-struct is **separate from** `Caveats`. Each does one thing. A relation with both `with cav and expiration` populates both sub-structs independently. Per Design Decision below — combining into a single `Constraints` struct was considered and rejected.

- **C4.** Field naming in `<RelName>Expirations` follows the same `IDFieldName` rule as `Caveats` — non-collision uses `<TypeName>`, collision (same `(Namespace, IsWildcard)` with distinct caveats) uses `<TypeName><CaveatPascal>`. Same disambiguation logic, same `flattenAllowedTypes` post-processing. Per A6.

- **C5.** Read / Check / Lookup paths are unchanged. SpiceDB filters expired tuples server-side per A4. The codegen makes no server-roundtrip-time changes for expiration — it's a write-only and storage-format concern from the codegen's perspective.

- **C6.** `Delete<Rel>Relations` is unchanged. SpiceDB's `OPERATION_DELETE` matches by 6-column tuple identity (per AUZ-007 SPEC-003 A3); expiration metadata is irrelevant to the match. Callers who want to remove an expiring tuple before its natural expiration call the existing `Delete<Rel>Relations` method.

- **C7.** Wildcard + expiration shares the `Expirations.<TypeName>` field with the regular branch when the allowed type is both wildcard and expiring. Two writes (regular + wildcard) consume one `*time.Time` value. Mirrors AUZ-007 SPEC-003 C10.

- **C8.** Expiration-free schemas regenerate byte-identically. All new template branches are guarded by `{{ if anyExpiring $rel.AllowedTypes }}` / `{{ if $relType.IsExpiring }}`; no expiring allowed types means zero new emission. Preserves the round-trip invariant for all existing fixtures.

- **C9.** The `use expiration` directive is a SpiceDB-side feature gate. The codegen doesn't read or emit it — `compiler.Compile()` consumes it and fails the schema if `with expiration` appears without it. The codegen sees only a successful `*core.NamespaceDefinition` with `RequiredExpiration != nil`. Per A1.

- **C10.** `Read<Rel><Type>Relations` does not surface `OptionalExpiresAt` from response tuples. Returns IDs only, matching the AUZ-007 Gap C deferral for `OptionalCaveat`. Future enhancement.

---

## Assumptions

- **A1 [VERIFIED]:** SpiceDB v1.40+ accepts `with expiration` and `use expiration` at the schema level. `core.AllowedRelation` carries `RequiredExpiration *ExpirationTrait` (an empty marker struct — its presence is the signal). Evidence: `go doc github.com/authzed/spicedb/pkg/proto/core/v1 AllowedRelation` confirms the field; `go doc … ExpirationTrait` confirms it's an empty struct; Authzed docs ([Writing Relationships that Expire](https://authzed.com/docs/spicedb/concepts/expiring-relationships)) document the `use expiration` directive requirement.

- **A2 [EXTERNAL FACT]:** `OPERATION_TOUCH` is required for expiration-bearing writes. `OPERATION_CREATE` errors when a tuple identity has an expired-but-not-yet-GC'd row at the same `(resource, relation, subject)`. Evidence: Authzed docs explicit guidance — "When working with expiring relationships, always use the TOUCH operation. If a relationship has expired but hasn't been garbage collected yet, using CREATE will return an error." Source: [Writing Relationships that Expire](https://authzed.com/docs/spicedb/concepts/expiring-relationships).

- **A3 [VERIFIED]:** `authzed-go` v1.9 `Relationship.OptionalExpiresAt` is `*timestamppb.Timestamp`. Evidence: `go doc github.com/authzed/authzed-go/proto/authzed/api/v1 Relationship` confirms the field. `timestamppb.New(time.Time)` is the standard constructor.

- **A4 [EXTERNAL FACT]:** SpiceDB filters expired tuples server-side from Check / Lookup / Read evaluations. The client (codegen) requires no changes on the read paths. Evidence: Authzed docs — "as soon as a relationship expires, it will no longer be used in permission checks." Source: [Writing Relationships that Expire](https://authzed.com/docs/spicedb/concepts/expiring-relationships).

- **A5 [EXTERNAL FACT]:** Garbage collection lag varies by datastore — Postgres / MySQL run a periodic GC job (every 5 minutes per Authzed docs); CockroachDB / Spanner use native row-expiration features (24-hour reclaim window). Tests exercise the in-memory datastore via testcontainers; behavior may differ in production. Source: Authzed docs and SpiceDB blog [Build Time-Bound Permissions with Relationship Expiration](https://authzed.com/blog/build-time-bound-permissions-with-relationship-expiration-in-spicedb).

- **A6 [VERIFIED]:** The `IDFieldName` disambiguation rule from AUZ-007 (collision on `(Namespace, IsWildcard)` with distinct caveats produces composite field names) extends naturally to expiration. An expiring allowed type uses `IDFieldName` for the `<RelName>Expirations` sub-struct field name; if the same allowed type appears with two different caveats both expiring, the existing post-processing in `flattenAllowedTypes` already disambiguates the IDFieldName itself, so no new disambiguation logic is needed. Evidence: AUZ-007 unit tests in `internal/generator/adapter_test.go` cover the disambiguation cases; this SPEC reuses the field directly.

---

## Unresolved Questions

(none)

---

## Summary

This SPEC adds expiration traits to the codegen surface. **Net change is bounded to five files:**

| File | Change |
|---|---|
| `internal/generator/adapter.go` | Add `IsExpiring bool` to `AllowedType`; remove the existing `expiration traits are not supported` rejection in `flattenAllowedTypes`; capture `RequiredExpiration != nil` into the new field. |
| `internal/generator/generator.go` | Add `anyExpiring` template helper. |
| `internal/templates/object.go.tmpl` | Emit `Expirations <RelName>Expirations` sub-struct on `<Rel>Objects` when `anyExpiring`; emit per-`IDFieldName` `*time.Time` fields. Per-allowed-type routing in `Create<Rel>Relations` body (regular + wildcard) selects between four engine methods based on caveat / expiration flags. |
| `pkg/authz/authz.go` | Add `Engine.CreateRelationsWithExpiration` to the interface (single new method covering expiration-only and caveat+expiration). |
| `pkg/authz/spicedb/crud.go` | Implement `*Engine.CreateRelationsWithExpiration` — builds `RelationshipUpdate{Operation: OPERATION_TOUCH, Relationship{...OptionalCaveat, OptionalExpiresAt}}`. Reuses `serializeCaveatMap`. Imports `time` and `google.golang.org/protobuf/types/known/timestamppb`. |
| `example/schema.zed` | Add `use expiration` directive at top; new fixtures: `expiring_viewer: user with expiration`, `gated_token: user with tenant_match and expiration`, optionally `wildcard_temp: user:* with expiration`. |
| `example/authzed/extsvc/folder.gen.go` | Regenerated output. |

E2E tests against live SpiceDB cover: write+check before expiry (granted), write+check after expiry (denied — server-side filter), combined caveat+expiration (both gates apply), TOUCH idempotency (re-write after GC of expired tuple), wildcard+expiration if schema supports it.
