# [SPEC-010] Schema Drift Detection

| Field      | Value                                          |
|------------|------------------------------------------------|
| Status     | Accepted                                       |
| Created    | 2026-05-09                                     |
| Author     | Danh Tran                                      |
| Implements | (deploy-time safety net for codegen-vs-deployed schema mismatches) |

---

## Overview

This SPEC adds runtime detection of mismatch between the schema the binary's codegen was built from and the schema currently deployed in SpiceDB. Today a binary built against schema v1 can talk to a SpiceDB running schema v2 without complaint — silent permission denies (or worse, silent grants) result. The codegen embeds the source `.zed` text as a constant in a new top-level generated file `schema.gen.go`; a runtime helper `<rootpkg>.VerifySchema(ctx)` calls SpiceDB's `DiffSchema` RPC, server-side normalized comparison returns typed `ReflectionSchemaDiff` entries, and the helper buckets them into Added / Removed / Changed / Cosmetic categories. Caller hard-fails at startup on `IsBreaking()` and warns on additive drift.

**What this component does:** Add `SchemaDrift` and `DriftEntry` runtime types in `pkg/authz/authz.go` with `IsBreaking()` / `IsClean()` predicates. Add `Engine.DiffSchema(ctx, comparisonSchema)` interface method returning raw `[]*v1.ReflectionSchemaDiff`. Implement `*spicedb.Engine.DiffSchema` calling the SpiceDB `SchemaService.DiffSchema` RPC. Extend the codegen CLI to emit `<output-dir>/schema.gen.go` with `SchemaText` (verbatim source bytes), `SchemaDigest` (sha256), and a typed `VerifySchema(ctx) (SchemaDrift, error)` helper that internally calls the engine, buckets the raw diffs by category, and returns the typed result. Derive the new package name from the output dir's last path component (e.g. `--output example/authzed` → `package authzed`).

