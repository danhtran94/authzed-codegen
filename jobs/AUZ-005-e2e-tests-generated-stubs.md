# AUZ-005: e2e tests for generated stubs

| Field        | Value                                               |
|------------|-----------------------------------------------------|
| Status       | Done                                                |
| Created      | 2026-05-05                                          |
| Assignee     | TBD                                                 |
| Source       | docs/scope-e2e-tests-generated-stubs.md             |
| Depends on    | —                                                   |
| Implements    | docs/scope-e2e-tests-generated-stubs.md             |

---

## Goal

Add end-to-end tests that exercise all 20 generated `.gen.go` stubs across `bookingsvc`, `menusvc`, and `extsvc` against a real SpiceDB Docker backend. After this job:

1. `pkg/authz/spicedb/spicedb_test.go` exists with a `TestMain` that starts a SpiceDB container via `testcontainers-go`, loads the `example/schema.zed` schema, and exposes a shared `Engine` through a `setupEngine(ctx)` helper.
2. `example/authzed/bookingsvc/bookingsvc_test.go` has 15+ `func Test*` functions covering `booking`, `brand`, `customer`, `employee`, `user` — union relations, self-referential permissions, wildcard relations, cross-definition arrows.
3. `example/authzed/menusvc/menusvc_test.go` has 25+ `func Test*` functions covering `company`, `order`, `pricelist`, `table`, `setting`, `user`, `customer`, `product` — primitives, self-referential, union relations, multi-permission resources, cross-package chains.
4. `example/authzed/extsvc/extsvc_test.go` has 20+ `func Test*` functions covering `user`, `group`, `role`, `folder`, `document`, `article` — intersection, exclusion, wildcard, union relations, arrow-only perms, mixed identifier+arrow.
5. `go test ./pkg/authz/spicedb/... ./example/authzed/bookingsvc/... ./example/authzed/menusvc/... ./example/authzed/extsvc/...` all pass against a running SpiceDB instance.
6. `go build ./...` and `go vet ./...` pass.

---

## Tasks

### Task 1: Create `pkg/authz/spicedb/spicedb_test.go` — TestMain + shared helper

- **File:** `pkg/authz/spicedb/spicedb_test.go`
- **Action:** Create a new test file with:
   - `TestMain(m *testing.M)` that starts a SpiceDB Docker container using `testcontainers-go`
   - Loads the schema from `example/schema.zed` using the `authzed-go` `WriteSchema` RPC
   - Creates an `Engine` via `spicedb.NewEngine(client, 3*time.Second)`
   - Sets it as the `authz.DefaultEngine` via `authz.SetDefaultEngine()`
   - Package-level `setupEngine(ctx)` helper function that returns a `context.Context` with the `DefaultEngine` set
   - A `setupTestEngine()` init function that creates a unique namespace prefix (e.g., `"t-"`) seed or package-level variable for generating unique test IDs
- **Workstream:** infrastructure
- **Verification:** `go test ./pkg/authz/spicedb/... -run "^$" -v` compiles; against a running SpiceDB instance, `go test ./pkg/authz/spicedb/...` passes
- **Dependency:** none (standalone)

### Task 2: Create `example/authzed/bookingsvc/bookingsvc_test.go`

- **File:** `example/authzed/bookingsvc/bookingsvc_test.go`
- **Action:** Create a test file with tests covering all 5 generated files:
   - **`customer` / `user` (primitives):** `TestCustomer_Boilerplate` — `CustomerStringer`, `CustomerStringers`, `Customer.ToList()` identity
   - **`brand` (simple relations):** `TestBrand_CheckManage` — Create `brand:1` with `admin=user:u1` + `manager=employee:e1` via `CreateAdminRelations`/`CreateManagerRelations`; check `u1` and `e1` have `manage` via `CheckManage`
   - **`employee` (self-ref + wildcard):** `TestEmployee_CheckManage` — Create `employee:1` with `account=user:u1` + `belongs_brand=brand:brand1`; check `u1` has `manage` (direct), `employee:1` has `manage` (self-ref: `belongs_brand->manage`). `TestEmployee_CheckView` — Create `employee:1` with `viewer=user:*` wildcard; check `user:anyone` has `view` via `CheckView`. `TestEmployee_ReadViewerUserWildcard` — `ReadViewerUserWildcard` returns `true`
   - **`booking` (cross-def arrow):** `TestBooking_CheckWrite` — Create `booking:1` with `owner=Employee:e1` + `creator=Customer:c1`; check `e1` has `write` (via `owner->manage` on brand), `c1` has `write` (via `creator->manage` on brand). `TestBooking_CheckChangeOwner` — Check `c1` has `change_owner` (`creator + creator->manage`)
