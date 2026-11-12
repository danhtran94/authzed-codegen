# Scope: Bounded batch relationship delete

| Field   | Value      |
|---------|------------|
| Status  | Accepted   |
| Created | 2026-11-13 |
| Author  | Danh Tran  |

---

## Problem

`authz.Engine.DeleteRelationsMatching(ctx, f RelationFilter) error` (`pkg/authz/authz.go:229`) issues exactly one delete per call, always with `OptionalLimit: 0` — a single transactional, unlimited delete (`pkg/authz/spicedb/crud.go`, `DeleteRelationsMatching`: `// OptionalLimit omitted (0) -> one transactional, unlimited delete`). `ADR-005` (Consequences) and `SPEC-014` (Constraint C1) already name the resulting gap explicitly — "An object with millions of tuples hits the same single-transaction timeout" — and pre-commit the fix's shape if it's ever built: "an `opts` parameter on `DeleteRelationsMatching` (or a sibling method) — `OptionalLimit: 0` callers must keep working." Both defer it until "a concrete huge-object need surfaces" (`scope-relationship-cleanup-verbs.md`, Out of Scope).

SpiceDB's `DeleteRelationshipsRequest` has carried the mechanism this gap needs since before that deferral: `OptionalLimit` + `OptionalAllowPartialDeletions` bound a single call to at most N deletions and return `DeletionProgress: DELETION_PROGRESS_PARTIAL` instead of erroring (per A1). What's new as of SpiceDB v1.57.0 (this repo is on v1.57.1 via the PR #7 merge) is a *cursor*: `DeleteRelationshipsRequest.OptionalCursor` / `DeleteRelationshipsResponse.AfterResultCursor` let a caller resume a partial deletion from exactly where the last call stopped, instead of re-scanning (and re-filtering past) every already-deleted row on each loop iteration (`authzed-go@v1.11.0` `proto/authzed/api/v1/permission_service.pb.go:915-944`, per A2).

**That cursor is CockroachDB-only** (per A3). Verified against `spicedb@v1.57.1`'s datastore implementations: `internal/datastore/{postgres,mysql,spanner,memdb}/readwrite.go` each call `delOpts.ValidateCursoredDelete()` and then unconditionally return `datastore.NewCursoredDeleteNotSupportedErr(Engine)` if a cursor is supplied — only `internal/datastore/crdb/` has a real implementation (`cursoreddelete.go`, with its own dedicated test and benchmark files). This repo's own e2e harness (`pkg/authz/spicedbtest/setup.go:71`, `"serve", "datastore", "memory"`) runs MemDB, which is in that reject list (per A4) — cursor-resume cannot be exercised by this repo's existing test infrastructure without adding a new CockroachDB e2e fixture.

The non-cursored bounded path (`OptionalLimit` + `OptionalAllowPartialDeletions`, no cursor — re-scanning each loop iteration) *is* supported by every datastore including MemDB (per A3: `internal/datastore/memdb/readwrite.go:144-169` handles `DeleteLimit` once `IsCursoredDelete()` is false). That is the portable, testable mechanism this repo can actually verify end to end; the cursor is an optional accelerant available only to CockroachDB deployments, and this scope does not implement it (see Out of Scope).

Per `ADR-006`, the chosen shape widens `DeleteRelationsMatching`'s existing signature rather than adding a sibling method. `DeleteRelationsMatching` is not called only from hand-written code — `internal/templates/object.go.tmpl` calls it from all three generated cleanup verbs (`Purge<Rel>Relations`, `PurgeRelations`, `PurgeRelationsAsSubject`; template lines 137, 151, 166) and those three call sites multiply out to over 100 generated call sites across `example/authzed/**/*.gen.go` (per A8). The signature widening therefore reaches into `internal/templates/` even though the capability itself is at the `pkg/authz/` layer — every one of those call sites needs the new argument added, passing the zero-value `authz.DeleteOptions{}` to keep the generated `Purge*` verbs unbounded (per the Out of Scope item on not wiring `Purge*` to bounded deletion).

## Success Criteria

1. `authz.Engine.DeleteRelationsMatching` gains a bounded/partial relationship-delete capability — exact signature pinned by `ADR-006` (`DeleteRelationsMatching(ctx, f RelationFilter, opts DeleteOptions) (DeletionProgress, error)`). It accepts a limit via `DeleteOptions` and reports whether the deletion completed or is partial via `DeletionProgress`. Verifiable: `grep -n "DeletionProgress\|DeleteOptions" pkg/authz/authz.go` shows new exported identifiers not present today.

2. `DeleteRelationsMatching`'s widened signature is the *only* delete-by-filter method on `authz.Engine` — per `ADR-006`, no sibling method is added. `DeleteOptions{}` (the zero value) reproduces today's unbounded, single-transaction behavior exactly. Verifiable: `grep -c "DeleteRelationsMatching(ctx" pkg/authz/authz.go` returns `1`; a call site passing `authz.DeleteOptions{}` behaves identically to a pre-`ADR-006` call with no third argument (same wire request, same `OptionalLimit: 0`).

3. `*spicedb.Engine`'s implementation sets `OptionalLimit` from `opts.Limit` (non-zero) and `OptionalAllowPartialDeletions: opts.Limit > 0` on the wire `DeleteRelationshipsRequest`, and never sets `OptionalCursor`. Verifiable: `grep -n "OptionalCursor" pkg/authz/spicedb/crud.go` returns no matches; `grep -n "OptionalAllowPartialDeletions" pkg/authz/spicedb/crud.go` returns at least one match.

4. `DeleteRelationsMatching` reuses the existing `RelationFilter.IsEmpty()` / `ErrEmptyRelationFilter` guard rather than re-implementing it. Verifiable: `grep -n "IsEmpty\|ErrEmptyRelationFilter" pkg/authz/spicedb/crud.go` shows the guard still fires regardless of `opts.Limit`.

5. e2e test against this repo's existing MemDB testcontainer fixture proves bounded deletion actually bounds, without a SpiceDB cursor: write more tuples than a chosen limit, call `DeleteRelationsMatching` with `DeleteOptions{Limit: n}`, assert the returned `DeletionProgress.Deleted` equals the limit (not the full count) and `Complete` is false; looping the call (caller-driven, no cursor argument) eventually clears the remainder and reports `Complete: true`. Verifiable: `pkg/authz/spicedb/bounded_delete_test.go` exists and runs against the real testcontainer (not mocked).

6. `go build ./...` exits 0.

7. `go vet ./...` exits 0.

8. `internal/templates/object.go.tmpl`'s three `DeleteRelationsMatching` call sites (Purge<Rel>Relations, PurgeRelations, PurgeRelationsAsSubject) each pass `authz.DeleteOptions{}` as the new third argument, preserving their existing unbounded behavior. Round-trip then regenerates cleanly: `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed && git diff --quiet example/authzed/` exits 0 *after* that template edit and a regen commit — i.e. the only expected `example/authzed/` diff is the mechanical `, authz.DeleteOptions{}` addition at every existing `DeleteRelationsMatching` call site, with no other behavioral change. Verifiable: `grep -c "DeleteRelationsMatching(ctx" internal/templates/object.go.tmpl` returns `3`; `grep -c "DeleteOptions{}" internal/templates/object.go.tmpl` also returns `3`.

9. `README.md`'s "Relationship Cleanup" section gains a subsection documenting the new method, and states explicitly that cursor-resume (`OptionalCursor`) is a CockroachDB-only SpiceDB v1.57.0+ capability this repo does not implement, with the datastore evidence from the Problem section. Verifiable: `grep -i "bounded\|partial" README.md` matches inside that section, and the section contains the literal string `CockroachDB`.

10. `CHANGELOG.md` gains a `[2.0.0]` entry documenting the breaking signature change (per `ADR-006`), tagged MAJOR — the first MAJOR release in this project's history — and names the module's new `/v2` import-path suffix (per Go's semantic import versioning). Verifiable: `grep -n "2.0.0" CHANGELOG.md` matches, and that entry's body contains both `BREAKING` (or `breaking`) and `/v2`.

## Out of Scope

- **Cursor-resume (`OptionalCursor` / `AfterResultCursor`)**. Reason: CockroachDB-only upstream as of SpiceDB v1.57.x (per A3); this repo's e2e harness is MemDB-only (per A4) and could not verify it. Revisit if this repo ever adds a CockroachDB fixture, or a user reports the re-scan cost of the non-cursored loop is a real problem on CRDB.
- **Wiring the generated `Purge<Rel>Relations` / `PurgeRelations` / `PurgeRelationsAsSubject` methods to the new bounded capability.** Reason: those stay on the existing unbounded `OptionalLimit: 0` path; offering a bounded codegen variant is a separate future decision, not a requirement of adding the Engine-level primitive — verbs-not-workflows, ship the primitive and let the caller decide when to use it.
- **An automatic loop-until-complete helper** in `pkg/authz` that calls the new method repeatedly until `DeletionProgress` reports complete. Reason: the scope ships the single-call bounded primitive (mirrors SC1-SC4); looping is caller-side composition, same stance as the original relationship-cleanup-verbs scope's "verbs, not a workflow."
- **A CockroachDB (or Postgres/MySQL/Spanner) e2e test fixture.** Reason: this repo's e2e harness stays MemDB-only (per A4); adding a new backend/testcontainer is a separate, larger infra scope, not a requirement for testing the non-cursored bounded path (which MemDB already supports).
- **Changing `DeleteRelations` or the generated `Delete<Rel>Relations`.** Reason: those are targeted per-ID deletes, not filter-based, and never hit the single-transaction-timeout problem this scope addresses.
- **Optimistic-concurrency preconditions (`OptionalPreconditions`) on the new method.** Reason: mirrors the original scope's deferral on the existing `DeleteRelationsMatching` — stays unconditional.
- **Exposing `RelationshipFilter` fields not already in `RelationFilter`** (e.g. `OptionalResourceIdPrefix`). Reason: a separate filter-expressiveness question, unrelated to bounding an existing filter's delete.

## Risks

- **The exact method name/signature is deferred to a follow-up ADR (per SC1), so implementation cannot start from this scope note alone.** Mitigation: author the follow-up ADR before any job doc for this scope — the same two-step this codebase already followed for the original cleanup-verbs scope (`scope-relationship-cleanup-verbs.md` → `ADR-005`, four months earlier in this repo's own history), so the authoring overhead and process are already precedented, not novel.

- **A reader could assume the new bounded method gives full resumable-cursor behavior (no re-scan cost) on any datastore, including the Postgres/MemDB most deployments run.** Mitigation: SC9 requires the README subsection to state explicitly, by name, that cursor-resume is a CockroachDB-only SpiceDB capability this method does not implement — not a vague "may be slower" caveat.

- **Bounded deletion's "which N tuples get deleted first when over the limit" ordering is not guaranteed identical across datastores** — a test asserting specific deleted IDs would pass against this repo's MemDB fixture while asserting a guarantee that doesn't hold against Postgres/CockroachDB in production. Mitigation: SC5 already scopes the e2e assertion to deleted-*count* and the partial/complete progress signal, never to which specific IDs were deleted first — keep it that way during implementation; do not strengthen the assertion to specific IDs.

- **A caller could reach for the new bounded method by default, even for ordinary objects that fit comfortably in one transaction**, paying unnecessary loop overhead for no benefit. Mitigation: the new method's Go doc comment must state, in the same style as the existing `DeleteRelationsMatching` doc comment (`pkg/authz/authz.go:224-228`), that `DeleteRelationsMatching` (`OptionalLimit: 0`) remains the default for normal-fan-out objects and the bounded method is only for objects that might exceed the single-transaction timeout.

- **Changing `authz.Engine` without breaking the only implementer.** Mitigation: `pkg/authz/spicedb/crud.go`'s existing `var _ authz.Engine = &Engine{}` compile-time assertion already fails the build the moment the interface and the implementation diverge — no new tooling needed, SC2/SC6 just have to hold.

- **`DeleteRelationsMatching`'s signature change is easy to implement in `pkg/authz/` while missing that `internal/templates/object.go.tmpl` calls the same method from three generated verbs** (per A8), leaving the template uncompilable against the new signature until it's updated too. Mitigation: SC8 names the exact template call-site count (3) and requires the regenerated fixture's only diff to be the mechanical `DeleteOptions{}` addition — a job doc for this scope must include the template edit as its own task, not treat `pkg/authz/` and the template as independent changes.

## Assumptions

- **A1 [VERIFIED]:** `DeleteRelationshipsRequest.OptionalLimit` (non-zero) + `OptionalAllowPartialDeletions` (true) bound a single `DeleteRelationships` call to at most N deletions and return `DeletionProgress: DELETION_PROGRESS_PARTIAL` + `RelationshipsDeletedCount` instead of erroring; this mechanism already existed at SpiceDB v1.52.0 and was the thing `scope-relationship-cleanup-verbs.md` and `ADR-005`/`SPEC-014` named and deferred. Evidence: those three docs' own citations of `spicedb@v1.52.0 internal/services/v1/relationships.go` and `spicedb` issue #1224.
- **A2 [VERIFIED]:** `DeleteRelationshipsRequest.OptionalCursor` and `DeleteRelationshipsResponse.AfterResultCursor` are new fields (proto field numbers 6 and 4 respectively) not present in the authzed-go version (`v1.10.0`) this repo was on before PR #7. Evidence: `authzed-go@v1.11.0 proto/authzed/api/v1/permission_service.pb.go:915-944,1018-1033`, field doc comments quoted verbatim in Problem above; SpiceDB v1.57.0 release notes ("The `DeleteRelationships` API now supports resumable, cursored batch deletion").
- **A3 [VERIFIED]:** Of SpiceDB's datastore backends, only `internal/datastore/crdb/` implements cursored delete; `postgres`, `mysql`, `spanner`, and `memdb` all call `ValidateCursoredDelete` and then unconditionally `return ..., datastore.NewCursoredDeleteNotSupportedErr(Engine)` when `IsCursoredDelete()` is true, while the non-cursored `OptionalLimit`-bounded path works on all of them (confirmed by direct read of `memdb/readwrite.go:144-169`, and the identical pattern in `postgres/readwrite.go:439-450`). Evidence: grep of `spicedb@v1.57.1`'s `internal/datastore/{postgres,mysql,spanner,memdb,crdb}/readwrite.go` for `CursoredDelete`, performed during this scope session; `crdb/cursoreddelete.go` + `crdb/cursoreddelete_test.go` existing only in that one backend; SpiceDB v1.57.0 release notes ("Other engines fall back to a slower non-cursored loop").
- **A4 [VERIFIED]:** This repo's e2e test harness exclusively exercises MemDB (`pkg/authz/spicedbtest/setup.go:71`: `Cmd: []string{"serve", "datastore", "memory", ...}`) — there is no Postgres/CockroachDB/MySQL/Spanner fixture anywhere in the repo today. Evidence: direct read of `pkg/authz/spicedbtest/setup.go`; `grep -rn "datastore" pkg/authz/spicedbtest/` shows no other backend.
- **A8 [VERIFIED]:** `DeleteRelationsMatching` is called from three places in `internal/templates/object.go.tmpl` (lines 137, 151, 166 — the `Purge<Rel>Relations`, `PurgeRelations`, and `PurgeRelationsAsSubject` generated methods), and those three call sites expand to over 100 generated call sites across `example/authzed/**/*.gen.go`. Evidence: `grep -n "DeleteRelationsMatching" internal/templates/object.go.tmpl` (3 matches) and `grep -rn "DeleteRelationsMatching" --include="*.go" example/authzed/` (counted during this scope session, excluding `_test.go` files).

## History

| Ticket    | Title                             | Created    | Status   |
|-----------|-----------------------------------|------------|----------|
| (initial) | Bounded batch relationship delete | 2026-11-13 | Accepted |
