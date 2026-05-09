# [SPEC-005] Read with Metadata

| Field      | Value                                                |
|------------|------------------------------------------------------|
| Status     | Accepted                                             |
| Created    | 2026-05-09                                           |
| Author     | Danh Tran                                            |
| Implements | (closes AUZ-007 Discoveries Gap C and SPEC-004 C10)  |

---

## Overview

This SPEC redefines `Read<Rel><Type>Relations` to return per-tuple metadata — caveat name, caveat context, expiration timestamp — alongside the typed subject ID. Today the read path strips both `OptionalCaveat` and `OptionalExpiresAt` from SpiceDB's response and returns `[]<Type>`; admin and audit code that needs to surface "user X has access via tenant=acme until 2026-Q4" has to bypass the codegen and call `authzed-go` directly. SPEC-005 lifts that gap by changing the canonical read return shape to `[]<Rel><Type>Relation` (a typed metadata struct), introducing a runtime tuple type `authz.RelationTuple` on the Engine surface, and providing a generic `authz.IDsOf` helper for the common "I just want IDs" case. The change applies uniformly: every relation gets the metadata return shape regardless of whether its allowed types currently carry traits, so a schema gaining `with caveat` or `with expiration` later doesn't change method names. Wildcards keep their existing dedicated method, now also returning metadata.

**What this component does:** Replace `Engine.ReadRelations` return type from `[]ID` to `[]RelationTuple`. Add `RelationTuple` struct in `pkg/authz/` carrying `ID + CaveatName + CaveatContext + ExpiresAt`. Generate per-relation/type typed structs `<Rel><Type>Relation` with the same fields, ID typed to the relation's subject. Generate `Read<Rel><Type>Relations(ctx) ([]<Rel><Type>Relation, error)` for every relation/type pair (replacing the existing `[]<Type>` return). Generate `Read<Rel><Type>Wildcard(ctx) (<Rel><Type>Relation, bool, error)` returning the wildcard tuple's metadata (replacing the existing `(bool, error)` return). Add `authz.IDsOf` generic helper that projects ID slices from any slice of metadata structs. Update `*spicedb.Engine.ReadRelations` to populate caveat and expiration fields from `Relationship.OptionalCaveat` and `Relationship.OptionalExpiresAt`. Update `*spicedb.Engine.HasPublicRelation` to consume the new `[]RelationTuple` return shape internally.

**What this component does not do:** Modify Check / Lookup / Create / Delete paths — those keep their existing signatures. Stream tuples — `[]RelationTuple` materializes in full (per A4); a future iterator API can replace this if memory pressure surfaces. Reconstruct typed caveat-arg structs from `CaveatContext` — the field is `map[string]any` (raw decoded structpb); callers needing typed access decode based on `CaveatName`. Filter by caveat name or expiration window at read time — reads return all live tuples; SpiceDB already filters expired ones server-side per SPEC-004 A4. Surface the SpiceDB tuple's resource (it's known by the caller — every codegen Read method is bound to a specific resource via the receiver).

---

## Interface Contracts

### Runtime types — `pkg/authz/authz.go`

```go
// RelationTuple is the engine-surface representation of a single
// SpiceDB relationship row. Subject ID is untyped at this layer;
// generated code casts it to the typed subject (User, Group, …).
type RelationTuple struct {
    ID            ID                // subject ID (may be WildcardID for wildcard tuples)
    CaveatName    string            // empty if no caveat attached
    CaveatContext map[string]any    // nil if no caveat or empty pre-context
    ExpiresAt     *time.Time        // nil if no per-tuple TTL
}
```

### Engine interface — `pkg/authz/authz.go`

`ReadRelations` return type changes from `[]ID` to `[]RelationTuple`. Other methods unchanged.

```go
type Engine interface {
    // ... existing methods unchanged ...
    ReadRelations(ctx context.Context, from Resource, relation Relation, subject Type) ([]RelationTuple, error)
    HasPublicRelation(ctx context.Context, on Resource, relation Relation, subject Type) (bool, error)  // unchanged signature; impl rewritten
    // ... existing methods unchanged ...
}
```