- **Workstream:** test files
- **Verification:** `go test ./example/authzed/bookingsvc/... -v` shows 15+ passing tests
- **Dependency:** depends on Task 1 (imports `setupEngine` from `pkg/authz/spicedb`)

### Task 3: Create `example/authzed/menusvc/menusvc_test.go`

- **File:** `example/authzed/menusvc/menusvc_test.go`
- **Action:** Create a test file with tests covering all 9 generated files:
   - **`customer` / `product` (primitives):** `TestCustomer_Boilerplate`, `TestProduct_Boilerplate` — Stringer/Stringers/ToList
   - **`table` / `pricelist` / `setting` (simple single-rel, single-perm):** `TestTable_CheckWrite` — Create `table:1` with `owner=Company:comp1`; check `user:u1` has `write` via `owner->manage` on company. Same pattern for `pricelist` and `setting`
   - **`user` (self-ref):** `TestUser_CheckManage` — Create `user:1` with `belongs_company=comp1`; check `user:1` has `manage` via `belongs_company->manage` on company
   - **`company` (3 perms, 3 relations):** `TestCompany_CheckManage` — Create `company:1` with `admin=user:u1` + `manager=user:u2` + `employee=user:u3`; check `u1` has `manage`, `u2` has `manage`. `TestCompany_CheckCreateBooking` — Check `u3` has `create_booking` (`manage + employee`). `TestCompany_LookupManageUserSubjects` — `LookupManageUserSubjects` on `company:1` returns `[u1, u2]`
   - **`order` (union relation):** `TestOrder_CheckWrite` — Create `order:1` with `creator=user:u1 + customer:c1`, `belongs_company=comp1`; check `u1` and `c1` have `write`. `TestOrder_ReadCreator` — `ReadCreatorUserRelations` returns `[u1]`, `ReadCreatorCustomerRelations` returns `[c1]`
- **Workstream:** test files
- **Verification:** `go test ./example/authzed/menusvc/... -v` shows 25+ passing tests
- **Dependency:** depends on Task 1

### Task 4: Create `example/authzed/extsvc/extsvc_test.go`

- **File:** `example/authzed/extsvc/extsvc_test.go`
- **Action:** Create a test file with tests covering all 6 generated files:
   - **`user` / `group` / `role` (primitives):** `TestUser_Boilerplate`, `TestGroup_Boilerplate`, `TestRole_Boilerplate`
   - **`folder` (union + wildcard):** `TestFolder_CheckBrowse_User` — Create `folder:1` with `viewer=user:uv1`; check `uv1` has `browse`. `TestFolder_CheckBrowse_GuestWildcard` — Create `folder:1` with `guest=user:gv1` (wildcard); check `user:anyone` has `browse`. `TestFolder_ReadGuestUserWildcard` — `ReadGuestUserWildcard` returns `true`
   - **`document` (arrow perms):** `TestDocument_CheckView` — Create `document:1` with `parent=folder:vf1`, `owner=user:vo1 + group:g1`; create `folder:vf1` with `viewer=user:vf1`; check `vo1` has `view` via `parent->browse`. `TestDocument_CheckEdit` — Check `vo1` has `edit` (`owner + parent->browse`); check `user:nobody` does NOT have `edit` → `ErrPermissionDenied`
   - **`article` (intersection + exclusion — critical):** `TestArticle_CheckEditor_AuthorOnly` — Create `article:1` with `author=user:a1` (no folder parent); check `a1` has `editor` → `ErrPermissionDenied` (has author but NOT `parent->browse`). `TestArticle_CheckEditor_AuthorPlusFolderGuest` — Create `article:1` with `author=user:a2`, `parent=folder:f1`; create `folder:f1` with `guest=user:a2`; check `a2` has `editor` → `true` (intersection: author AND parent->browse). `TestArticle_CheckAuthorOnly_NotEditor` — Create `article:1` with `author=user:a3` (no folder); check `a3` has `author_only` → `true`. `TestArticle_CheckAuthorOnly_Excluded` — Create `article:1` with `author=user:a4`, `parent=folder:f2`; create `folder:f2` with `guest=user:a4`; check `a4` has `author_only` → `ErrPermissionDenied` (author but ALSO `parent->browse`, so excluded)
- **Workstream:** test files
- **Verification:** `go test ./example/authzed/extsvc/... -v` shows 20+ passing tests
- **Dependency:** depends on Task 1

### Task 5: Update `go.mod` + build verification

- **File:** `go.mod`, `go.sum`
- **Action:**
   - Add `github.com/testcontainers/testcontainers-go` v0.33.0 as a direct dependency
   - Run `go mod tidy`
   - Run `go build ./...` and `go vet ./...` — verify zero errors
   - Run `go test ./pkg/authz/spicedb/... ./example/authzed/bookingsvc/... ./example/authzed/menusvc/... ./example/authzed/extsvc/... -v` against a locally running SpiceDB instance — verify all pass
