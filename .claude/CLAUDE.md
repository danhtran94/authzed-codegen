# <Service Name> — Agent Context

## Stack
- Go 1.26+, Huma, ent, franz-go, Atlas
- PostgreSQL 17, Apache Kafka
- AWS / GCP

## Architecture Boundaries (enforced by depguard)
cmd/server/         → wiring only, zero business logic
api/<module>.v1/    → HTTP contract, transforms, routes
internal/domain/    → pure functions, NO external imports
internal/module/    → use cases, defines interfaces only
internal/integrate/ → external adapters, implementations
repo/<module>.rp/   → data access, owns transformers
pkg/                → zero domain coupling

## Make Targets
make gen      → regenerate ent + domain models
make lint     → golangci-lint run (pinned version)
make test     → go test -v -timeout 120s ./...
make openapi  → export OpenAPI spec
make diff     → generate Atlas migration

## Where things live

The harness separates **skill definitions** (shared across every
project) from **work artifacts** (per-repo). These two live in
different places and never cross.

- **This repo** (walked from `.claude/harness.json`, module-scoped):
  - `jobs/<PREFIX>-NNN-*.md` — job docs produced by `/job`
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
