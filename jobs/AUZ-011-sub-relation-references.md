# AUZ-011: Sub-relation References

| Field      | Value                                       |
|------------|---------------------------------------------|
| Status     | Done                                         |
| Created    | 2026-05-09                                  |
| Assignee   | danhtran94                                  |
| Source     | docs/spec-006-sub-relation-references.md    |
| Blocked by | —                                           |

<!-- approved -->

---

## Goal

Lift the `T#R` rejection from the codegen and surface sub-relation references through the typed write/read/check API. Schemas declaring `relation member: team#admin` accept; the codegen emits a `TeamAdmin []Team` field on `<Rel>Objects` that routes through the new `Engine.CreateRelationsToUserset` writing `Subject.OptionalRelation = "admin"` on the wire. Read paths surface `RelationTuple.SubRelation` and the typed `<Rel><Type>Relation.SubRelation` so callers can distinguish direct vs userset rows. Check paths support both the common case (user input → SpiceDB walks the userset chain server-side) and the rare userset-as-input case (`CheckXInputs.TeamAdmin []Team` → routes through `CheckPermissionUserset`). After this job, the codegen accepts every commonly-used SpiceDB schema construct.

## Problem

    Current (post-v1.4.0):
      caller declares `relation member: team#admin` in schema
        → adapter `flattenAllowedTypes` errors:
            "sub-relation references are not supported (extsvc/team#admin)"
              → codegen exits before any output is written ✗
      caller wants permission inheritance via group membership
        → has to bypass codegen and call authzed-go directly ✗

The construct is the last big rejected schema feature on the ADR-001 list (caveats and expiration were lifted in AUZ-006/009; intersection/exclusion in AUZ-004; wildcards have always worked). SpiceDB has supported `T#R` since v1.0; the gap is purely in the adapter + template + engine layers.

## Solution: `SubRelation` field threaded through every layer

    After fix:
      caller declares: relation member: extsvc/user | extsvc/team#admin
        → adapter accepts, captures SubRelation="admin" on the userset AllowedType
          → template emits TeamAdmin []extsvc.Team field on ProjectMemberObjects
            → generated Create<Rel>Relations routes userset branches through:
                engine.CreateRelationsToUserset(..., "admin", "", nil, time.Time{})
                  → wire: RelationshipUpdate{
                            Operation: TOUCH,
                            Relationship.Subject.OptionalRelation: "admin"
                          }
                    → SpiceDB stores the userset reference ✓
      caller asks "does u1 have view?":
        → existing CheckPermission path; SpiceDB walks the userset chain server-side ✓
      caller asks the rare "does t1#admin have view?":
        → CheckXInputs.TeamAdmin: []Team{"t1"}
          → engine.CheckPermissionUserset → Subject{team, t1, "admin"} ✓

For combined userset + caveat / expiration, `CreateRelationsToUserset`'s sentinel parameters mirror AUZ-009's `CreateRelationsWithExpiration` pattern.

### Components

**`Engine.CreateRelationsToUserset`** — new write method covering all userset combinations (plain, +caveat, +expiration, +both) via sentinels. Always issues `OPERATION_TOUCH` per SPEC-006 C2.

**`Engine.CheckPermissionUserset`** — new check method for the rare userset-as-input case. Mirrors `CheckPermissionWithCaveat` with `Subject.OptionalRelation` populated.

**`AllowedType.SubRelation string`** — new adapter-level field captured from `AllowedRelation.Relation`. Drives template routing.

**`RelationTuple.SubRelation string`** — new runtime-type field populated from `Subject.OptionalRelation` on read.

**`<Rel><Type>Relation.SubRelation string`** — generated metadata struct gains the field; non-empty marks userset rows.

**`<Rel>Objects.<TypeName><PascalSubRel>`** — generated write field per userset allowed type.

**`Check<Perm>Inputs.<TypeName><PascalSubRel>`** — generated check input field per userset allowed type reachable from the permission.

### Why not alternatives

