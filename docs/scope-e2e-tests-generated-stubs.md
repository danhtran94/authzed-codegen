# Scope: E2E tests for generated stubs

| Field    | Value                        |
|----------|------------------------------|
| Status   | Draft                        |
| Created  | 2026-05-05                   |
| Author   | Danh Tran                    |

---

## Problem

The `authzed-codegen` repo has **zero** `*_test.go` files. All 20 generated `.gen.go` stubs across `bookingsvc`, `menusvc`, and `extsvc` — which wrap a `authz.Engine` interface over a real SpiceDB backend — have **no end-to-end validation** that calling these stubs produces correct SpiceDB-level results.

The only regression bar is a round-trip fixture check (`go run ./cmd/authzed-codegen --output example/authzed example/schema.zed && git diff --quiet example/authzed/`), which verifies **byte-identical regeneration** but exercises **zero runtime behavior**. It confirms the template emits the same text, not that the generated methods correctly exercise the SpiceDB engine.

The generated stubs cover a complex surface area (21 relations, ~19 permission expressions):
- Union relations (`employee: extsvc/user | extsvc/group`)
- Wildcard relations (`folder: extsvc/user:*`)
- Self-referential permissions (`employee.manage = account + belongs_brand->manage`)
- Cross-definition arrows (`booking.write = owner->manage`, where `manage` is on `brand`)
- Intersection (`article.editor = author & parent->browse`)
- Exclusion (`article.author_only = author - parent->browse`)
- Sibling permission chains (`document.admin = view + edit`)

Without e2e tests, these patterns are unvalidated against a real SpiceDB backend.

## Success Criteria

1. A `pkg/authz/spicedb/spicedb_test.go` file exists with a `TestMain` function that starts a SpiceDB Docker container via `testcontainers-go`, loads the schema from `example/schema.zed` using the `authzed` CLI or gRPC API, and exposes a shared `Engine` via a `setupEngine(ctx)` package helper. Verifiable: `grep "TestMain" pkg/authz/spicedb/spicedb_test.go` shows the function; `go test ./pkg/authz/spicedb/...` passes (or is skipped when Docker is unavailable).

2. A `example/authzed/bookingsvc/bookingsvc_test.go` file exists with tests covering all 5 generated files (`booking`, `brand`, `customer`, `employee`, `user`): primitive boilerplate (`Stringer`, `Stringers`, `ToList`), simple permission checks (`brand.manage`), self-referential permissions (`employee.manage`), wildcard relations (`employee.viewer`), and cross-definition arrows (`booking.write = creator->manage + owner->manage`). Verifiable: `grep -c "^func Test" example/authzed/bookingsvc/bookingsvc_test.go` shows at least 15 test functions.

3. A `example/authzed/menusvc/menusvc_test.go` file exists with tests covering all 9 generated files: primitives (`customer`, `product`), simple single-rel patterns (`table`, `pricelist`, `setting`), self-referential (`user.manage`), union relations (`order.creator`), multi-permission resources (`company.manage`, `company.create_booking`, `company.create_order`), and cross-package reference chains (`company.create_booking -> employee.manage -> account`). Verifiable: `grep -c "^func Test" example/authzed/menusvc/menusvc_test.go` shows at least 25 test functions.

4. A `example/authzed/extsvc/extsvc_test.go` file exists with tests covering all 6 generated files: primitives (`user`, `group`, `role`), union + wildcard relations (`folder.viewer` + `folder.guest`), arrow-only permissions (`document.view = parent->browse`), mixed identifier + arrow (`document.edit = owner + parent->browse`), intersection (`article.editor = author & parent->browse`), and exclusion (`article.author_only = author - parent->browse`). Verifiable: `grep -c "^func Test" example/authzed/extsvc/extsvc_test.go` shows at least 20 test functions.

5. `go test ./pkg/authz/spicedb/... ./example/authzed/bookingsvc/... ./example/authzed/menusvc/... ./example/authzed/extsvc/...` **all pass** against a running SpiceDB instance. Verifiable: after starting SpiceDB locally, `go test -v ./pkg/authz/spicedb/...` exits 0; same for all four test packages.

6. `go build ./...` and `go vet ./...` pass after adding test files. Verifiable: `go build ./...` exits 0; `go vet ./...` exits 0.

---

## Out of Scope