`HasPublicRelation` signature stays `(bool, error)` — its public contract doesn't change. Its body becomes "read tuples, check if any has `ID == WildcardID`". `HasPublicSubject` is similarly unaffected — it's a check-side concept, not read-side.

### `*spicedb.Engine` implementation — `pkg/authz/spicedb/crud.go`

```go
func (e *Engine) ReadRelations(
    ctx context.Context,
    from authz.Resource,
    relation authz.Relation,
    subject authz.Type,
) ([]authz.RelationTuple, error) {
    e.debugLog("Reading relations: from=%v, relation=%v, subject=%v", from, relation, subject)
    consistency := e.getConsistencySnapshot()
    tuples := []authz.RelationTuple{}

    res, err := e.client.ReadRelationships(ctx, &v1.ReadRelationshipsRequest{
        Consistency: consistency,
        RelationshipFilter: &v1.RelationshipFilter{
            ResourceType:       string(from.Type),
            OptionalResourceId: string(from.ID),
            OptionalRelation:   string(relation),
            OptionalSubjectFilter: &v1.SubjectFilter{
                SubjectType: string(subject),
            },
        },
    })
    if err != nil {
        return nil, err
    }

    data, err := res.Recv()
    for ; err == nil && data != nil; data, err = res.Recv() {
        rel := data.Relationship
        t := authz.RelationTuple{
            ID: authz.ID(rel.Subject.Object.ObjectId),
        }
        if rel.OptionalCaveat != nil {
            t.CaveatName = rel.OptionalCaveat.CaveatName
            if rel.OptionalCaveat.Context != nil {
                t.CaveatContext = rel.OptionalCaveat.Context.AsMap()
            }
        }
        if rel.OptionalExpiresAt != nil {
            ts := rel.OptionalExpiresAt.AsTime()
            t.ExpiresAt = &ts
        }
        tuples = append(tuples, t)
    }
    if !errors.Is(err, io.EOF) {
        return nil, err
    }
    return tuples, nil
}

func (e *Engine) HasPublicRelation(
    ctx context.Context,
    on authz.Resource,
    relation authz.Relation,
    subject authz.Type,
) (bool, error) {
    e.debugLog("Checking public relation: on=%v, relation=%v, subject=%v", on, relation, subject)
    tuples, err := e.ReadRelations(ctx, on, relation, subject)
    if err != nil {
        return false, err
    }
    for _, t := range tuples {
        if t.ID == authz.WildcardID {
            return true, nil
        }
    }
    return false, nil
}
```

`structpb.Struct.AsMap()` is the canonical decoder; `timestamppb.Timestamp.AsTime()` returns a `time.Time` in UTC. Both are stdlib-adjacent (`google.golang.org/protobuf/types/known/...`), already imported by the engine.

### Helper — `pkg/authz/authz.go`

```go
// IDsOf projects subject IDs from a slice of relation metadata structs.
// Generated code's per-relation typed structs all expose RelationID() T.
func IDsOf[T ~string, R interface{ RelationID() T }](rels []R) []T {
    out := make([]T, len(rels))
    for i, r := range rels {
        out[i] = r.RelationID()
    }
    return out
}

// IDsOfExcludingWildcard is the read-side equivalent of FromIDsExcludingWildcard.
// Drops any tuple where RelationID() == WildcardID. Generated Read methods
// call this internally before returning a non-wildcard slice (the wildcard
// tuple is surfaced via the sibling Read<Rel><Type>Wildcard method instead).
func IDsOfExcludingWildcard[T ~string, R interface{ RelationID() T }](rels []R) []R {
    out := make([]R, 0, len(rels))
    for _, r := range rels {
        if string(r.RelationID()) == string(WildcardID) {
            continue
        }
        out = append(out, r)
    }
    return out
}
```

Each generated `<Rel><Type>Relation` struct gains a `RelationID() <SubjectType>` method so the constraint is satisfied uniformly. Per A1 — Go 1.21+ generic inference resolves both type parameters from a single positional argument, so callers write `authz.IDsOf(rels)` without the explicit type list.