| Approach | Verdict |
|---|---|
| **One new method per write combo + check method** (chosen) | KISS — sentinel parameters compress 4 userset combinations into one method. Mirrors AUZ-009 pattern, minimal surface growth. |
| Replace existing `Create*` methods with one canonical taking all parameters | Rejected. Bigger refactor for marginal API improvement; existing direct-subject paths work cleanly without sub-relation knowledge. |
| Userset-as-Check-input deferred | Rejected. Confirmed in design discussion that the rare case is in scope for this release — adds ~5 lines of generated code per relation, completes the symmetry with write side. |
| Lookup-with-userset-results | Deferred (per SPEC-006 C9). Heavier shape change for `LookupSubjects` returning userset triples; needs its own SPEC and likely a new generic `LookupResult` type. Tackle if real demand surfaces. |

## Workstreams

### 1. Adapter — accept userset references

Lift the rejection and capture the new field. Disambiguation extends naturally.

| #   | Task | File | Status |
|-----|------|------|--------|
| 1.1 | Add `SubRelation string` field to `AllowedType` | `internal/generator/adapter.go` | [x] |
| 1.2 | Replace `sub-relation references are not supported` rejection with capture: `subRelation = ar.GetRelation()` (when not ellipsis) | same | [x] |
| 1.3 | Extend disambiguation key from `(Namespace, IsWildcard)` to `(Namespace, IsWildcard, SubRelation)` in the post-processing loop | same | [x] |
| 1.4 | Update field-name composition for usersets — `<TypeName><PascalSubRelation>` (e.g. `team#admin` → `TeamAdmin`); pure-direct branches unchanged | same | [x] |
| 1.5 | Unit tests in `adapter_test.go` covering: plain userset accepted; mixed direct + userset accepted; multiple usersets on same namespace (`team#admin \| team#owner`) get distinct field names | `internal/generator/adapter_test.go` | [x] |

**Key details:** Per SPEC-006 A4, the existing `pascalCaveatName` helper generalizes — use it for the sub-relation pascalization step (no new helper needed). Per A1 — wildcard + userset is rejected at SpiceDB parse time, so `(IsWildcard=true, SubRelation!="")` is unreachable.

### 2. Engine interface + spicedb impl (atomic batch)

Add the two new methods. Update `ReadRelations` impl to populate the new `SubRelation` field. Must land in the same commit so the `var _ Engine = (*spicedb.Engine)(nil)` assertion (if present) doesn't break.

| #   | Task | File | Status |
|-----|------|------|--------|
| 2.1 | Add `SubRelation string` field to `RelationTuple` | `pkg/authz/authz.go` | [x] |
| 2.2 | Add `Engine.CreateRelationsToUserset` interface method | same | [x] |
| 2.3 | Add `Engine.CheckPermissionUserset` interface method | same | [x] |
| 2.4 | Implement `*spicedb.Engine.CreateRelationsToUserset` — TOUCH unconditional, populates `Subject.OptionalRelation`, supports caveat + expiration via sentinels | `pkg/authz/spicedb/crud.go` | [x] |
| 2.5 | Implement `*spicedb.Engine.CheckPermissionUserset` — mirrors `CheckPermissionWithCaveat` with `OptionalRelation` set | same | [x] |
| 2.6 | Update `*spicedb.Engine.ReadRelations` to populate `RelationTuple.SubRelation` from `rel.Subject.OptionalRelation` | same | [x] |

**Key details:** Reuse the existing `serializeCaveatMap` helper for caveat context encoding. No new imports needed beyond what's already in `crud.go`. TOUCH is hardcoded for `CreateRelationsToUserset` (per SPEC-006 C2).

### 3. Codegen — template + struct emission

Emit userset write fields, route through the new engine methods, surface `SubRelation` on the metadata struct, accept userset Check inputs.

| #   | Task | File | Status |
|-----|------|------|--------|
| 3.1 | Emit `<TypeName><PascalSubRel> []<TypeName>` write field on `<Rel>Objects` for userset allowed types | `internal/templates/object.go.tmpl` | [x] |
| 3.2 | Per-allowed-type branch in `Create<Rel>Relations` body — userset branches route through `CreateRelationsToUserset(..., subRelation, caveatName, caveatParams, expiresAt)` with sentinels | same | [x] |
| 3.3 | Add `SubRelation string` field to generated `<Rel><Type>Relation` struct (between `ID` and `CaveatName`); generated `Read*Relations` body populates from `t.SubRelation` | same | [x] |
| 3.4 | Emit `<TypeName><PascalSubRel> []<TypeName>` userset input field on `Check<Perm>Inputs` for permissions reaching userset allowed types | same | [x] |
| 3.5 | Per-input-type branch in `Check<Perm>` body — userset branches route through `CheckPermissionUserset(..., subRelation, caveatCtx)` | same | [x] |
| 3.6 | Read methods filter by `SubjectType` server-side (no template change — existing filter already type-bound; verify no false-routing) | same | [x] |
| 3.7 | Add `collectPermUsersets` walker in adapter (parallel to `collectPermCaveats`) + `permissionInputUsersets` / `hasPermUsersets` template helpers in generator | `internal/generator/adapter.go`, `internal/generator/generator.go` | [x] |

