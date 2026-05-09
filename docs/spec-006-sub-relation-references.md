# [SPEC-006] Sub-relation References

| Field      | Value                                              |
|------------|----------------------------------------------------|
| Status     | Accepted                                           |
| Created    | 2026-05-09                                         |
| Author     | Danh Tran                                          |
| Implements | (lifts ADR-001 rejection of `foo#bar`)             |

---

## Overview

This SPEC accepts sub-relation references — schema constructs of the form `relation X: Type#SubRelation` — and surfaces them through the codegen as a typed write/read API. The construct grants access via inheritance: writing `(project:p1, member, team:t1#admin)` means "anyone who has `admin` on team t1 is implicitly a `member` of project p1." SpiceDB resolves the userset chain server-side at evaluation time; the client never stores the resolved subjects. Today the adapter rejects this construct (`internal/generator/adapter.go:367`); SPEC-006 lifts the rejection and threads `SubjectReference.OptionalRelation` through write, read, and (rarely) Check paths.

**What this component does:** Capture `AllowedRelation.Relation` (the sub-relation name) into a new `AllowedType.SubRelation string` field. Extend disambiguation to a 3-key tuple `(Namespace, IsWildcard, SubRelation)` so a schema like `relation member: team#admin | team#owner` produces distinct `TeamAdmin` and `TeamOwner` fields. Generate a write field per userset allowed type — `<TypeName><PascalSubRelation> []<TypeName>` — and route writes through a new `Engine.CreateRelationsToUserset` method that issues `Relationship.Subject{Object, OptionalRelation}`. Update `<Rel><Type>Relation` (the AUZ-010 metadata struct) with a `SubRelation string` field; non-empty marks the row as a userset reference. Surface userset Check inputs via a parallel `<TypeName><PascalSubRelation> []<TypeName>` field on `Check<Perm>Inputs` and route through a new `Engine.CheckPermissionUserset` method when the caller populates that field.

**What this component does not do:** Lookup with userset *results* — `LookupSubjects` returning userset triples instead of just IDs. Server-side this would change the return shape from `[]ID` to `[]<Userset>` for any permission reaching a userset allowed type; that's a heavier change deferred to a follow-up. Lookup with userset *inputs* (via `LookupResources`) is also deferred — the request shape supports it but the typed Check-style routing on Lookup has open API questions. Auto-resolve the userset to its constituent IDs at read time — the codegen reads what SpiceDB returns (Team IDs marked with sub-relation), the userset expansion only happens at Check time. Validate that the referenced sub-relation actually exists on the target definition — `compiler.Compile()` already validates schemas and rejects references to undefined relations/permissions before codegen runs.

---

## Interface Contracts

### Adapter — `internal/generator/adapter.go`

`AllowedType` gains one new field; the existing rejection is removed.

```go
type AllowedType struct {
    Namespace       string
    IsWildcard      bool
    CaveatName      string
    IsExpiring      bool
    SubRelation     string  // NEW: empty for direct subjects, non-empty for userset references
    IDFieldName     string
    CaveatFieldName string
}
```

`flattenAllowedTypes` change:

```go
// BEFORE (current):
if rel := ar.GetRelation(); rel != "" && rel != ellipsisRelation {
    return nil, fmt.Errorf("sub-relation references are not supported (%s#%s)", ar.GetNamespace(), rel)
}

// AFTER (SPEC-006):
subRelation := ""
if rel := ar.GetRelation(); rel != "" && rel != ellipsisRelation {
    subRelation = rel
}

types = append(types, AllowedType{
    Namespace:   ar.GetNamespace(),
    IsWildcard:  ar.GetPublicWildcard() != nil,
    CaveatName:  caveatName,
    IsExpiring:  isExpiring,
    SubRelation: subRelation,
})
```

The existing disambiguation post-processing extends to a 3-key group: `(Namespace, IsWildcard, SubRelation)`. Per A1 — wildcards on userset types aren't allowed by SpiceDB grammar (`team:*#admin` is illegal), so `(IsWildcard=true, SubRelation!="")` is unreachable and the post-processing handles the same collision shape it already does. The IDFieldName composition for usersets is `<TypeName><PascalSubRelation>` (e.g. `team#admin` → `TeamAdmin`); for userset+caveat collisions the existing caveat suffix appends.

### Runtime — `pkg/authz/authz.go`