- **Tests for the codegen CLI itself** (`cmd/authzed-codegen/`). Reason: the codegen CLI is a build-time tool; its behavior is validated by the round-trip fixture and separate unit tests for the adapter/generator. This scope covers e2e tests for the *generated stubs* against a SpiceDB backend.
- **Tests for the adapter** (`internal/generator/adapter.go`). Reason: adapter logic (proto-to-DefinitionView) is unit-testable without a SpiceDB backend. Covered by a separate unit-test scope.
- **Tests for the generator** (`internal/generator/generator.go`). Reason: template execution and permission tree resolution are unit-testable without SpiceDB. Covered by a separate unit-test scope.
- **Tests for the SpiceDB engine implementation** (`pkg/authz/spicedb/crud.go`). Reason: the engine's RPC mappings are tested separately against SpiceDB's own test suite. This scope tests the *generated stubs*, not the engine.
- **Schema migration / teardown tests**. Reason: tests rely on non-colliding `t-` prefixed IDs and do not clean up test data. Schema migration is validated once at startup; per-test cleanup adds complexity without material risk.
- **Performance or load tests**. Reason: the e2e tests validate correctness against a single SpiceDB instance. Load testing is a separate concern for production CI.

---

## Risks

- **SpiceDB Docker container startup time slows the test suite.** Mitigation: a single SpiceDB instance is shared across all 4 test files via `TestMain`. Schema is loaded once. Per-test overhead is limited to creating relationships, checking permissions, and reading relations — no container restarts. If Docker is unavailable, `testcontainers-go`'s `Skip()` pattern ensures tests are skipped rather than failing.
- **Schema is large (20 resource types, 21 relations, ~19 permissions) and the full fixture takes >30s to exercise.** Mitigation: only the most complex patterns get dedicated test functions. Simple patterns (single relation + single permission) are verified in grouped tests. The 20 primitives (files with no relations/permissions) only need `Stringer`/`Stringers`/`ToList` checks which run in <1s total.
- **Test data from one test pollutes another, causing flaky failures.** Mitigation: all test IDs are prefixed with `t-` and are globally unique per test function. Tests do not clean up data but rely on name isolation. SpiceDB treats test data as regular relationships — no cross-test leakage since IDs never collide.
- **`testcontainers-go` adds a new direct dependency with platform-specific build requirements.** Mitivation: `testcontainers-go` is a well-maintained library (v0.33.0, 3k+ stars). It requires Docker to be installed, which is standard for a Go dev environment. If Docker is unavailable, tests are skipped via `t.Skip()`.
- **The `document.admin` permission (computed as `view + edit`) adds complexity that bloats test count.** Mitigation: `document.admin` tests are excluded from scope. Only `document.view` (arrow-only) and `document.edit` (mixed identifier + arrow) are tested, which covers the arrow and union patterns without needing a separate computed-perm test.

---

## Assumptions

- **A1 [EXTERNAL FACT]:** SpiceDB provides a Docker image (`ghcr.io/authzed/spicedb`) that can be started programmatically via `testcontainers-go`. Evidence: `docker pull ghcr.io/authzed/spicedb:latest` works; the official SpiceDB docs reference this image for development/testing.
- **A2 [EXTERNAL FACT]:** SpiceDB accepts schema loading via the `zed` CLI or gRPC API (`SchemaService.LoadSchema`) at startup. Evidence: `authzed/spicedb` source code in `cmd/spicedb` shows the `zed` CLI and `grpc` schema loading; the `authzed-go` client provides `WriteSchema` RPC.
- **A3 [VERIFIED]:** `github.com/stretchr/testify` v1.11.1 is an indirect dependency in `go.mod`. Evidence: `go mod graph | grep testify` shows it; it will be promoted to a direct dependency for test files.
- **A4 [VERIFIED]:** All 20 generated `.gen.go` files compile and the project builds (`go build ./...` passes). Evidence: the most recent commit (`e8c6aeb`) ran `go build ./... && go vet ./...` successfully; the round-trip diff is clean.
- **A5 [HYPOTHESIS]:** The `testcontainers-go` library supports Linux x86_64 and macOS Apple Silicon (arm64) Docker backends. Both are the developer's local platforms. Verification: check `testcontainers-go` docs and test matrix before implementation; if macOS arm64 is unsupported, use `t.Skip()` with a comment linking to the issue.

---

## History

| Date       | Change                           |
|------------|----------------------------------|
| 2026-05-05 | Initial scope note authored.     |