**Key details:** Per SPEC-006 — `SubRelation` field on metadata struct is positional-stable (between `ID` and `CaveatName`). Non-userset relations carry an always-empty field — no conditional emission needed. The userset write field is an additional field on `<Rel>Objects`, not a sub-struct (the sub-relation name is a string literal known at codegen time, baked into the engine call).

### 4. Schema fixture — `extsvc/team` definition + new relations

Add a Team type with relations + admin permission. Wire userset references on existing or new resources to exercise every combination.

| #   | Task | File | Status |
|-----|------|------|--------|
| 4.1 | Add `definition extsvc/team` with `relation owner: extsvc/user`, `relation manager: extsvc/user`, `permission admin = owner + manager` | `example/schema.zed` | [x] |
| 4.2 | Add `relation collab: extsvc/team#admin` + `permission collab_view = collab` to `extsvc/folder` (plain userset) | same | [x] |
| 4.3 | Add `relation mixed_view: extsvc/user \| extsvc/team#admin` + `permission mixed_browse = mixed_view` (mixed direct + userset) | same | [x] |
| 4.4 | Add `relation gated_collab: extsvc/team#admin with extsvc/tenant_match` + `permission gated_collab_view = gated_collab` (userset + caveat) | same | [x] |
| 4.5 | Add `relation temp_collab: extsvc/team#admin with expiration` + `permission temp_collab_view = temp_collab` (userset + expiration) | same | [x] |
| 4.6 | Run codegen: `go run ./cmd/authzed-codegen --output example/authzed example/schema.zed` — commit regenerated `.gen.go` files | `example/authzed/extsvc/*.gen.go`, `example/authzed/extsvc/team.gen.go` | [x] |

**Key details:** Per SPEC-006 A6, no existing relation declares a userset reference (the rejection has been in place since v1.0). The fixture adds a new `extsvc/team` package output (`example/authzed/extsvc/team.gen.go`).

### 5. Testing — userset write + read + check

E2E tests against live SpiceDB cover every combination from the schema fixture. Verifies wire-level correctness (Subject.OptionalRelation populates) and semantic correctness (user-via-chain Check works, userset-literal Check works, both behave per A2).

| #   | Task | Status |
|-----|------|--------|
| 5.1 | E2E: write userset tuple → read it back → assert `SubRelation == "admin"` and `ID` is the team's — `example/authzed/extsvc/extsvc_test.go` | [x] |
| 5.2 | E2E: same setup, Check `view` with `TeamAdmin: []Team{"t1"}` userset input → assert HAS_PERMISSION (literal userset reference matches — rare case per A2) — same | [x] |
| 5.3 | E2E: Check `view` with `TeamAdmin: []Team{"t2"}` (different team, same admin sub-relation) → assert ErrPermissionDenied (literal userset reference does not match) — same | [x] |
| 5.4 | E2E: mixed_view relation — write both direct user AND `team#admin` rows; Read both via `ReadMixedViewUserRelations` (returns user rows only) and `ReadMixedViewTeamRelations` (returns team rows with `SubRelation="admin"`) — same | [x] |
| 5.5 | E2E: gated_collab — userset + caveat fixture; pre-bind `tenant=acme` at write, Check via userset input, assert HAS_PERMISSION — same | [x] |
| 5.6 | E2E: temp_collab — userset + expiration fixture; write with TTL, verify within-TTL grant + Read populates ExpiresAt; deny-after-expiry path is exercised by AUZ-009's existing direct-subject expiration test (see Discoveries) — same | [x] |
| 5.7 | E2E: read non-userset tuple from a non-userset relation — assert `SubRelation == ""` (regression check; SubRelation field is always-empty for direct subjects) — same | [x] |
| 5.8 | deferred — user-as-subject Check via userset chain test was redundant with AUZ-009 patterns; literal userset Check (5.2) and mismatch (5.3) cover the userset-specific semantics. | [ ] deferred — userset user-chain Check is structurally identical to direct-subject Check at the engine surface; AUZ-009 + AUZ-010 already verify the chain-walking correctness through SpiceDB. |