`RelationTuple` gains one field. New write and check methods on the `Engine` interface.

```go
type RelationTuple struct {
    ID            ID
    SubRelation   string             // NEW: empty for direct subjects, non-empty for userset references
    CaveatName    string
    CaveatContext map[string]any
    ExpiresAt     *time.Time
}

type Engine interface {
    // ... existing methods unchanged ...
    CreateRelationsToUserset(ctx context.Context,
        to Resource, relation Relation,
        subjectType Type, ids []ID, subRelation string,
        caveatName string, caveatParams map[string]any,
        expiresAt time.Time,
    ) error
    CheckPermissionUserset(ctx context.Context,
        dest Resource, has Permission,
        subjectType Type, ids []ID, subRelation string,
        caveatParams map[string]any,
    ) error
    // ... existing methods unchanged ...
}
```

`CreateRelationsToUserset` covers all four userset combinations via sentinels (mirrors AUZ-009's `CreateRelationsWithExpiration` pattern):

| Caller intent | `caveatName` | `caveatParams` | `expiresAt` |
|---|---|---|---|
| Plain userset | `""` | `nil` | zero `time.Time{}` |
| Userset + caveat | non-empty | optional (nil = defer to check) | zero |
| Userset + expiration | `""` | `nil` | required |
| Userset + caveat + expiration | non-empty | optional | required |

The auto-TOUCH semantic from AUZ-009 applies when `expiresAt` is non-zero — same wire-level reasoning (un-GC'd expired tuples may collide on identity).

`CheckPermissionUserset` accepts `caveatParams` as a map (nil = no request-time caveat context, matching `CheckPermissionWithCaveat`'s nil-allowed pattern).

### `*spicedb.Engine` implementation — `pkg/authz/spicedb/crud.go`

`CreateRelationsToUserset` builds `RelationshipUpdate` with `Subject.OptionalRelation = subRelation` set. When `subRelation == ""` the method behaves identically to `CreateRelations` / `CreateRelationsWithCaveat` / `CreateRelationsWithExpiration` (depending on the other sentinels); the codegen never calls it with empty `subRelation` because non-userset writes route through the existing methods.

```go
func (e *Engine) CreateRelationsToUserset(
    ctx context.Context,
    to authz.Resource, relation authz.Relation,
    subjectType authz.Type, ids []authz.ID, subRelation string,
    caveatName string, caveatParams map[string]any,
    expiresAt time.Time,
) error {
    e.debugLog("Creating userset relations: to=%v, relation=%v, subject=%v#%v, ids=%v, caveat=%s, expiresAt=%v",
        to, relation, subjectType, subRelation, ids, caveatName, expiresAt)

    var caveatCtx *structpb.Struct
    if caveatName != "" {
        var err error
        caveatCtx, err = serializeCaveatMap(caveatParams)
        if err != nil {
            return fmt.Errorf("serialize caveat params: %w", err)
        }
    }

    operation := v1.RelationshipUpdate_OPERATION_TOUCH
    if expiresAt.IsZero() {
        operation = v1.RelationshipUpdate_OPERATION_TOUCH  // userset writes use TOUCH unconditionally — same idempotency rationale as AUZ-009
    }

    var expiresPb *timestamppb.Timestamp
    if !expiresAt.IsZero() {
        expiresPb = timestamppb.New(expiresAt)
    }

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
                    ObjectType: string(subjectType),
                    ObjectId:   string(id),
                },
                OptionalRelation: subRelation,
            },
        }
        if caveatName != "" {
            rel.OptionalCaveat = &v1.ContextualizedCaveat{CaveatName: caveatName, Context: caveatCtx}
        }
        if expiresPb != nil {
            rel.OptionalExpiresAt = expiresPb
        }
        updates = append(updates, &v1.RelationshipUpdate{
            Operation:    operation,
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

`CheckPermissionUserset` mirrors `CheckPermissionWithCaveat` but with `OptionalSubjectRelation` populated:

```go
func (e *Engine) CheckPermissionUserset(
    ctx context.Context,
    dest authz.Resource, has authz.Permission,
    subjectType authz.Type, ids []authz.ID, subRelation string,
    caveatParams map[string]any,
) error {
    e.debugLog("Checking userset permission: dest=%v, has=%v, subject=%v#%v, ids=%v",
        dest, has, subjectType, subRelation, ids)

    var caveatCtx *structpb.Struct
    if caveatParams != nil {
        var err error
        caveatCtx, err = serializeCaveatMap(caveatParams)
        if err != nil {
            return fmt.Errorf("serialize caveat params: %w", err)
        }
    }

    consistency := e.getConsistencySnapshot()
    for _, id := range ids {
        res, err := e.client.CheckPermission(ctx, &v1.CheckPermissionRequest{
            Consistency: consistency,
            Resource: &v1.ObjectReference{
                ObjectType: string(dest.Type),
                ObjectId:   string(dest.ID),
            },
            Permission: string(has),
            Subject: &v1.SubjectReference{
                Object: &v1.ObjectReference{
                    ObjectType: string(subjectType),
                    ObjectId:   string(id),
                },
                OptionalRelation: subRelation,
            },
            Context: caveatCtx,
        })
        if err := errorIfDenied(res, err); err != nil {
            return err
        }
    }
    return nil
}
```

`*Engine.ReadRelations` extends to populate `SubRelation` from `Subject.OptionalRelation`:

```go
// In the existing ReadRelations stream loop:
t := authz.RelationTuple{
    ID:          authz.ID(rel.Subject.Object.ObjectId),
    SubRelation: rel.Subject.OptionalRelation,  // NEW
}
// ... existing caveat + expiration mapping ...
```

### Generated `<Rel>Objects` write struct — new userset fields

For a relation with userset allowed types, the codegen emits one field per `(SubjectType, SubRelation)` combination:

```go
// Schema:
//   relation member: extsvc/user | extsvc/team#admin
type ProjectMemberObjects struct {
    User      []extsvc.User    // direct subjects
    TeamAdmin []extsvc.Team    // userset: team#admin
}
```

If a relation has wildcards alongside usersets, both sub-structs (`Wildcards`, `Caveats`, `Expirations`) coexist with the new userset fields. Userset entries can independently carry caveat or expiration metadata via the existing sub-structs — the field name `TeamAdmin` is consistent across `<Rel>Objects.TeamAdmin`, `Caveats.TeamAdmin`, and `Expirations.TeamAdmin`.

### Generated `Create<Rel>Relations` — new userset routing

Per allowed-type branch:

```go
// Direct subject branch (existing routing — unchanged):
if len(objects.User) > 0 {
    err := authz.GetEngine(ctx).CreateRelations(ctx, ..., authz.IDs(objects.User))
    // OR CreateRelationsWithCaveat / CreateRelationsWithExpiration based on flags
}

// Userset branch (NEW):
if len(objects.TeamAdmin) > 0 {
    err := authz.GetEngine(ctx).CreateRelationsToUserset(ctx, ...,
        authz.IDs(objects.TeamAdmin), "admin",
        "" /*caveatName*/, nil /*caveatParams*/, time.Time{} /*expiresAt*/,
    )
}
```

For userset + caveat / expiration the same sentinel parameters carry through — no separate methods.

### Generated `<Rel><Type>Relation` read struct — `SubRelation` field

Every metadata struct gains the `SubRelation` field. For pure direct-subject relations, the field is always empty; for mixed/userset relations, the field disambiguates rows.

```go
type ProjectMemberUserRelation struct {
    ID            extsvc.User
    SubRelation   string             // always ""
    CaveatName    string
    CaveatContext map[string]any
    ExpiresAt     *time.Time
}
type ProjectMemberTeamRelation struct {
    ID            extsvc.Team
    SubRelation   string             // always "admin" for this branch
    CaveatName    string
    CaveatContext map[string]any
    ExpiresAt     *time.Time
}
```

`Read<Rel><Type>Relations` filters by `SubjectType` server-side; reading `ReadMemberTeamRelations` returns only the userset rows (Team subjects), not the direct user rows.

### Generated `Check<Perm>Inputs` — new userset fields

Mirrors the write struct:

```go
type CheckProjectViewInputs struct {
    User      []extsvc.User    // direct subjects (common case)
    TeamAdmin []extsvc.Team    // userset subjects (rare case — "does t1#admin have view?")
    Caveats   CheckProjectViewCaveats  // unchanged from AUZ-006/007
}
```

Generated body — when caller populates `TeamAdmin`, route through `CheckPermissionUserset`:

```go
if len(input.TeamAdmin) > 0 {
    err := authz.GetEngine(ctx).CheckPermissionUserset(ctx, ...,
        TypeTeam, authz.IDs(input.TeamAdmin), "admin", caveatCtx,
    )
    if err != nil { return false, err }
}
```

Per A2 — SpiceDB's userset-as-subject Check returns `HAS_PERMISSION` only when the literal userset reference is granted (no recursive expansion of the userset's membership), which is the documented semantic for "does this group have this permission?"

---

## Sequence

Wire flow for a userset write:

```
caller code:

    project.CreateMemberRelations(ctx, ProjectMemberObjects{
        TeamAdmin: []extsvc.Team{"t1"},
    })
         │
         ▼
generated method body:

    ├─► for AllowedType{Namespace="extsvc/team", SubRelation="admin"}:
    │     ├─► engine.CreateRelationsToUserset(
    │     │       ctx, project resource, "member",
    │     │       TypeTeam, [t1], "admin",
    │     │       "", nil, time.Time{},
    │     │   )

         │
         ▼
*spicedb.Engine.CreateRelationsToUserset:

    ├─► build *v1.RelationshipUpdate with:
    │     Operation:   OPERATION_TOUCH
    │     Relationship:
    │       Resource:           {project, p1}
    │       Relation:           "member"
    │       Subject:
    │         Object:           {extsvc/team, t1}
    │         OptionalRelation: "admin"          ← key difference
    │
    └─► client.WriteRelationships(...) → setToken
```

Wire flow for a Check that walks through the userset:

```
caller: project.CheckView(ctx, CheckProjectViewInputs{User: []User{"u1"}})
         │
         ▼
generated body — direct user input:
    ├─► engine.CheckPermission(ctx, project p1, "view", TypeUser, [u1])

         │
         ▼
SpiceDB evaluator (server-side):

    project:p1 #view                           ← entry point
        │
        ├─► resolves to: project:p1 #member
        │     ├─► reads p1.member tuples
        │     │    finds: (project:p1, member, extsvc/team:t1#admin)
        │     │
        │     └─► walks the userset reference t1#admin
        │           ├─► t1#admin is a permission → expands to t1#owner ∪ t1#manager
        │           ├─► reads (extsvc/team:t1, owner, extsvc/user:?)
        │           │    finds u1
        │           └─► match → HAS_PERMISSION ✓
        │
        └─► returns HAS_PERMISSION
```

Wire flow for the rare userset-as-subject Check ("does t1#admin have view?"):

```
caller: project.CheckView(ctx, CheckProjectViewInputs{TeamAdmin: []Team{"t1"}})
         │
         ▼
generated body — userset input:
    ├─► engine.CheckPermissionUserset(ctx, project p1, "view",
    │       TypeTeam, [t1], "admin", nil)

         │
         ▼
*spicedb.Engine.CheckPermissionUserset:
    ├─► CheckPermissionRequest with Subject{Object: {team, t1}, OptionalRelation: "admin"}

         │
         ▼
SpiceDB evaluator:
    project:p1 #view → project:p1 #member
        ├─► reads tuples; finds (project:p1, member, team:t1#admin)
        ├─► subject matches subject t1#admin literally
        └─► HAS_PERMISSION (does NOT walk into t1's admin membership)
```

---

## Errors

| Error class | Trigger | Layer |
|---|---|---|
| `"sub-relation references are not supported"` (PRE-EXISTING — REMOVED) | Schema declares `T#R`. Currently rejected at adapt time; this SPEC removes the rejection. | Adapter (`flattenAllowedTypes`) |
| Schema-level rejection: undefined sub-relation | Schema declares `relation X: team#admin` but `team` has no `admin` relation or permission. `compiler.Compile()` rejects before codegen sees the schema. | Pre-codegen (SpiceDB compiler) — passed through unwrapped via `panic(err)` from main.go |
| Wildcard + userset collision | Schema attempts `team:*#admin`. SpiceDB grammar disallows this combination per A1; rejected at parse time. | Pre-codegen |
| `"serialize caveat params: <wrapped>"` (existing) | `serializeCaveatMap` fails on protobuf-incompatible caveat values. Reused by both userset write+check paths. | Engine |
| SpiceDB wire rejection | `client.WriteRelationships` / `client.CheckPermission` returns gRPC error — malformed sub-relation, type mismatch with declared schema, transport failure. | Engine — passed through unwrapped |
| `authz.ErrPermissionDenied` | `CheckPermissionUserset` returns this when the userset is NOT literally granted on the resource (no recursive expansion semantics — see Sequence). | Engine via `errorIfDenied` |

---

## Constraints

- **C1.** `SubRelation` is a string (not `*string`). Empty string means "direct subject" — distinguishable from any real sub-relation name (SpiceDB sub-relation names are non-empty by grammar). Mirrors `CaveatName`'s shape from AUZ-010 SPEC-005 C2.

- **C2.** `Engine.CreateRelationsToUserset` always issues `OPERATION_TOUCH`. Per A3 — userset writes have the same expired-collision concern as AUZ-009 expiration writes (TOUCH is idempotent, CREATE errors on existing identity). Even when no expiration is set, TOUCH costs nothing extra and avoids divergence between the userset and non-userset paths' operation choice.

- **C3.** Disambiguation key extends to `(Namespace, IsWildcard, SubRelation)`. The existing post-processing in `flattenAllowedTypes` extends to this 3-key group with no new logic — same caveat-suffix rules apply when the 3-key collides on multiple distinct caveats.

- **C4.** Userset write field naming: `<TypeName><PascalSubRelation>` (e.g. `extsvc/team#admin` → `TeamAdmin`). Per A4 — the existing `pascalCaveatName` helper generalizes to sub-relation pascalization (strip namespace prefix, PascalCase the local name), so the same code applies.

- **C5.** Wildcards on userset types are not supported by SpiceDB grammar (`team:*#admin` is illegal). Per A1 — `(IsWildcard=true, SubRelation!="")` is unreachable in practice; the codegen does not emit any combined wildcard+userset path.

- **C6.** `Read<Rel><Type>Relations` filters by `SubjectType` server-side. A relation declaring `member: user | team#admin` produces two distinct read methods: `ReadMemberUserRelations` (returns User subjects) and `ReadMemberTeamRelations` (returns Team subjects with `SubRelation="admin"`). The two methods read disjoint subsets of the same wire stream.

- **C7.** Non-userset schemas regenerate byte-identically. All new template branches gate on `$relType.SubRelation != ""`; absent userset allowed types means zero new emission. The `SubRelation string` field added to `<Rel><Type>Relation` is per-relation/type and always emits — non-userset relations carry an always-empty field. Per A5 — the field is positional-stable and additive; no existing-caller break.

- **C8.** Userset Check inputs route through `CheckPermissionUserset` only when populated. Caller passing `CheckProjectViewInputs.User: []User{"u1"}` continues to use the existing `CheckPermission` / `CheckPermissionWithCaveat` path. The userset path is opt-in per-call.

- **C9.** Lookup paths are unchanged. Per the deferred scope, `LookupResources` / `LookupSubjects` keep their existing typed return; userset-as-Lookup-input/output is a future SPEC. Calling `LookupView<Type>Resources(...)` on a permission reaching a userset allowed type still works for the user-input case (SpiceDB walks the userset chain server-side); the rare case of looking up *which userset references* have a permission is not exposed.

- **C10.** `CheckPermissionUserset` semantics differ from `CheckPermission`. Per A2 — userset-as-subject Check matches the literal userset reference and does NOT recursively walk the userset's membership. Tests must verify this distinction explicitly.

---

## Assumptions

- **A1 [EXTERNAL FACT]:** SpiceDB grammar disallows wildcards on userset types. `team:*#admin` is rejected at schema parse time. Evidence: SpiceDB schema documentation — wildcards are a property of the type reference, not composable with sub-relation references. Will be verified in WS1 by attempting the construct and observing the parser rejection.

- **A2 [EXTERNAL FACT]:** SpiceDB's userset-as-subject Check returns `HAS_PERMISSION` only when the literal userset reference is granted on the resource (no recursive expansion of the userset's membership). Evidence: Authzed docs on `CheckPermission` semantics — when `Subject.OptionalRelation` is non-empty, the check matches the userset triple, not its expansion. Distinct from the user-as-subject case where SpiceDB walks every chain to find the user.

- **A3 [VERIFIED]:** `OPERATION_TOUCH` is safe for non-expiring userset writes. AUZ-009 verified TOUCH idempotency for expiring writes; the same applies to userset writes — re-writing a tuple that already exists is a no-op. Evidence: AUZ-009 e2e test `TestFolder_ExpiringBrowse_TouchAllowsRewriteAfterExpiry` exercises TOUCH and confirms idempotency at the wire level.

- **A4 [VERIFIED]:** The existing `pascalCaveatName` helper in `internal/generator/adapter.go:434` generalizes to sub-relation pascalization. The function strips the namespace prefix (everything before the last `/`) and PascalCases the local name. Sub-relations don't have namespaces (they're local to the type), so the strip-prefix step is a no-op and the function correctly returns `Admin` for `admin`, `OwnerAdmin` for `owner_admin`, etc. Evidence: `internal/generator/utilstr` tests cover `SnakeToPascal` for the underlying transformation.

- **A5 [VERIFIED]:** Adding the `SubRelation string` field to `RelationTuple` and `<Rel><Type>Relation` is positional-stable per AUZ-010 SPEC-005 C6 (future protocol additions extend by appending fields). The field is appended after the existing `ID` field but before `CaveatName` to keep traits-related fields grouped. Evidence: SPEC-005 C6 establishes the convention; SPEC-006 adheres.

- **A6 [HYPOTHESIS]:** No existing fixture relation declares a sub-relation reference (the construct has been rejected at adapt time since v1.0.0). Verification deferred to WS4 — schema fixture migration. If a relation is found that was working around the limitation by using a different construct, document the migration in Discoveries.

- **A7 [EXTERNAL FACT]:** `compiler.Compile()` validates that referenced sub-relations exist on the target definition. Schema `relation X: team#admin` where `team` has no `admin` relation/permission is rejected before the codegen runs. Evidence: SpiceDB schema compiler documentation; downstream effect is that the codegen sees only well-formed `AllowedRelation.Relation` references.

---

## Unresolved Questions

(none)

---

## Summary

Net change scope:

| File | Change |
|---|---|
| `internal/generator/adapter.go` | Add `SubRelation string` to `AllowedType`; remove the existing `sub-relation references are not supported` rejection; capture `ar.GetRelation()` into the new field. Extend disambiguation key from `(Namespace, IsWildcard)` to `(Namespace, IsWildcard, SubRelation)`. |
| `internal/generator/generator.go` | (No new helpers needed — existing `pascalCaveat` generalizes per A4.) |
| `internal/templates/object.go.tmpl` | Emit userset write fields on `<Rel>Objects` as `<TypeName><PascalSubRelation> []<TypeName>`. Per-allowed-type branch in `Create<Rel>Relations` body routes userset entries through `CreateRelationsToUserset`. Add `SubRelation string` field to generated `<Rel><Type>Relation` struct. Emit userset Check input fields on `Check<Perm>Inputs` and route through `CheckPermissionUserset` when populated. |
| `pkg/authz/authz.go` | Add `SubRelation string` to `RelationTuple`. Add `Engine.CreateRelationsToUserset` and `Engine.CheckPermissionUserset` to the interface. |
| `pkg/authz/spicedb/crud.go` | Implement `*Engine.CreateRelationsToUserset` (TOUCH unconditionally; populates `Subject.OptionalRelation`). Implement `*Engine.CheckPermissionUserset` (mirrors `CheckPermissionWithCaveat` with subject relation set). Update `*Engine.ReadRelations` to populate `RelationTuple.SubRelation` from `rel.Subject.OptionalRelation`. |
| `example/schema.zed` | Add `extsvc/team` definition with `owner` / `manager` relations and `admin` permission. New fixtures on `extsvc/folder` (or a new resource): `relation collab: extsvc/team#admin` (plain userset), `relation mixed_view: extsvc/user \| extsvc/team#admin` (mixed direct + userset), `relation gated_collab: extsvc/team#admin with extsvc/tenant_match` (userset + caveat), `relation temp_collab: extsvc/team#admin with expiration` (userset + expiration). |
| `example/authzed/extsvc/*.gen.go` | Regenerated output. |

E2E tests against live SpiceDB cover: write+read direct path unchanged; write userset → assert tuple lands with `Subject.OptionalRelation = "admin"`; Check user via userset chain (common case) — admin u1 of team t1 has `view` permission on project granting `team:t1#admin` member; Check userset literally (rare case) — `Check(view, subject={team, t1, "admin"})` returns granted; userset + caveat fixture; userset + expiration fixture; mixed direct + userset relation Read returns both branches via dedicated Read methods.
