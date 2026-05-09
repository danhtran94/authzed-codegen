# AUZ-010: Read with Metadata

| Field      | Value                                  |
|------------|----------------------------------------|
| Status     | Done                                    |
| Created    | 2026-05-09                             |
| Assignee   | danhtran94                             |
| Source     | docs/spec-005-read-with-metadata.md    |
| Blocked by | —                                      |

<!-- approved -->

---

## Goal

Replace the codegen's read-side return shape from `[]<SubjectType>` to `[]<Rel><Type>Relation` (a typed metadata struct carrying ID + caveat name + caveat context + expiration timestamp). Closes the gap left by AUZ-006/007/009 where caveat and expiration metadata travel on the wire but get stripped by the read path. After this job, callers reading any relation get the canonical metadata shape — schemas adopting `with caveat` or `with expiration` later don't change method names; only what populates in the existing struct's nil fields. End-to-end verified against live SpiceDB with caveated, expiring, combined-trait, and wildcard fixtures.

## Problem

    Current (post-v1.3.0):
      caller: users := folder.ReadGuardedViewerUserRelations(ctx)   // []User
        → engine returns []ID, codegen casts to typed slice
          → caveat name (extsvc/tenant_match) on the wire is silently dropped ✗
          → expiration timestamp on the wire is silently dropped ✗
      caller wants "user X has access via tenant=acme until 2026-Q4"
        → has to bypass codegen and call authzed-go directly ✗

The read-side metadata gap has been documented twice — AUZ-007 Discoveries Gap C and SPEC-004 C10 — but never closed. SpiceDB serves the metadata on every `ReadRelationships` response; only the engine and codegen layers throw it away.

## Solution: Variant C — `[]<Rel><Type>Relation` everywhere

    After fix:
      caller: rels := folder.ReadGuardedViewerUserRelations(ctx)
        → engine returns []authz.RelationTuple (with caveat + expiry fields)
          → codegen casts to []FolderGuardedViewerRelation { ID, CaveatName,
                                                             CaveatContext,
                                                             ExpiresAt }
            → caller projects IDs via authz.IDsOf(rels) when only IDs needed
              → metadata available without leaving codegen ✓

Variant C — every relation's read returns the metadata shape, not just trait-bearing ones. Schema evolution surprise (relation gains a trait → method name changes) is eliminated. Trade-off: every existing test call site migrates from `[]<Type>` to `[]<Rel><Type>Relation`. Mechanical sweep, internal-only.

### Components

**`authz.RelationTuple`** — runtime engine-surface type
- Subject ID (untyped `authz.ID`)
- `CaveatName string` (empty = none)
- `CaveatContext map[string]any` (decoded structpb)
- `ExpiresAt *time.Time` (nil = none)

**`authz.IDsOf[T,R]`** — generic projector
- Constraint: `interface{ RelationID() T }`
- Each generated metadata struct exposes `RelationID() T`
- Single-call ID extraction for the simple case

**`<Rel><Type>Relation`** — generated typed struct
- Same fields as `RelationTuple` but `ID` is typed (`User`, `Group`, …)
- One per `(relation, allowed-type)` pair
- Implements `RelationID()` for `IDsOf` constraint satisfaction

**`*spicedb.Engine.ReadRelations`** — implementation update
- Maps `Relationship.OptionalCaveat` → `CaveatName + CaveatContext`
- Maps `Relationship.OptionalExpiresAt` → `*ExpiresAt`
- Existing stream-loop and consistency-snapshot logic preserved

### Why not alternatives

| Approach | Verdict |
|---|---|
| **Variant C — replace return type everywhere** | Chosen. Uniform surface; schema evolution is invisible to callers; one-time migration cost is mechanical. |
| Variant A — keep both `Read*Relations` and `Read*RelationsWithMetadata` | Rejected. Doubles the read surface; callers face "which one do I use?" decision per-relation. |
| Variant B — only emit `WithMetadata` for trait-reachable relations | Rejected. Schema-evolution surprise: adding `with caveat` later sprouts a new method name; callers have to find/adopt it. |
| Drop `ReadRelations`, return tuples streamed via iterator | Deferred. Iterator API is heavier; A4 hypothesizes no schema in this codebase will hit memory pressure. Revisit if proven wrong. |

## Workstreams

### 1. Runtime types + helpers