- **Workstream:** verification
- **Verification:** `go build ./...` exits 0; `go vet ./...` exits 0; `go test ...` exits 0
- **Dependency:** depends on Tasks 1–4

---

## Implementation Order

1. **Task 1 (infrastructure)** ← standalone; no dependencies
2. **Task 2 (bookingsvc tests)** ← depends on Task 1
3. **Task 3 (menusvc tests)** ← depends on Task 1
4. **Task 4 (extsvc tests)** ← depends on Task 1
5. **Task 5 (build + verification)** ← depends on Tasks 2–4; all test files must exist before running `go test`

Tasks 2–4 can run in parallel once Task 1 is complete (they all depend only on Task 1).

## What Stays Unchanged

- `example/schema.zed` — the schema file is loaded as-is
- `example/authzed/**/*.gen.go` — generated stubs are not modified
- `pkg/authz/authz.go` — the Engine interface is not modified
- `pkg/authz/spicedb/crud.go` — the engine implementation is not modified
- `internal/generator/adapter.go` — no codegen changes
- `internal/generator/generator.go` — no codegen changes
- `internal/templates/object.go.tmpl` — template unchanged

---

## Discoveries & Decisions During Implementation

### Shared `setupEngine` across packages does not work in Go's test model

The original plan put `TestMain` in `pkg/authz/spicedb/spicedb_test.go` and
expected sibling packages to import `setupEngine` from there. Go compiles
each test package as its own binary; `_test.go` symbols never export to
other packages, and `authz.SetDefaultEngine` only fires inside the binary
whose `TestMain` ran. Decision: extract a non-`_test.go` helper at
`pkg/authz/spicedbtest/setup.go` exposing `Start(ctx, schemaSDL) (*Sandbox, error)`.
Each test package's `TestMain` calls `Start` and binds the engine in its
own process. Container names are auto-generated so parallel `go test ./...`
invocations do not collide on a fixed name.

### `*Stringer` / `*Stringers` boilerplate testing requires a `String() string` shim

The generated `*Stringer(authz.StringConvertable)` and
`*Stringers(...authz.StringConvertable)` helpers are designed to ingest
upstream ID types (e.g. `uuid.UUID`) that implement `String() string`.
Neither the generated `~string` types (e.g. `Customer`, `User`) nor
`authz.ID` implement that interface. Decision: each test file declares a
local `type strID string` with a `String() string` method, used only for
exercising the boilerplate helpers. This matches the production caller
shape without coupling the test to `uuid` or any external dep.

### Schema constrains several test variants we drafted

Three tests in the initial draft assumed permission semantics the schema
does not provide:

- `bookingsvc/employee.viewer: bookingsvc/user:*` is wildcard-only.
  Direct-user-in-viewer tests fail at write time. Replaced with a
  `view via manage` test that exercises `permission view = manage + viewer`
  through the manage chain.
- `bookingsvc/employee.manage = account + belongs_brand->manage` requires
  the employee to be a brand `manager` (not just `admin`) for the
  self-ref leg. Added `brand.CreateManagerRelations(employee)` to the
  test setup.
- `extsvc/folder.browse = viewer` only — the `guest` relation is
  wildcard-only data and feeds no permission. Article tests that
  expected `parent->browse` via folder.guest were rewritten to use
  folder.viewer instead. The wildcard-on-guest paths are still exercised
  via `ReadGuestUserWildcard` (read-side), which is the one production
  surface that touches the wildcard data.

### `wait.ForLog` text varies by SpiceDB version

The first iteration waited for log line `"serving"`, which matches several
non-ready lines emitted before the gRPC port accepts traffic. Switched to
`"grpc server started serving"` plus `wait.ForListeningPort("50051/tcp")`
under `wait.ForAll` to gate startup on both signals.

### `*.test` artifacts were leaking into the working tree

A 29 MB `spicedb.test` Mach-O binary (from a prior `go test -c` run) was
sitting in the repo root. Added `*.test` to `.gitignore` and deleted the
artifact.

### Test counts (post-implementation)

| Package    | Required | Actual |
|------------|----------|--------|
| bookingsvc | 15+      | 20     |
| menusvc    | 25+      | 27     |
| extsvc     | 20+      | 22     |

`pkg/authz/spicedb/spicedb_test.go` carries only `TestMain` — it boots
SpiceDB to verify the harness wiring; the e2e tests themselves live in
the three `example/authzed/*` packages.

All four packages pass against `ghcr.io/authzed/spicedb:latest`. The
codegen round-trip (`go run ./cmd/authzed-codegen … && git diff --quiet`)
remains clean.