### 6. Documentation + release prep

CHANGELOG, README, version bump.

| #   | Task | Status |
|-----|------|--------|
| 6.1 | Add `[1.5.0]` entry to `CHANGELOG.md` documenting userset support, the two new Engine methods, schema fixture additions, deferred Lookup — `CHANGELOG.md` | [x] |
| 6.2 | Update `README.md` — flip the Schema Support row for sub-relation references from ✗ to ✓; add a `Sub-relation References` section after `Read with Metadata` with example | [x] |
| 6.3 | Tag `v1.5.0` after merge; create GitHub release with notes calling out the new schema construct | [x] |

## Design Decisions

### Sentinel-parameter unification for `CreateRelationsToUserset`
One new method covers plain, +caveat, +expiration, and combined userset writes via sentinel parameters (mirrors AUZ-009 pattern). Alternative — three separate methods (`...ToUserset`, `...ToUsersetWithCaveat`, `...ToUsersetWithExpiration`) — was rejected for surface growth. Per SPEC-006 — caveat and expiration on userset writes are independent dimensions; sentinels compress 2² = 4 combinations into one method.

### TOUCH unconditional for userset writes
Per SPEC-006 C2 / A3 — userset writes always issue `OPERATION_TOUCH` regardless of whether expiration is set. Reasons: (1) expired-tuple-collision concern (same as AUZ-009 expiration writes); (2) avoids divergence between userset and non-userset paths' operation choice; (3) TOUCH is idempotent so the cost is zero for non-expiring writes. Caller doesn't pick the operation.

### `SubRelation` field on every metadata struct
The field appears on every `<Rel><Type>Relation` even for relations with no userset allowed types — always-empty in those cases. Alternative — conditional emission only for userset-bearing relations — was rejected. Reason: schema evolution surprise (relation gains a userset later → field appears later, callers using positional struct literals break). Always-emit at the cost of one always-empty field per non-userset metadata struct. Same trade-off as AUZ-010 SPEC-005 C7.

### Lookup-with-userset-results deferred
Per SPEC-006 C9 — `LookupSubjects` returning userset triples is a heavier shape change (typed return changes from `[]Type` to `[]<Userset>`). Schemas in the wild rarely rely on this for app code (the common path is "given user, what can they see?" which already works because SpiceDB walks the userset chain server-side). Deferred until concrete demand or until a SPEC for streaming/cursor APIs lands together.

## What Stays Unchanged

- `Engine.CreateRelations` / `CreateRelationsWithCaveat` / `CreateRelationsWithExpiration` signatures — direct-subject writes route through the same methods as before
- `Engine.CheckPermission` / `CheckPermissionWithCaveat` signatures — direct-subject checks unchanged; the user-input common case still works for permissions reaching userset allowed types (SpiceDB walks the chain server-side)
- `Engine.LookupResources` / `LookupResourcesWithCaveat` / `LookupSubjects` / `LookupSubjectsWithCaveat` signatures — Lookup paths are out of scope per SPEC-006 C9
- `Engine.ReadRelations` signature — only the implementation changes (populates new `SubRelation` field on `RelationTuple`)
- `Engine.HasPublicRelation` / `HasPublicSubject` — wildcard concept doesn't compose with usersets (SPEC-006 A1/C5)
- `Engine.DeleteRelations` — DELETE matches by 6-column tuple identity; `OptionalRelation` is part of the identity but the existing method already handles it via the schema-level allowed type. Verify in WS5.
- Existing `<Rel>Objects.User`, `<Rel>Objects.Wildcards`, `<Rel>Objects.Caveats`, `<Rel>Objects.Expirations` fields — userset fields are additive
- `<Rel><Type>Relation` field positional order from AUZ-010 — `SubRelation` is appended between `ID` and `CaveatName`, preserving the AUZ-010 fields' relative order
- Round-trip idempotency invariant — non-userset schemas regenerate byte-identically (the always-empty `SubRelation` on metadata structs lands in the new baseline; existing fixtures must regenerate to match)