**What this component does not do:** Auto-invoke at engine init — drift detection is opinionated (fail-fast vs log-and-continue is caller's call); the helper is caller-driven. Auto-write the deployed schema if drift is detected — verification is read-only. Provide a CLI mode (`authzed-codegen --verify`) — out of scope; could be a future tool. Categorize partial drift severity beyond the 4 buckets — `DriftEntry.Raw` exposes the typed `*ReflectionSchemaDiff` for callers needing finer-grained handling. Embed the schema as anything other than the source `.zed` bytes — preserves comments and formatting so DiffSchema's normalization is the single source of truth for "are these the same."

---

## Interface Contracts

### Runtime types — `pkg/authz/authz.go`

```go
// SchemaDrift is the result of comparing the codegen-baseline schema
// against the currently deployed schema in SpiceDB. Buckets the raw
// ReflectionSchemaDiff entries by severity:
//   - Added/Cosmetic are safe (deployed schema is ahead or just doc changes)
//   - Removed/Changed are breaking (deployed schema lacks something the
//     binary depends on, or evaluates differently)
type SchemaDrift struct {
    Added    []DriftEntry
    Removed  []DriftEntry
    Changed  []DriftEntry
    Cosmetic []DriftEntry
}

// IsBreaking reports whether any breaking drift exists (Removed or Changed).
// Caller typically hard-fails at startup when this is true.
func (d SchemaDrift) IsBreaking() bool {
    return len(d.Removed) > 0 || len(d.Changed) > 0
}

// IsClean reports whether the deployed schema matches the codegen baseline
// exactly across all four buckets.
func (d SchemaDrift) IsClean() bool {
    return len(d.Added)+len(d.Removed)+len(d.Changed)+len(d.Cosmetic) == 0
}

// DriftEntry is one row of drift. Description is human-readable for logs;
// Raw is the typed wire-level diff for callers needing programmatic access
// to specific oneof variants (e.g. PermissionExprChanged.Old / .New).
type DriftEntry struct {
    Description string
    Raw         *v1.ReflectionSchemaDiff
}
```

The runtime types live in `pkg/authz/`, but `DriftEntry.Raw` is `*v1.ReflectionSchemaDiff` from `github.com/authzed/authzed-go/proto/authzed/api/v1`. Per A1 — `pkg/authz/` already imports authzed-go via the engine impl, but `pkg/authz/authz.go` itself doesn't currently reference proto types. Adding the import is acceptable: the type is the canonical wire shape and copying it into `pkg/authz/` would force the runtime layer to track upstream changes.

### Engine interface — `pkg/authz/authz.go`

```go
type Engine interface {
    // ... existing methods unchanged ...

    // DiffSchema calls SpiceDB's SchemaService.DiffSchema RPC with the
    // caller-supplied comparisonSchema (typically the codegen baseline
    // SchemaText constant). Returns the raw typed diffs for the codegen
    // helper to bucket; or empty slice when schemas match.
    DiffSchema(ctx context.Context, comparisonSchema string) ([]*v1.ReflectionSchemaDiff, error)
}
```

### `*spicedb.Engine` implementation — `pkg/authz/spicedb/crud.go`

Per A2 — the SpiceDB Go client exposes `SchemaService` via `client.SchemaServiceClient` accessor:

```go
func (e *Engine) DiffSchema(ctx context.Context, comparisonSchema string) ([]*v1.ReflectionSchemaDiff, error) {
    e.debugLog("Diffing schema: comparison_schema length=%d", len(comparisonSchema))
    res, err := e.client.SchemaServiceClient.DiffSchema(ctx, &v1.DiffSchemaRequest{
        ComparisonSchema: comparisonSchema,
    })
    if err != nil {
        return nil, err
    }
    return res.GetDiffs(), nil
}
```

Consistency override is intentionally NOT applied — the schema service has its own consistency model (always reads the latest schema). Per A3.

### Generated `<output-dir>/schema.gen.go` — new file

The codegen CLI emits ONE additional file at the output root (a level above the per-namespace subdirectories). Package name derived from the output dir's last path component.

For `--output example/authzed`:

```go
// example/authzed/schema.gen.go
// Code generated by authzed-codegen. DO NOT EDIT.

package authzed

import (
    "context"
    "fmt"

    v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
    "github.com/danhtran94/authzed-codegen/pkg/authz"
)

// SchemaText is the canonical schema this binary's codegen was built from.
// Use VerifySchema(ctx) at startup to compare against the deployed schema.
const SchemaText = `<verbatim schema bytes>`

// SchemaDigest is sha256(SchemaText). Cheap pre-flight equality check;
// DiffSchema gives the rich diff when digests don't match.
const SchemaDigest = "sha256:<hex>"

// VerifySchema compares SchemaText against the deployed schema in SpiceDB
// and returns a typed SchemaDrift partitioning the diffs into severity
// buckets. Caller decides how to react (fail-fast on .IsBreaking(),
// log-and-continue on additive drift).
//
// Uses the engine registered via authz.SetDefaultEngine.
func VerifySchema(ctx context.Context) (authz.SchemaDrift, error) {
    diffs, err := authz.GetEngine(ctx).DiffSchema(ctx, SchemaText)
    if err != nil {
        return authz.SchemaDrift{}, fmt.Errorf("verify schema: %w", err)
    }
    return bucketDrift(diffs), nil
}

func bucketDrift(diffs []*v1.ReflectionSchemaDiff) authz.SchemaDrift {
    var out authz.SchemaDrift
    for _, d := range diffs {
        entry := authz.DriftEntry{Description: describeDrift(d), Raw: d}
        switch d.GetDiff().(type) {
        case *v1.ReflectionSchemaDiff_DefinitionAdded,
            *v1.ReflectionSchemaDiff_RelationAdded,
            *v1.ReflectionSchemaDiff_PermissionAdded,
            *v1.ReflectionSchemaDiff_RelationSubjectTypeAdded,
            *v1.ReflectionSchemaDiff_CaveatAdded,
            *v1.ReflectionSchemaDiff_CaveatParameterAdded:
            out.Added = append(out.Added, entry)
        case *v1.ReflectionSchemaDiff_DefinitionRemoved,
            *v1.ReflectionSchemaDiff_RelationRemoved,
            *v1.ReflectionSchemaDiff_PermissionRemoved,
            *v1.ReflectionSchemaDiff_RelationSubjectTypeRemoved,
            *v1.ReflectionSchemaDiff_CaveatRemoved,
            *v1.ReflectionSchemaDiff_CaveatParameterRemoved:
            out.Removed = append(out.Removed, entry)
        case *v1.ReflectionSchemaDiff_PermissionExprChanged,
            *v1.ReflectionSchemaDiff_CaveatExprChanged,
            *v1.ReflectionSchemaDiff_CaveatParameterTypeChanged:
            out.Changed = append(out.Changed, entry)
        case *v1.ReflectionSchemaDiff_DefinitionDocCommentChanged,
            *v1.ReflectionSchemaDiff_RelationDocCommentChanged,
            *v1.ReflectionSchemaDiff_PermissionDocCommentChanged,
            *v1.ReflectionSchemaDiff_CaveatDocCommentChanged:
            out.Cosmetic = append(out.Cosmetic, entry)
        }
    }
    return out
}

func describeDrift(d *v1.ReflectionSchemaDiff) string {
    // ... switch on d.GetDiff() type, format human-readable description ...
}
```

### CLI change — `cmd/authzed-codegen/main.go`

After the existing per-namespace file generation, the CLI:

1. Computes the SHA-256 digest of the source schema bytes
2. Derives the package name from `outputPath` last segment (via `utilstr.PackageName`)
3. Writes `<outputPath>/schema.gen.go` from a new template `schema.go.tmpl`

The schema text is captured at the same point as the existing `schemaBytes := os.ReadFile(schemaPath)` read.

### Caller pattern

```go
// Application startup:
import (
    authzedgen "github.com/danhtran94/authzed-codegen/example/authzed"
    "github.com/danhtran94/authzed-codegen/pkg/authz"
    spicedbengine "github.com/danhtran94/authzed-codegen/pkg/authz/spicedb"
)

func main() {
    engine := spicedbengine.NewEngine(client, 3*time.Second)
    authz.SetDefaultEngine(engine)

    drift, err := authzedgen.VerifySchema(ctx)
    if err != nil {
        log.Fatalf("schema verification failed: %v", err)
    }
    if drift.IsBreaking() {
        log.Fatalf("schema drift: %d removed, %d changed — refusing to start",
            len(drift.Removed), len(drift.Changed))
    }
    if !drift.IsClean() {
        log.Warnf("schema is ahead: %d added, %d cosmetic", len(drift.Added), len(drift.Cosmetic))
    }
    // ... continue with normal startup ...
}
```

---

## Sequence

Wire flow at startup:

```
caller code:
    drift, err := authzed.VerifySchema(ctx)
         │
         ▼
generated VerifySchema:
    ├─► engine.DiffSchema(ctx, authzed.SchemaText)

         │
         ▼
*spicedb.Engine.DiffSchema:
    └─► client.SchemaService.DiffSchema(ctx, &DiffSchemaRequest{
            ComparisonSchema: authzed.SchemaText,
        })

         │
         ▼
SpiceDB SchemaService:
    ├─► parses comparisonSchema, parses deployed schema
    ├─► structural comparison → []ReflectionSchemaDiff
    └─► returns typed diffs (or empty when match)

         │
         ▼
generated bucketDrift:
    ├─► switch on each diff's typed oneof variant
    ├─► Added if *_Added; Removed if *_Removed; Changed if *_Changed;
    │   Cosmetic if *_DocCommentChanged
    └─► return SchemaDrift{Added, Removed, Changed, Cosmetic}

         │
         ▼
caller branches:
    if drift.IsBreaking() { hard-fail }
    else if !drift.IsClean() { log.Warn }
    else { proceed }
```

---

## Errors

| Error class | Trigger | Layer |
|---|---|---|
| `gRPC error` | SpiceDB unreachable, auth failure, malformed request | Engine — passed through |
| `INVALID_ARGUMENT` | `comparisonSchema` fails to parse server-side | Engine — gRPC error from SpiceDB |
| `PermissionsAPI: NOT_FOUND` (per A2 — schema not yet deployed) | Empty SpiceDB | Engine — surfaced unwrapped; caller's choice (drift? bootstrap?) |
| `verify schema: <wrapped>` | `engine.DiffSchema` returned a non-nil error | Codegen helper — wrapped with context |

Empty diffs when schemas match. Per C4 — a clean schema returns `SchemaDrift{}` (all four slices nil), and `IsClean()` returns true.

---

## Constraints

- **C1.** `SchemaText` is the verbatim source `.zed` bytes — comments, whitespace, formatting preserved. SpiceDB's `DiffSchema` parses both sides server-side; cosmetic differences land in the `Cosmetic` bucket. Per A4 — DiffSchema normalises before comparing.

- **C2.** `SchemaDigest` uses sha256 of the same bytes. Per A5 — cheap pre-flight equality is useful for callers wanting to skip the gRPC roundtrip when the binary's digest is reported by an out-of-band mechanism (e.g. config server stores deployed schema hash).

- **C3.** Generated `schema.gen.go` lives at the codegen output root (NOT inside any namespace subdirectory). Package name derives from the output path's last segment via `utilstr.PackageName`. For `--output example/authzed` → `package authzed`. For `--output zed` (the CLI default) → `package zed`.

- **C4.** `SchemaDrift{}.IsClean()` returns true (empty slices). The engine returns empty `[]*ReflectionSchemaDiff` when schemas match; the bucketing helper skips the loop.

- **C5.** `DriftEntry.Raw` exposes the typed wire diff for callers needing fine-grained handling. The 17-case oneof is preserved as a forward-compat escape hatch.

- **C6.** `VerifySchema` does NOT auto-invoke at engine init. Caller-driven; engine is unaware of any specific schema baseline. Mirrors the existing pattern where `Engine` doesn't bake schema knowledge.

- **C7.** Consistency override is not applied to `DiffSchema`. The schema service has its own consistency model (always reads the latest schema); per A3, applying `Consistency_FullyConsistent` would be redundant or rejected.

- **C8.** Round-trip idempotency stable. The new `schema.gen.go` regenerates byte-identical to the previous run if the source `.zed` bytes haven't changed (sha256 is deterministic, the file content is verbatim text). Per the AUZ-010 round-trip discipline.

- **C9.** No write-side change. SpiceDB `WriteSchema` is a separate RPC, used for schema deployment (not in scope here). SPEC-010 only ADDS the read-side detection.

- **C10.** The codegen CLI continues to accept the existing `--output` flag without breaking. Generating `schema.gen.go` is unconditional — every codegen run produces it. No new flag needed in v1.9.

- **C11.** The `describeDrift` helper formats a human-readable line per diff (e.g. `"definition extsvc/folder removed"`). Format is illustrative; SPEC does not pin the exact strings — they're for logs, not for parsing.

---

## Assumptions

- **A1 [VERIFIED]:** `pkg/authz/` is allowed to import authzed-go proto types. Evidence: while `pkg/authz/authz.go` doesn't currently reference proto types, the `pkg/authz/spicedb/` engine impl already imports `github.com/authzed/authzed-go/proto/authzed/api/v1`. The proto types are stable cross-version. Adding a single proto import to `authz.go` for `*v1.ReflectionSchemaDiff` matches existing dependency patterns.

- **A2 [VERIFIED]:** SpiceDB's `SchemaService.DiffSchema` is exposed on the authzed-go `*authzed.Client`. Evidence: `go doc github.com/authzed/authzed-go/proto/authzed/api/v1 SchemaServiceClient` confirms the RPC; the `*authzed.Client` embeds `SchemaServiceClient` directly, accessible via `e.client.DiffSchema(ctx, req)`.

- **A3 [EXTERNAL FACT]:** SpiceDB's schema service uses its own consistency model (always reads the latest schema). The `DiffSchemaRequest` proto includes a `Consistency` field for completeness but the typical use is leaving it nil. Per Authzed docs — schema reads are not subject to the same staleness as relationship reads.

- **A4 [EXTERNAL FACT]:** `DiffSchema` parses both schemas server-side and returns structural diffs. Whitespace, comments, ordering, formatting are normalised before comparison. Verified by inspecting the `ReflectionSchemaDiff` oneof — it operates at the AST level (definitions, relations, permissions, caveats), not at the text level.

- **A5 [VERIFIED]:** `crypto/sha256` is part of the Go stdlib. Generating a hex digest of the schema bytes is trivial: `fmt.Sprintf("sha256:%x", sha256.Sum256(schemaBytes))`. The constant lands at codegen time.

- **A6 [VERIFIED]:** The codegen CLI has access to the source schema bytes — `schemaBytes := os.ReadFile(schemaPath)` happens before `compiler.Compile`. Embedding into a generated string literal is straightforward; Go raw string literals (backticks) preserve all bytes except the backtick character itself, which is rare in `.zed` schemas.

- **A7 [HYPOTHESIS]:** No `.zed` schema in this codebase or in typical SpiceDB use contains a backtick character. If one does, the codegen falls back to interpreted-string-literal escaping. Verified by grepping `example/schema.zed` — no backticks present.

---

## Unresolved Questions

(none)

---

## Summary

Net change scope:

| File | Change |
|---|---|
| `pkg/authz/authz.go` | Add `SchemaDrift` struct (`Added/Removed/Changed/Cosmetic []DriftEntry`) with `IsBreaking()` / `IsClean()` predicates. Add `DriftEntry` struct (`Description string` + `Raw *v1.ReflectionSchemaDiff`). Add `Engine.DiffSchema(ctx, comparisonSchema string) ([]*v1.ReflectionSchemaDiff, error)` interface method. Import `github.com/authzed/authzed-go/proto/authzed/api/v1` (new dep for this file). |
| `pkg/authz/spicedb/crud.go` | Implement `*Engine.DiffSchema` — single gRPC call to `e.client.SchemaServiceClient.DiffSchema`. |
| `internal/templates/schema.go.tmpl` | NEW. Template for the top-level `schema.gen.go`. Contains `SchemaText`, `SchemaDigest`, `VerifySchema`, `bucketDrift`, `describeDrift`. |
| `internal/templates/templates.go` | Embed the new template alongside the existing `object.go.tmpl`. |
| `internal/generator/generator.go` | Add a method to write `<OutputPath>/schema.gen.go` from the new template. CLI invokes it once per generation. |
| `cmd/authzed-codegen/main.go` | After existing per-namespace generation, invoke the new schema-template emit. Pass the schema bytes + derived package name. |
| `example/authzed/schema.gen.go` | NEW generated file at `example/authzed/`. Package `authzed`. |
| `example/authzed/extsvc/extsvc_test.go` | E2E tests covering: clean state (deployed schema matches → IsClean), additive drift (write a schema with extra definition deployed → Added populated), breaking drift (write a schema missing a definition → Removed populated, IsBreaking true). |

E2E tests verify each of the 4 buckets populates correctly and the helper's `IsBreaking()` / `IsClean()` predicates behave as documented.

---

## History

(History is owned by `harness history-update` — do not hand-edit.)