Lay the foundation in `pkg/authz/` before changing the engine signature. The helpers must exist before the codegen template can reference them.

| #   | Task | File | Status |
|-----|------|------|--------|
| 1.1 | Add `RelationTuple` struct with `ID, CaveatName, CaveatContext, ExpiresAt` fields and `time` import | `pkg/authz/authz.go` | [x] |
| 1.2 | Add `IDsOf[T,R]` generic helper with constraint `interface{ RelationID() T }` | same | [x] |
| 1.3 | Add `IDsOfExcludingWildcard[T,R]` — drops tuples with `RelationID() == WildcardID` | same | [x] |

**Key details:** `RelationTuple.CaveatName` is `string` (empty = none), `ExpiresAt` is `*time.Time` (nil = none) — per SPEC-005 C2/C3. Imports add `time` to the package; rest of stdlib already covered.

### 2. Engine method update (atomic batch with WS3)

Update `Engine.ReadRelations` return type and the SpiceDB implementation. Must land in the same commit as WS3's interface change so the `var _ Engine = (*spicedb.Engine)(nil)` assertion (if present) doesn't break, and the example fixture compiles.

| #   | Task | File | Status |
|-----|------|------|--------|
| 2.1 | Change `Engine.ReadRelations` return type to `([]RelationTuple, error)` | `pkg/authz/authz.go` | [x] |
| 2.2 | Update `*spicedb.Engine.ReadRelations` to populate `CaveatName/CaveatContext/ExpiresAt` from `Relationship.OptionalCaveat/OptionalExpiresAt` | `pkg/authz/spicedb/crud.go` | [x] |
| 2.3 | Update `*spicedb.Engine.HasPublicRelation` to scan tuples for `ID == WildcardID` (signature unchanged) | same | [x] |

**Key details:** Use `*structpb.Struct.AsMap()` and `*timestamppb.Timestamp.AsTime()` for decoding. Both protobuf packages are already imported. Per SPEC-005 A3/A6 — total functions over real schema values.

### 3. Codegen — adapter + template + generator

Generate the new typed struct + `RelationID()` method, replace the `Read<Rel><Type>Relations` body, replace the `Read<Rel><Type>Wildcard` body.

| #   | Task | File | Status |
|-----|------|------|--------|
| 3.1 | Add `permRelationStructName` template helper (formats `<Pascal(ResourceDef)><Pascal(RelationName)>Relation`) | `internal/generator/generator.go` | [x] (no new helper needed — existing `$typeName + snakeToPascal $rel.Name + typeName $relType.Namespace` composition works inline) |
| 3.2 | Emit `<Rel><Type>Relation` struct per allowed type with `RelationID() <SubjectType>` method | `internal/templates/object.go.tmpl` | [x] |
| 3.3 | Replace `Read<Rel><Type>Relations` body — call `engine.ReadRelations`, filter wildcards, map to typed struct | same | [x] |
| 3.4 | Replace `Read<Rel><Type>Wildcard` body — return `(<Rel><Type>Relation, bool, error)` instead of `(bool, error)` | same | [x] |
| 3.5 | Update template's import block to include `time` whenever there are relations (every metadata struct references `*time.Time`) | same | [x] |

**Key details:** Per SPEC-005 — `<Rel><Type>Relation` field order is positional-stable (`ID, CaveatName, CaveatContext, ExpiresAt`); future protocol additions append. `RelationID()` returns the typed subject ID so `IDsOf` infers `T` from a single positional argument.

### 4. Fixture migration

Sweep every existing `Read<Rel><Type>Relations` and `Read<Rel><Type>Wildcard` call site in test files. Tests using only IDs migrate via `authz.IDsOf`; new tests cover metadata surfacing for caveat / expiration / combined / wildcard cases.