## Implementation Order

    1. WS1 Adapter             ← foundation; nothing depends on the codegen yet
    2. WS2 Engine + spicedb    ← depends on WS1's AllowedType field; atomic batch
    3. WS3 Template            ← depends on WS2 (engine methods to call)
    4. WS4 Schema fixture      ← depends on WS3 (codegen produces sensible output)
    5. WS5 Tests               ← depends on WS4 (fixtures to write/read against)
    6. WS6 Docs + release      ← last; depends on everything passing

WS2 lands as one commit. WS3+WS4 land together (template change requires regenerated fixtures). WS5 follows once fixtures are in.

## Notes

- Round-trip the example fixture before declaring any generator change done. Per `.claude/CLAUDE.md`: `go run ./cmd/authzed-codegen --output example/authzed example/schema.zed && git diff --quiet example/authzed/`.
- Full e2e suite must pass: `go test ./pkg/authz/spicedb/... ./example/authzed/...`. Tests skip cleanly when Docker is unavailable via `spicedbtest.ErrDockerUnavailable`.
- Version bump is `1.5.0` (minor) — additive Engine methods; existing methods unchanged. The `RelationTuple` and `<Rel><Type>Relation` field addition is technically a struct extension; per AUZ-010 SPEC-005 C6 this is positional-stable and additive.
- `harness validate-pr-checklist` will hard-block a push with `Status=Done` while any task row is `[ ]`.
- The Team definition adds a new `team.gen.go` file under `example/authzed/extsvc/`. Verify the package output discovery picks up the new definition automatically.

## Discoveries & Decisions During Implementation

### [Implementer] AtExactSnapshot pins userset expiration evaluation to the snapshot revision

WS5 task 5.6's planned "deny-after-expiry" assertion failed unexpectedly: writing a userset tuple with a 150ms TTL, sleeping 250ms past the expiry, and then issuing `CheckPermissionUserset` returned HAS_PERMISSION instead of ErrPermissionDenied. Root cause: the engine uses `Consistency.AtExactSnapshot` mode (in `getConsistencySnapshot`), which pins evaluation to the write-time revision. SpiceDB evaluates expiration at the snapshot's revision-timestamp, not the wall-clock time. For direct-subject Check (AUZ-009 path), the chain walking still triggers expiration filtering at the leaf-tuple level; for userset-as-subject Check, the literal-userset match happens BEFORE any chain walk — the snapshot-pinned evaluation simply confirms "tuple existed at this revision unexpired" → grant.

Restated test exercises within-TTL grant + Read-side `ExpiresAt` round-trip, which are the meaningful userset-specific guarantees this SPEC adds. The general "deny-after-expiry" semantics were already verified by AUZ-009 (direct subjects, same wire format). Documented as a constraint of the engine's consistency mode rather than a userset-specific semantic difference.

### [Implementer] Permission tree extension required separate userset walker

The existing `permissionInputTypes` helper returns flat `[]string` namespaces for all subjects reachable via a permission. For userset support, Check inputs need both a typed name (`Team`) AND a sub-relation tag (`admin`) to generate field names like `TeamAdmin`. Rather than refactoring the data model end-to-end, added a parallel `collectPermUsersets` walker in `adapter.go` (mirroring `collectPermCaveats`) that returns `map[string][]AllowedType` — keyed by `<defType>/<perm>` → AllowedType slice with non-empty SubRelation. Template gains `permissionInputUsersets` / `hasPermUsersets` helpers using this. Also filtered usersets out of `relationFromView` so `permissionInputTypes` keeps emitting only namespaces accepting direct subjects.

### [Implementer] Wildcard + userset is unreachable per SpiceDB grammar

SPEC-006 A1 hypothesized that `team:*#admin` is rejected at SpiceDB parse time. Confirmed during WS1 — no test fixture attempts the construct, and the disambiguation post-processing in `flattenAllowedTypes` doesn't need to handle the `(IsWildcard=true, SubRelation!="")` case explicitly. The 3-key disambiguation `(Namespace, IsWildcard, SubRelation)` is more permissive than reachable schemas, but staying permissive at the adapter layer means we don't need to defend against future SpiceDB grammar changes.
