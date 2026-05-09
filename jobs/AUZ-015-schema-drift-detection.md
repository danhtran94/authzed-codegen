# AUZ-015: Schema Drift Detection

| Field      | Value                                          |
|------------|------------------------------------------------|
| Status     | Done                                            |
| Created    | 2026-05-09                                     |
| Assignee   | danhtran94                                     |
| Source     | docs/spec-010-schema-drift-detection.md        |
| Blocked by | —                                              |

<!-- approved -->

---

## Goal

Catch a class of silent production bugs: binary built against schema v1 calls SpiceDB running schema v2 → mis-permission everything with no error path. After this job, the codegen embeds the source schema as a constant in a new top-level `schema.gen.go` and exposes `<rootpkg>.VerifySchema(ctx)` that calls SpiceDB's `DiffSchema` RPC and buckets the typed diffs into Added / Removed / Changed / Cosmetic. Caller hard-fails at startup on `IsBreaking()`. Closes the deploy-time-mismatch detection gap that's been the biggest remaining production-readiness item.

## Problem

    Current (post-v1.8.0):
      ops deploys binary with codegen baseline = schema-v1
        SpiceDB cluster running schema-v2 (someone updated SpiceDB schema directly)
          → binary calls Check<Perm>, Lookup<Perm>...
            ✗ silent permission denies / wrong evaluations
            ✗ no error path; debugging is "just doesn't work as expected"

The deployed schema can drift from the codegen baseline through several routes: someone runs `zed schema write` directly without rebuilding the binary; binary deployed before schema migration completed; binary deployed with stale codegen output. Today the codegen has no awareness of the deployed schema; the engine just calls Check / Lookup with whatever permission name the schema-v1 binary knows, and SpiceDB returns whatever schema-v2 says (which may be deny, conditional, or a different evaluation).

## Solution: Embed schema, runtime DiffSchema RPC, typed drift buckets

    After fix:
      ops deploys binary:
        startup hook:
          drift, err := authzed.VerifySchema(ctx)
            → engine.DiffSchema(ctx, embedded SchemaText)
              → SpiceDB SchemaService.DiffSchema returns []ReflectionSchemaDiff
                → buckets by category:
                    Added (additive drift) — safe
                    Removed (something binary uses is gone) — breaking
                    Changed (permission expr or caveat eval differs) — breaking
                    Cosmetic (doc comment changes) — safe
          if drift.IsBreaking() { os.Exit(1) }
          ✓ ops sees actionable startup error before serving traffic