### Generated typed metadata struct — codegen template

For every `(relation, allowed-type)` pair, the codegen emits:

```go
// For relation Author: extsvc/user → emits ArticleAuthorRelation
type ArticleAuthorRelation struct {
    ID            User              // typed to the allowed type
    CaveatName    string
    CaveatContext map[string]any
    ExpiresAt     *time.Time
}

func (r ArticleAuthorRelation) RelationID() User { return r.ID }
```

Naming convention: `<Pascal(ResourceDef)><Pascal(RelationName)>Relation`. Matches the existing `<Rel><Type>` typed-method naming pattern (`ArticleAuthorRelation` is the read counterpart to the `ArticleAuthorObjects` write struct).

When a relation has multiple allowed types — `relation member: extsvc/user | extsvc/group` — each gets its own struct: `FolderMemberUserRelation`, `FolderMemberGroupRelation`. Field name on the struct is fixed `ID` (the subject is type-bound by the struct itself; no disambiguation needed at the field level).

### Generated `Read<Rel><Type>Relations` method — codegen template

```go
func (article Article) ReadAuthorUserRelations(ctx context.Context) ([]ArticleAuthorRelation, error) {
    tuples, err := authz.GetEngine(ctx).ReadRelations(ctx, authz.Resource{
        Type: TypeArticle,
        ID:   authz.ID(article),
    }, authz.Relation(ArticleAuthor), TypeUser)
    if err != nil {
        return nil, err
    }
    rels := make([]ArticleAuthorRelation, 0, len(tuples))
    for _, t := range tuples {
        if t.ID == authz.WildcardID {
            continue  // wildcard surfaces via Read<Rel><Type>Wildcard
        }
        rels = append(rels, ArticleAuthorRelation{
            ID:            User(t.ID),
            CaveatName:    t.CaveatName,
            CaveatContext: t.CaveatContext,
            ExpiresAt:     t.ExpiresAt,
        })
    }
    return rels, nil
}
```

Wildcard filtering matches the existing `FromIDsExcludingWildcard` behavior — wildcards live in their own method.

### Generated `Read<Rel><Type>Wildcard` method — codegen template

For every `(relation, allowed-type)` pair where `IsWildcard == true`:

```go
func (folder Folder) ReadGuestUserWildcard(ctx context.Context) (FolderGuestRelation, bool, error) {
    tuples, err := authz.GetEngine(ctx).ReadRelations(ctx, authz.Resource{
        Type: TypeFolder,
        ID:   authz.ID(folder),
    }, authz.Relation(FolderGuest), TypeUser)
    if err != nil {
        return FolderGuestRelation{}, false, err
    }
    for _, t := range tuples {
        if t.ID == authz.WildcardID {
            return FolderGuestRelation{
                ID:            User(t.ID),  // == authz.WildcardID; caller can ignore or use the sentinel
                CaveatName:    t.CaveatName,
                CaveatContext: t.CaveatContext,
                ExpiresAt:     t.ExpiresAt,
            }, true, nil
        }
    }
    return FolderGuestRelation{}, false, nil
}
```