| #   | Task | File | Status |
|-----|------|------|--------|
| 4.1 | Run codegen against `example/schema.zed`; commit the regenerated `.gen.go` files (every relation's Read methods change signatures) | `example/authzed/**/*.gen.go` | [x] |
| 4.2 | Migrate non-metadata test call sites — wrap with `authz.IDsOf(rels)` where the test only consumes IDs | `example/authzed/extsvc/extsvc_test.go`, `example/authzed/menusvc/menusvc_test.go`, `example/authzed/bookingsvc/bookingsvc_test.go` | [x] |
| 4.3 | Migrate wildcard test call sites — discard the new metadata struct via `_,` binding where the existing test only checks the bool | `example/authzed/extsvc/extsvc_test.go`, `example/authzed/bookingsvc/bookingsvc_test.go` | [x] |

**Key details:** Codegen migration must produce a clean `git diff --quiet example/authzed/` after a second `go run ./cmd/authzed-codegen` pass — round-trip idempotency at the new baseline. Per SPEC-005 A7.

### 5. Testing — metadata surfacing

Verify the new metadata fields actually populate. Without these tests we'd ship a SPEC-compliant API that returns all-nil metadata for every tuple.

| #   | Task | Status |
|-----|------|--------|
| 5.1 | E2E: read non-traited tuple — assert `CaveatName == ""`, `CaveatContext == nil`, `ExpiresAt == nil` | [x] |
| 5.2 | E2E: read caveated tuple (write `tenanted_viewer` with `Tenant: "acme"`) — assert `CaveatName == "extsvc/tenant_match"` and `CaveatContext["tenant"] == "acme"` | [x] |
| 5.3 | E2E: read expiring tuple — assert `ExpiresAt != nil` and within ≤2s of the original write timestamp | [x] |
| 5.4 | E2E: read combined caveat+expiration tuple (`gated_token`) — both metadata categories populate | [x] |
| 5.5 | E2E: read wildcard tuple via `Read<Rel><Type>Wildcard` — assert metadata struct populates with caveat info on `guarded_viewer` | [x] |
| 5.6 | E2E: `IDsOf` round-trip equivalence — `IDsOf(rels)` produces the same slice as the old `[]User` return | [x] |

### 6. Documentation + release prep

CHANGELOG, README, version bump.

| #   | Task | Status |
|-----|------|--------|
| 6.1 | Add `[1.4.0]` entry to `CHANGELOG.md` documenting the Read API change, runtime types, helpers, and migration recipe — `CHANGELOG.md` | [x] |
| 6.2 | Update `README.md` — add `Read with Metadata` section after `Expiration` with example | [x] |
| 6.3 | Tag `v1.4.0` after merge; create GitHub release with notes calling out the API change | [x] |

## Design Decisions

### Variant C over A/B
Replace `Read*Relations` return type uniformly. A leaves both methods (surface bloat); B emits `WithMetadata` only for trait-reachable relations (schema evolution surprise). C accepts a one-time migration cost across `example/authzed/**/*_test.go` in exchange for a uniform surface. Per the active-development consumer profile (only this repo consumes the codegen), the breaking change cost is bounded.

### `RelationTuple.CaveatContext` stays `map[string]any`, not auto-decoded `<Caveat>Args`
The same allowed type may carry different caveats over the schema's lifetime; the typed `<Caveat>Args` struct is per-caveat-declaration, not per-allowed-type. Auto-decoding would force the codegen to enumerate all possible caveats reachable per allowed type, multiplying the generated surface. Callers needing typed access decode based on `CaveatName` (one switch per consumer).

### Wildcard split preserved
`Read<Rel><Type>Relations` filters wildcards out; sibling `Read<Rel><Type>Wildcard` returns the wildcard tuple's metadata. This matches the existing pattern (wildcards have always been their own method) and avoids forcing every caller to branch on `ID == authz.WildcardID` in the regular read path.

### `time` always imported in `.gen.go` files
Every `<Rel><Type>Relation` struct references `*time.Time`. Conditional `time` import (the AUZ-009 trick) doesn't apply here — there's no relation that escapes the metadata struct shape. Generated files all gain `"time"` in the import block; round-trip check passes once at the new baseline.

### Slice materialization (no streaming) in v1
`ReadRelations` materializes the full result before returning. Per SPEC-005 A4 — no schema in this codebase has hit memory pressure; iterator API is a future change. SPEC notes the limit, this job does not address it.

## What Stays Unchanged

- `Engine.CheckPermission` / `CheckPermissionWithCaveat` / `LookupResources*` / `LookupSubjects*` / `CreateRelations*` / `DeleteRelations` signatures and behavior
- `HasPublicRelation` / `HasPublicSubject` public signatures (only the `HasPublicRelation` body is rewritten)
- `Caveats` and `Expirations` sub-structs on `<Rel>Objects` (write-side surface; this job only changes the read-side return)
- The `<Rel>Objects` write structs themselves
- Adapter (`internal/generator/adapter.go`) — `AllowedType` already carries the fields needed
- Schema acceptance — no new schema constructs; existing `with caveat` and `with expiration` round-trip identically through the new read path
- Round-trip idempotency invariant (`git diff --quiet example/authzed/` after regen) holds at the new baseline

## Implementation Order

    1. WS1 Runtime types       ← foundation; nothing depends on the codegen yet
    2. WS2 Engine method       ← depends on WS1 (RelationTuple); must land atomically with WS3 to avoid var _ Engine = (*spicedb.Engine)(nil) breakage
    3. WS3 Codegen template    ← depends on WS2 (engine return type)
    4. WS4 Fixture migration   ← depends on WS3 (regenerated .gen.go files); test migration follows
    5. WS5 Tests               ← can parallel with WS4's test migration; new tests cover metadata-surfacing
    6. WS6 Docs + release      ← last; depends on everything else passing

WS2 and WS3 land as one commit (atomic batch — interface and impl change together). WS4 lands as a follow-on commit (mechanical migration). WS5 lands alongside WS4 (test additions for new metadata surfacing). WS6 closes the cycle.

## Notes

- Round-trip the example fixture before declaring any generator change done. Per `.claude/CLAUDE.md`: `go run ./cmd/authzed-codegen --output example/authzed example/schema.zed && git diff --quiet example/authzed/`.
- Full e2e suite must pass: `go test ./pkg/authz/spicedb/... ./example/authzed/...`. Tests skip cleanly when Docker is unavailable via `spicedbtest.ErrDockerUnavailable`.
- Version bump is `1.4.0` (minor). The change is technically breaking (`Engine.ReadRelations` return type), but the only consumer is this repo and we're in active development — staying on minor per established convention. Document the API change in CHANGELOG with an explicit migration recipe so external adopters (if they appear) have a clean upgrade path.
- `harness validate-pr-checklist` will hard-block a push with `Status=Done` while any task row is `[ ]` (per AUZ-007 chore-commit experience). Flip checkboxes as work progresses.
- The `<Rel><Type>Relation` struct name might collide with type names already in use elsewhere in the codegen output. Audit before committing the template change — fallback name is `<Rel><Type>RelationTuple` if collision found.

## Discoveries & Decisions During Implementation

### [Implementer] No new template helper needed for struct name

WS3 task 3.1 anticipated a `permRelationStructName` template helper to format `<Pascal(ResourceDef)><Pascal(RelationName)><Pascal(Namespace)>Relation`. In practice the existing template variables (`$typeName`, `snakeToPascal $rel.Name`, `typeName $relType.Namespace`) compose inline cleanly with no readability loss — adding a helper would have introduced one more indirection for no payoff. Marked the task done with a note rather than authoring dead code.

### [Implementer] LSP cache severely stale after engine signature change

Throughout WS2-WS4 the IDE diagnostics layer reported phantom errors (`bookingsvc.User does not satisfy interface{RelationID() T}`) on call sites that compiled cleanly. Root cause: the LSP server cached the pre-WS3 generator output and didn't re-read `.gen.go` files after `go run ./cmd/authzed-codegen`. Authoritative signal during this kind of regen-heavy work is `go vet ./...` returning empty output, not the stale diagnostics stream. No action needed beyond ignoring the warnings.

### [Implementer] Test-side migration was 18 call sites, not the originally-feared churn

Initial concern was that variant C (replacing Read return types uniformly) would cascade into deep test rewrites. Final tally: 18 `assert.Equal(t, []<Type>{...}, X)` sites, each closed with one `authz.IDsOf(X)` wrap at the assert. No test logic changed. Confirms the active-development consumer profile — breaking API changes are bounded by current internal usage, not hypothetical external adopters.

### [Implementer] Wildcard split preserved cleanly via existing `uniqueByNamespace` dedup

SPEC-005 worried about the (Namespace, IsWildcard) collision case for `Read<Rel><Type>Wildcard` emission — namespaces appearing as both wildcard AND concrete on the same relation would lose the wildcard method. Audit confirmed: no fixture relation in `example/schema.zed` mixes both branches on the same namespace, so the pre-existing `uniqueByNamespace` (which keeps first-seen) didn't surface a real regression. Documented as a pre-existing limitation rather than introducing a fix.