The codegen captures the source `.zed` bytes verbatim and embeds them into `<output-dir>/schema.gen.go` (new file at the output root, package name derived from the dir's last segment). Single source of truth for "what schema the binary expects."

### Components

**`authz.SchemaDrift`** — runtime drift report; `Added/Removed/Changed/Cosmetic []DriftEntry` slices with `IsBreaking()` / `IsClean()` predicates.

**`authz.DriftEntry`** — single drift row; `Description string` for logs + `Raw *v1.ReflectionSchemaDiff` for programmatic handling.

**`Engine.DiffSchema`** — new interface method calling SpiceDB's `SchemaService.DiffSchema` RPC; returns raw typed diffs.

**`<rootpkg>.SchemaText`** + **`<rootpkg>.SchemaDigest`** — generated constants per codegen run.

**`<rootpkg>.VerifySchema(ctx)`** — generated helper calling the engine method, bucketing diffs into typed `SchemaDrift`.

### Why not alternatives

| Approach | Verdict |
|---|---|
| **Embed schema text + DiffSchema RPC** (chosen) | Server-side normalised comparison via SpiceDB's parser. Rich typed diffs. Caller controls fail-fast policy. |
| Digest-only equality (sha256(schema)) | Rejected. Whitespace / comment / ordering changes false-positive every time someone reformats the schema. Requires normalised comparison. |
| Parse client-side, compare ASTs | Rejected. Reimplements SpiceDB's parser. SpiceDB already has it server-side via `DiffSchema`. |
| Auto-invoke at engine init | Rejected. Drift detection is opinionated (fail-fast vs log-and-continue is caller's call). Engine doesn't bake schema knowledge. |
| Separate CLI tool (`authzed-codegen --verify`) | Out of scope. Future tool; runtime helper is the more useful primitive. |

## Workstreams

### 1. Runtime types

Add `SchemaDrift`, `DriftEntry`, and the new Engine interface method in `pkg/authz/`. Foundation for the engine impl + codegen helper.

| #   | Task | File | Status |
|-----|------|------|--------|
| 1.1 | Add `SchemaDrift` struct with `Added/Removed/Changed/Cosmetic []DriftEntry` fields and `IsBreaking()` / `IsClean()` methods | `pkg/authz/authz.go` | [x] |
| 1.2 | Add `DriftEntry` struct with `Description string` and `Raw *v1.ReflectionSchemaDiff` fields | same | [x] |
| 1.3 | Add `Engine.DiffSchema(ctx context.Context, comparisonSchema string) ([]*v1.ReflectionSchemaDiff, error)` interface method | same | [x] |
| 1.4 | Add proto import (`github.com/authzed/authzed-go/proto/authzed/api/v1`) to `pkg/authz/authz.go` | same | [x] |

**Key details:** Per SPEC-010 A1 — `pkg/authz/` is allowed to depend on authzed-go proto types; existing pattern via the engine layer. `DriftEntry.Raw` exposes the typed oneof for callers needing the wire-level payload (`PermissionExprChanged.Old`, etc.).

### 2. Engine impl

Single gRPC call wrapping SpiceDB's `SchemaService.DiffSchema`. Atomic batch with WS1 (interface + impl).

| #   | Task | File | Status |
|-----|------|------|--------|
| 2.1 | Implement `*Engine.DiffSchema(ctx, comparisonSchema)` calling `e.client.DiffSchema(ctx, &v1.DiffSchemaRequest{ComparisonSchema: comparisonSchema})`; return `res.GetDiffs()` | `pkg/authz/spicedb/crud.go` | [x] |

**Key details:** Per SPEC-010 A2 / A3 — schema service has its own consistency model; do NOT apply `getConsistencySnapshot(ctx)`. The DiffSchema RPC reads the latest schema unconditionally.

### 3. Codegen — new schema template + CLI hook

Create the new template, embed it via the existing templates package, and have the CLI emit `<output>/schema.gen.go` with schema bytes + sha256 digest.

| #   | Task | File | Status |
|-----|------|------|--------|
| 3.1 | Create `internal/templates/schema.go.tmpl` containing `SchemaText`, `SchemaDigest`, `VerifySchema`, `bucketDrift`, `describeDrift` | `internal/templates/schema.go.tmpl` | [x] |
| 3.2 | Update `internal/templates/embed.go` to embed the new template and expose it | `internal/templates/embed.go` | [x] |
| 3.3 | Add `Generator.GenerateSchemaSource(schemaBytes []byte)` method to write `<OutputPath>/schema.gen.go` from the schema template; package name from `utilstr.PackageName(filepath.Base(g.OutputPath))` | `internal/generator/generator.go` | [x] |
| 3.4 | CLI hook in `cmd/authzed-codegen/main.go`: after the existing `g.GenerateObjectSource("[object]")` call, invoke `g.GenerateSchemaSource(schemaBytes)` with the bytes already read | `cmd/authzed-codegen/main.go` | [x] |

**Key details:** Per SPEC-010 C3 — package name derives from output dir's last segment via `utilstr.PackageName`. Per A6 — the schema bytes are already read in `main.go` (`schemaBytes := os.ReadFile(schemaPath)`), just need to thread them to the new generator method. Per A7 — Go raw string literals work for `.zed` content; falls back to interpreted-string escaping if a backtick is found.

### 4. Fixture regeneration + verification

Regenerate the example output. The new file `example/authzed/schema.gen.go` lands at the output root.

| #   | Task | File | Status |
|-----|------|------|--------|
| 4.1 | Run codegen — `go run ./cmd/authzed-codegen --output example/authzed example/schema.zed` — commit the new `example/authzed/schema.gen.go` file | `example/authzed/schema.gen.go` (new) | [x] |
| 4.2 | Verify per-namespace `.gen.go` files are byte-identical to v1.8.0 (codegen change is purely additive — no template change to object.go.tmpl) | `example/authzed/**/*.gen.go` | [x] |
| 4.3 | Verify the new `schema.gen.go` round-trips byte-identical on a second codegen run | same | [x] |

**Key details:** Per SPEC-010 C8 — sha256 is deterministic; the file content is verbatim text; second run produces zero diff. Per A7 — verify schema.zed has no backticks (already confirmed in research).

### 5. Testing — drift bucket coverage

E2E tests against live SpiceDB cover the four drift categories.

| #   | Task | Status |
|-----|------|--------|
| 5.1 | E2E: clean state — schema deployed matches `SchemaText`; `VerifySchema(ctx).IsClean()` returns true — `example/authzed/schema_test.go` | [x] |
| 5.2 | E2E: additive drift — deploy a schema variant with one extra definition; `drift.Added` contains 1 entry with `*ReflectionSchemaDiff_DefinitionRemoved` raw (SpiceDB direction is from-comparison-perspective) — same | [x] |
| 5.3 | E2E: breaking drift (Removed) — deploy a schema variant missing one definition; `drift.Removed` contains entries; `drift.IsBreaking()` returns true — same | [x] |
| 5.4 | E2E: cosmetic drift — deploy a schema with a doc comment difference; `drift.Cosmetic` contains entries; `drift.IsBreaking()` returns false; `drift.IsClean()` returns false — same | [x] |
| 5.5 | E2E: regression sweep — full e2e suite passes after WS1-WS4 — `go test ./pkg/authz/spicedb/... ./example/authzed/...` | [x] |

**Key details:** Tests use the spicedbtest harness's existing schema-write capability to install drifted variants; each test isolates to its own SpiceDB instance to avoid cross-test contamination. Per the existing AUZ-005 pattern.

### 6. Documentation + release prep

CHANGELOG, README, version bump.

| #   | Task | Status |
|-----|------|--------|
| 6.1 | Add `[1.9.0]` entry to `CHANGELOG.md` documenting the new types, helper, codegen change, and use case — `CHANGELOG.md` | [x] |
| 6.2 | Update `README.md` — add `Schema Drift Detection` section after `Consistency` showing the startup-hook caller pattern with example — `README.md` | [x] |
| 6.3 | Tag `v1.9.0` after merge; create GitHub release with notes calling out the production safety net | [x] |

## Design Decisions

### Use SpiceDB's DiffSchema RPC, not local comparison
SpiceDB has the parser; offload normalization. Whitespace/comment/ordering changes don't false-positive. Rich typed diffs available without reimplementing the parser.

### 4 severity buckets, not 17 typed cases
Mirror's SpiceDB's 17-case oneof but the common-case caller wants severity (breaking vs not). Buckets: Added (compatible), Removed/Changed (breaking), Cosmetic (safe). `DriftEntry.Raw` preserves the typed oneof for fine-grained handling.

### Single top-level `schema.gen.go` at output root
Schema is a SpiceDB-wide concept; one constant per package would be wrong. New top-level package alongside the per-namespace ones. Package name derives from the output dir's last segment.

### Caller-driven, not auto-invoked
Drift policy is opinionated (fail-fast vs log). Engine doesn't bake schema knowledge. Caller hooks at startup; codegen surfaces the data.

### Verbatim source bytes, not normalised AST
DiffSchema does the normalisation server-side. Embedding verbatim preserves comments + formatting; the only consequence is `Cosmetic` entries when comments diverge — which is the right behavior (informational, not breaking).

## What Stays Unchanged

- All existing `Engine.*` method signatures (Check, Lookup, Read, write paths)
- Per-namespace generated `.gen.go` files (no template change to `object.go.tmpl`)
- `internal/generator/adapter.go` and the existing tree-walker — DiffSchema doesn't need adapter-level info
- Codegen idempotency invariant — per-namespace files regenerate byte-identical to v1.8.0
- `--output` flag semantics — same path; codegen now emits one additional file at the root
- `pkg/authz/spicedbtest/` test harness — `WriteSchema` capability already there; tests reuse it
- README's existing sections on Caveats / Expiration / Sub-relation References / Conditional Permission / Consistency

## Implementation Order

    1. WS1 Runtime types       ← foundation; pure additions in pkg/authz/
    2. WS2 Engine impl         ← depends on WS1; atomic batch with WS1
    3. WS3 Codegen template    ← depends on WS2 (engine method to call)
    4. WS4 Fixture regeneration ← depends on WS3 (template emits the new file)
    5. WS5 Tests                ← depends on WS4 (fixture in place)
    6. WS6 Docs + release       ← last; depends on test pass

WS1 + WS2 land as one commit (atomic — interface and impl). WS3 + WS4 land together (template change requires regen). WS5 follows. WS6 closes.

## Notes

- No fixture changes to `example/schema.zed`. Tests deploy drifted variants via the spicedbtest harness's WriteSchema capability.
- Round-trip the example fixture before declaring any generator change done. Per `.claude/CLAUDE.md`.
- Full e2e suite must pass: `go test ./pkg/authz/spicedb/... ./example/authzed/...`.
- Version bump is `1.9.0` (minor). Pure additive; existing callers untouched.
- `harness validate-pr-checklist` will hard-block a push with `Status=Done` while any task row is `[ ]`.

## Discoveries & Decisions During Implementation

### [Implementer] SPEC-010 A7 hypothesis (no backticks in schema) was wrong

The verbatim-embed strategy assumed `.zed` schema content has no backtick characters, allowing direct emission as a Go raw string literal. Confirmed wrong: `example/schema.zed` contains inline-code references in comments (e.g. `` `with expiration` ``) which break raw string literals. First codegen run produced an unparseable `schema.gen.go`. Fixed by encoding via `strconv.Quote(string(schemaBytes))` — produces a valid Go interpreted string literal with all special characters escaped. Renamed `SchemaView.SchemaText` to `SchemaView.QuotedSchemaText` and updated the template to emit it directly. The constant in the generated file is functionally identical (it's a `string` either way); only the source representation changes.

### [Implementer] DiffSchema RPC direction is from-comparison-schema-perspective

SpiceDB's `DiffSchema` returns typed diffs as "operations to apply to the deployed schema to reach the comparison schema":
- `*_Added` = comparison schema has this, deployed schema lacks it
- `*_Removed` = deployed has this, comparison lacks it

From the binary's perspective (where comparison = our baseline `SchemaText`), this is INVERTED relative to the `SchemaDrift` API's caller-friendly bucketing:
- `*_Added` from SpiceDB → `SchemaDrift.Removed` (BREAKING — baseline expects but deployed lacks)
- `*_Removed` from SpiceDB → `SchemaDrift.Added` (ADDITIVE — deployed has extras)

Caught in WS5 — additive and breaking tests initially failed with empty drift slices because the bucketing was inverted. Fixed `bucketDrift` to swap the mapping; updated `describeDrift` strings to phrase from the binary's perspective (e.g. "X expected by baseline but missing from deployed" / "X present in deployed but not in baseline (extra)"). Added a doc comment to `bucketDrift` explaining the inversion so future readers don't have to re-derive it.

### [Implementer] Drift tests need isolated sandboxes

The shared `TestMain`-bootstrapped sandbox in `extsvc_test.go` deploys the canonical schema once and serves all tests in that package. Drift tests need different deployed schemas per scenario, which would conflict with other tests in the same sandbox. Solved by putting drift tests in a new package `authzed_test` (in `example/authzed/schema_test.go`) where each test boots its own SpiceDB sandbox via `spicedbtest.Start`. Adds ~5s of container start per test (4 tests = ~20s) but keeps each scenario isolated. The Sandbox is cleaned up via `t.Cleanup`. Added a new e2e package to the build.

### [Implementer] No template change to `object.go.tmpl`

SPEC-010 promised the per-namespace `.gen.go` files would be unchanged. Verified by post-regen `diff -q` against the v1.8.0 baseline — all `*.gen.go` files in `example/authzed/{extsvc,bookingsvc,menusvc}/` are byte-identical. Only the new `example/authzed/schema.gen.go` lands as a fresh file.