The `bool` reports presence (matches the existing `(bool, error)` shape's intent). The struct carries metadata when present — caveat info and expiry on a wildcard tuple are preserved per A2 (SpiceDB allows wildcards with traits, AUZ-007 verified the write side).

### Adapter / Generator — no new fields

`AllowedType` already carries `Namespace`, `IsWildcard`, `CaveatName`, `IsExpiring`, `IDFieldName`. SPEC-005 adds no new adapter-level state. Template helpers added: `permRelationStructName` (formats `<Pascal(ResourceDef)><Pascal(RelationName)>Relation`). Existing `anyCaveat`, `anyExpiring`, `anyWildcard` cover the conditional emission needs.

### Fixture migration — `example/authzed/**/*_test.go`

Every existing `Read<Rel><Type>Relations` caller in `example/authzed/...` test files migrates from `[]<Type>` to `[]<Rel><Type>Relation`. Mechanical sweep — find call sites via `grep -rn 'Read.*Relations(ctx)' example/`, project IDs via `authz.IDsOf` where the test only cares about IDs, otherwise consume the full struct. Wildcard call sites migrate from `(bool, error)` to `(<Rel><Type>Relation, bool, error)`; existing tests already discard the new struct via `_,` (binding the bool only) so most call sites stay one-liners. New tests verify metadata fields populate correctly for caveated, expiring, and combined-trait tuples.

---

## Sequence

Runtime flow when a caller reads relations:

```
caller code:

    rels, err := article.ReadAuthorUserRelations(ctx)
         │
         ▼
generated method body:

    ├─► engine.ReadRelations(
    │        ctx,
    │        authz.Resource{Type: TypeArticle, ID: authz.ID(article)},
    │        authz.Relation(ArticleAuthor),
    │        TypeUser,
    │    )
    │       returns: []authz.RelationTuple
    │
    ├─► for each tuple:
    │     ├─► if ID == WildcardID: skip (surfaces via ReadAuthorUserWildcard)
    │     └─► else: append ArticleAuthorRelation{
    │             ID:            User(t.ID),
    │             CaveatName:    t.CaveatName,
    │             CaveatContext: t.CaveatContext,
    │             ExpiresAt:     t.ExpiresAt,
    │         }
    │
    └─► return rels, nil

         │
         ▼
*spicedb.Engine.ReadRelations:

    ├─► consistency := getConsistencySnapshot()  (existing)
    │
    ├─► client.ReadRelationships(ctx, &Request{
    │       RelationshipFilter: ResourceType+OptionalResourceId+OptionalRelation+SubjectFilter,
    │       Consistency: consistency,
    │   })
    │
    ├─► stream loop:
    │     for data, err := res.Recv(); ; {
    │       rel := data.Relationship
    │       tuples = append(tuples, RelationTuple{
    │           ID: authz.ID(rel.Subject.Object.ObjectId),
    │           CaveatName:    rel.OptionalCaveat.CaveatName    (if not nil)
    │           CaveatContext: rel.OptionalCaveat.Context.AsMap() (if not nil)
    │           ExpiresAt:     pointer-to(rel.OptionalExpiresAt.AsTime()) (if not nil)
    │       })
    │     }
    │
    └─► return tuples, nil
```

Wildcard read flow — same engine call, generated wrapper picks the wildcard tuple instead:

```
caller: present, meta, err := folder.ReadGuestUserWildcard(ctx)
         │
         ▼
generated body:
    ├─► engine.ReadRelations(...)  // identical to above
    ├─► scan for tuple with ID == WildcardID
    │     ├─► found: return (<Rel><Type>Relation{...}, true, nil)
    │     └─► not found: return (<Rel><Type>Relation{}, false, nil)
```

`HasPublicRelation` shares the same scan but returns just the bool — a thin specialization of the wildcard-read flow above for callers that don't need the metadata.

---

## Errors

| Error class | Trigger | Layer |
|---|---|---|
| `*structpb.Struct.AsMap()` panic | Should not occur per A3 — protobuf-decoded structs are always well-formed. If SpiceDB ever returns a malformed `Context`, the panic surfaces unwrapped. | Engine — caller observes panic |
| `*timestamppb.Timestamp.AsTime()` overflow | Timestamps outside `time.Time`'s representable range. SpiceDB stores Unix-epoch-bounded timestamps; per A6, this is a non-occurring class for any real schema. | Engine — caller observes `time.Time` clamped to bounds |
| `client.ReadRelationships` stream error mid-loop | gRPC failure mid-stream — partial results. The current implementation returns `(nil, err)` on non-EOF errors; SPEC-005 keeps that behavior. | Engine — passed through unwrapped |
| Empty result | Zero tuples match the filter. Returns `([]RelationTuple{}, nil)` (empty slice, not nil). Generated code returns `([]<Rel><Type>Relation{}, nil)`. | Engine + codegen — non-error |
| Invalid resource ID | Caller passes an empty resource ID. `client.ReadRelationships` errors with gRPC `InvalidArgument`; pass-through. | Engine — passed through |

The aggregator behavior on stream-mid-loop error: returns `(nil, err)` per the existing pattern; partial tuples are not surfaced. Per A5 — caller should not inspect the slice when err != nil.

---

## Constraints

- **C1.** `RelationTuple.CaveatContext` is `map[string]any` (raw decoded structpb). Callers needing typed access decode based on `CaveatName`. Per A3 — the codegen does not reconstruct `<Caveat>Args` from `CaveatContext` because the same allowed type may carry different caveats over the schema's lifetime, and the typed struct is per-caveat-declaration not per-allowed-type.

- **C2.** `RelationTuple.CaveatName` is `string` (not `*string`). Empty string means "no caveat" — distinguishable from any real caveat name (SpiceDB caveat names are always non-empty per schema syntax).

- **C3.** `RelationTuple.ExpiresAt` is `*time.Time` (not `time.Time`) because the zero `time.Time{}` is a valid past timestamp (`0001-01-01`) and would be ambiguous with "no expiration set".

- **C4.** Wildcards split keeps the existing pattern. `Read<Rel><Type>Relations` filters wildcards out; `Read<Rel><Type>Wildcard` returns the single wildcard tuple's metadata + presence bool. Per A2 — wildcards may carry caveats/expiration; metadata surfaces in both directions.

- **C5.** `HasPublicRelation` signature `(bool, error)` is preserved. The internal implementation now consumes `[]RelationTuple` instead of `[]ID`. Callers see no behavior change.

- **C6.** Generated `<Rel><Type>Relation` struct per allowed type — fields are positional-stable for the lifetime of v1.x: `{ID, CaveatName, CaveatContext, ExpiresAt}`. Future protocol additions (e.g. SpiceDB adds `OptionalRetentionLabel`) extend by appending fields, never reordering. Callers using positional struct literals (`{User("u1"), "", nil, nil}`) keep compiling but should migrate to keyed literals.

- **C7.** No metadata in generated `Read<Rel><Type>Relations` for non-traited relations carries cost: every relation's read returns the same struct shape regardless of whether its allowed types ever attach traits today. Per the always-emit decision (variant C above) — schema evolution is invisible to the read API.

- **C8.** All-relations migration. Variant C replaces every existing `Read<Rel><Type>Relations() ([]<Type>, error)` with the new return type. Callers in `example/authzed/**/*_test.go` migrate; the `authz.IDsOf` helper closes the simple-case migration to one extra line per call site. Trade-off: the typed-IDs-only path now requires that helper call. Per the active-development consumer profile, the breaking change is acceptable.

- **C9.** Slice materialization. `ReadRelations` returns the full result before yielding to the caller (matches current behavior). Per A4 — for a relation with N tuples, memory cost is O(N · sizeof(RelationTuple) + sum of caveat-context sizes). Iterator API deferred; SPEC notes the limit but does not address it in this revision.

- **C10.** Resource is not surfaced. `RelationTuple` and `<Rel><Type>Relation` carry only the subject side. The resource is bound by the calling receiver (`folder.ReadGuestUserRelations` knows it's reading from a specific `Folder`). Per the existing codegen invariant — every `Read` is resource-bound.

---

## Assumptions

- **A1 [VERIFIED]:** Go 1.21+ generic type inference resolves `IDsOf[T, R](rels []R) []T` from a single positional `[]<TypedRelation>` argument because the constraint `interface{ RelationID() T }` provides a constructive bridge from `R` to `T`. Evidence: Go 1.21 release notes "more powerful type inference"; current go.mod targets 1.26.2; the codegen already uses similar generic patterns for `FromIDsExcludingWildcard[T ~string]`.

- **A2 [EXTERNAL FACT]:** SpiceDB allows wildcards to carry caveats and expiration. AUZ-007 verified the write side (`relation guarded_viewer: extsvc/user:* with extsvc/tenant_match`); AUZ-009.1 verified the read-side filtering for wildcard+expiration. Evidence: `example/schema.zed` lines 218 and 320–323; `TestFolder_PublicBrowse_*` tests pass.

- **A3 [EXTERNAL FACT]:** `*structpb.Struct.AsMap()` returns a recursively-decoded `map[string]any` that loses no information at decode time — protobuf encodes only well-formed JSON-compatible types. Evidence: `go doc google.golang.org/protobuf/types/known/structpb Struct.AsMap`. Reverse direction: `serializeCaveatMap` in `pkg/authz/spicedb/crud.go` produces protobuf from `map[string]any` symmetrically (per AUZ-007 implementation).

- **A4 [HYPOTHESIS]:** No production schema in the codebase will hit memory pressure from materializing `[]RelationTuple` in `ReadRelations`. Verification deferred — current example fixtures hold ≤10 tuples per relation; production deployments typically partition by resource ID. Iterator API is a future change if this assumption fails.

- **A5 [VERIFIED]:** The current `ReadRelations` implementation returns `(nil, err)` on stream-mid-loop errors. Callers must not inspect the slice when err is non-nil. Evidence: `pkg/authz/spicedb/crud.go:515` `if !errors.Is(err, io.EOF) { return nil, err }`. SPEC-005 preserves this.

- **A6 [EXTERNAL FACT]:** SpiceDB stores `OptionalExpiresAt` as a Unix-epoch timestamp; values fall within `time.Time`'s representable range (well outside the year 1 / year 9999 bounds). `*timestamppb.Timestamp.AsTime()` is total over real schema values. Evidence: `go doc google.golang.org/protobuf/types/known/timestamppb Timestamp.AsTime`.

- **A7 [VERIFIED]:** Existing fixture round-trip is a regression bar. Every change in this SPEC must preserve `git diff --quiet example/authzed/` after `go run ./cmd/authzed-codegen --output example/authzed example/schema.zed` against the new baseline. The migration regenerates every `.gen.go` file in the fixture; the post-migration diff against HEAD is non-zero by definition, but subsequent re-runs are zero-diff. Evidence: `.claude/CLAUDE.md` build/verify section names this as the bar.

---

## Unresolved Questions

(none)

---

## Summary

Net change scope:

| File | Change |
|---|---|
| `pkg/authz/authz.go` | Add `RelationTuple` struct; change `Engine.ReadRelations` return to `[]RelationTuple`; add `IDsOf` and `IDsOfExcludingWildcard` generic helpers. |
| `pkg/authz/spicedb/crud.go` | Update `*Engine.ReadRelations` to populate caveat + expiration fields from `Relationship.OptionalCaveat` and `OptionalExpiresAt`. Update `*Engine.HasPublicRelation` to consume the new return shape. |
| `internal/templates/object.go.tmpl` | Generate `<Rel><Type>Relation` struct per allowed type with `RelationID()` method. Replace existing `Read<Rel><Type>Relations` body with the metadata-mapping form. Replace existing `Read<Rel><Type>Wildcard` body with the metadata-returning form. |
| `internal/generator/generator.go` | Add `permRelationStructName` template helper. |
| `example/authzed/**/*.gen.go` | Regenerated output — every relation's `Read*` methods change signatures. |
| `example/authzed/**/*_test.go` | Migrate call sites: extract IDs via `authz.IDsOf` for simple cases; consume metadata struct for new tests covering caveat-name surfacing, caveat-context surfacing, expiration timestamp surfacing. |
| `internal/generator/adapter.go` | No changes — existing `AllowedType` fields are sufficient. |

E2E tests cover: read non-traited tuple (all metadata fields nil/empty); read caveated tuple (`CaveatName` and `CaveatContext` populated); read expiring tuple (`ExpiresAt` populated); read combined caveat+expiration; read wildcard tuple via `Read<Rel><Type>Wildcard` (metadata struct returned alongside the bool); `IDsOf` round-trip equivalence with the previous API surface (no behavior gap on the simple "give me the IDs" path).
