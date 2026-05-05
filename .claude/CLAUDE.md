# `authzed-codegen` — Agent Context

## What this is

A build-time CLI that reads an AuthZED (SpiceDB) schema file and emits
type-safe Go stubs (`.gen.go`) for each `definition` block. The
generated code wraps a thin runtime in `pkg/authz/` so callers can
`Check<Permission>`, `Lookup<Permission>Resources`, and
`Create<Relation>Relations` against a SpiceDB engine without
hand-writing per-resource boilerplate.

## Stack
- Go 1.26+
- `github.com/authzed/spicedb` v1.52+ (parser backend — `pkg/schemadsl/compiler`)
- `github.com/authzed/authzed-go` v1.9+ (runtime client used by `pkg/authz/spicedb/`)
- `text/template` (codegen via `internal/templates/object.go.tmpl`)
- No DB, no message broker, no cloud. Pure build-time CLI.

## Project structure

    cmd/authzed-codegen/      → main entry point; calls compiler.Compile + generator
    internal/generator/       → core codegen
       adapter.go             → proto → DefinitionView; rejects unsupported constructs
       generator.go           → resolver + template execution
    internal/templates/       → embedded text/template + per-object template
    internal/utilstr/         → string-mangling helpers (PascalCase, package names)
    pkg/authz/                → runtime types (Type, Relation, Permission, Engine)
    pkg/authz/spicedb/        → SpiceDB engine implementation
    example/                  → schema.zed + checked-in generated output (bookingsvc, menusvc, extsvc)
    docs/                     → ADRs, RFCs, scope notes, SPECs
    jobs/                     → job docs for `/job` workflow

## Architecture rules

- `pkg/authz/` defines the runtime contract (interfaces, ID types). Generated code imports it. It must not import `internal/`.
- `internal/generator/` owns the proto-to-template adapter. Proto types from `github.com/authzed/spicedb/pkg/proto/core/v1` only appear in `adapter.go` — the rest of the generator consumes `*DefinitionView`.
- `internal/templates/` is import-free except `embed`. The template's `gotype` comment names `generator.DefinitionView`.
- `cmd/authzed-codegen/` is pure wiring: read file → `compiler.Compile` → `generator.AdaptDefinitions` → `Generator.GenerateObjectSource`.

## Build / verify

No Makefile. The full verification loop:

    go build ./...
    go vet ./...
    go mod tidy
    go run ./cmd/authzed-codegen --output example/authzed example/schema.zed
    git diff --quiet example/authzed/      # round-trip must be zero-diff
    go test ./pkg/authz/spicedb/... \
            ./example/authzed/bookingsvc/... \
            ./example/authzed/menusvc/... \
            ./example/authzed/extsvc/...

The fixture round-trip remains the codegen regression bar —
`example/schema.zed` must regenerate `example/authzed/**.gen.go`
byte-identical to the committed version.

E2E coverage for the generated stubs lives in the four test packages
above (added per AUZ-005). Each `TestMain` calls
`spicedbtest.Start(ctx, schema)` to launch a SpiceDB Docker container
via `testcontainers-go`; tests that can't reach Docker are skipped
cleanly via the `spicedbtest.ErrDockerUnavailable` sentinel.

## Codegen scope (what's accepted vs rejected)

The SpiceDB compiler accepts the full AuthZED grammar; the **codegen**
layer is narrower. `internal/generator/adapter.go` rejects unsupported
constructs at adapt time with schema-relative errors:

- ✓ Union (`+`), arrow (`->`), wildcard relations (`type:*`) — Wildcards sub-struct on Objects + sibling Read/Lookup wildcard methods
- ✓ Intersection (`&`), exclusion (`-`) — structurally identical to union at codegen time (see `internal/generator/adapter.go:132`)
- ✗ Caveats (`with <caveat>`), expiration traits (`with expiration`)
- ✗ Sub-relation references (`foo#bar`)
- ✗ `_this`, `_nil`, `_self`, functioned tuple-to-userset

Adding support means extending `lowerSetOperationChild` /
`flattenAllowedTypes` and a corresponding template branch. See
`docs/ADR-001-parser-migration.md`.

## Where things live

The harness separates **skill definitions** (shared across every
project) from **work artifacts** (per-repo). These two live in
different places and never cross.

- **This repo** (walked from `.claude/harness.json`, module-scoped):
  - `jobs/<PREFIX>-NNN-*.md` — job docs produced by `/job` (PREFIX is `AUZ`)
  - `docs/scope-*.md`, `docs/RFC-*.md`, `docs/ADR-*.md`, `docs/spec-*.md` — planning artifacts produced by `/doc`
  - `docs/.drafts/**` — in-progress `/doc` session checkpoints
  - `.claude/harness.json`, `.claude/settings.json` — harness config
- **`~/.claude/` (global install)**:
  - `skills/<name>/SKILL.md` — skill definitions (invoked by `/job`, `/doc`, `/review`, `/pr`)
  - `commands/<name>.md` — slash command surface
  - `CLAUDE.md`, `HARNESS_WRITING_RULES.md` — global agent context
  - `settings.json` — global hook wiring

Never create `~/.claude/jobs/`, `~/.claude/docs/`, or any
workspace artifact under the global install. The harness binary
resolves paths relative to the nearest `.claude/harness.json`
ancestor — never global. If you catch yourself about to Write
under `~/.claude/jobs/` or `~/.claude/docs/`, stop and recheck
the module root via `harness doctor`.

## Workflow Contract

The harness ships four lifecycle skills covering plan → implement → review → ship. Each has a slash-command surface and a `SKILL.md` consulted at invocation:

- `/doc` — `~/.claude/skills/writing-docs/SKILL.md` — author RFC / ADR / scope note / SPEC. Per-type discipline in `~/.claude/skills/writing-docs/{rfc,adr,scope,spec}-discipline.md`. Trigger-pointer preloaded via `~/.claude/HARNESS_WRITING_RULES.md`.
- `/job` — `~/.claude/skills/executing-jobs/SKILL.md` — execute multi-file work derived from an approved source doc. Plan doc required before any implementation.
- `/review` — `~/.claude/skills/reviewing-code/SKILL.md` — architectural review against boundaries, error handling, concurrency, security, testing.
- `/pr` — `~/.claude/skills/preparing-prs/SKILL.md` — pre-push validation gate + PR body composition.

Operating rules:
- One task at a time; verify build after every change
- `harness validate-docs` is reporting-tier (exit 0); `validate-plan` and `validate-pr-checklist` are blocking-tier (binary)
- Round-trip the example fixture (`go run ./cmd/authzed-codegen --output example/authzed example/schema.zed && git diff --quiet example/authzed/`) before declaring any generator change done
