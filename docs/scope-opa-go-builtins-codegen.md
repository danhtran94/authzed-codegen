# Scope: OPA Go builtins codegen

| Field   | Value      |
|---------|------------|
| Status  | Accepted   |
| Created | 2026-05-10 |
| Author  | Danh Tran  |

---

## Problem

The codegen project today emits typed Go bindings (`Check<Perm>`, `Lookup<Perm>Resources`, `Create<Rel>Relations`) but no integration with OPA's Rego runtime. Users who want to invoke generated Check / Lookup methods from Rego policies hand-roll OPA custom-builtin registrations — ~13 lines of mechanical wrapping per method. ADR-004 commits Pattern 4 (OPA Go builtins) as the first Tier 2 codegen extension under RFC-001's R2 demand qualification (per A1).

The boilerplate the codegen replaces is concrete. For each generated Check method like `extsvc.CheckFolderView(ctx, User{ID}, Folder{ID}) (bool, error)`, the OPA registration is a stanza that:

1. Declares the function name and signature — uniform 3-arg shape: `extsvc.check_folder_view(subject_id string, resource_id string, caveat_context object) → bool`.
2. Binds it to a closure that converts the Rego caveat-context map to `*structpb.Struct` via `structpb.NewStruct(m)`, then calls the typed Check method (with-caveat or no-caveat depending on whether the map is empty).
3. Maps the typed return (`bool`) and any error to OPA's `ast.Term` shape (per A5).

Lookup methods follow the same shape: 2-arg `lookup_<resource>_<perm>_resources(subject_id string, caveat_context object) → []string`. Callers pass an empty `{}` for `caveat_context` when no caveat applies.

Across the three example services (`bookingsvc`, `menusvc`, `extsvc`) the existing codegen produces ~30 Check methods and ~30 Lookup methods. Each becomes one OPA custom builtin (uniform 3-arg / 2-arg shape regardless of caveat applicability). The codegen mechanically emits all ~60 stanzas as one `opa.gen.go` per package alongside the existing `<entity>.gen.go` files (per A3).

This scope bounds the codegen feature, the CLI flag that opts users in, the round-trip fixture coverage, and the e2e test that exercises the generated bindings against a live SpiceDB. README documentation is tracked separately per RFC-001's documentation work item.

Concrete artifacts that change:

- `internal/generator/` — new template emission path for OPA bindings (per A1)
- `internal/templates/` — new `.tmpl` file for `opa.gen.go`; embedded via existing `//go:embed` pattern (per A2)
- `cmd/authzed-codegen/` — new `--emit-opa` CLI flag
- `example/authzed/{bookingsvc,menusvc,extsvc}/` — new `opa.gen.go` files committed alongside existing `.gen.go` outputs
- `go.mod` — adds `github.com/open-policy-agent/opa` to the dependency graph
- New e2e test exercising a sample Rego policy via `rego.New` against a live SpiceDB testcontainer (per A4)

## Success Criteria

1. With `--emit-opa` flag, codegen produces an `opa.gen.go` file per package alongside existing `<entity>.gen.go` files. Verifiable: `go run ./cmd/authzed-codegen --output /tmp/out --emit-opa example/schema.zed && ls /tmp/out/extsvc/opa.gen.go /tmp/out/bookingsvc/opa.gen.go /tmp/out/menusvc/opa.gen.go` — all three files exist and `$? == 0`.

2. Each generated `opa.gen.go` declares exactly one exported function: `func RegisterSpiceDBBuiltins(r *rego.Rego, engine authz.Engine, ctx context.Context)`. Verifiable: `grep -c "^func RegisterSpiceDBBuiltins" example/authzed/extsvc/opa.gen.go` returns exactly `1`. Same for `bookingsvc/opa.gen.go` and `menusvc/opa.gen.go`.

3. `RegisterSpiceDBBuiltins` registers one OPA custom builtin per Check method, named `<package>.check_<resource_lowercase>_<perm_lowercase>` taking three arguments — `(subject_id string, resource_id string, caveat_context object)` — returning bool. Caveat context is always required at the call site; pass `{}` when no caveat applies. Verifiable: for `extsvc/folder.gen.go`'s `CheckFolderView`, `grep "check_folder_view" example/authzed/extsvc/opa.gen.go` matches at least once. The total count of `rego.Function3(` calls in `example/authzed/extsvc/opa.gen.go` equals the count of `func.*Check.*Inputs.*bool` matches across `example/authzed/extsvc/*.gen.go` (excluding `opa.gen.go` itself).

4. `RegisterSpiceDBBuiltins` registers one OPA custom builtin per Lookup method, named `<package>.lookup_<resource_lowercase>_<perm_lowercase>_resources` taking two arguments — `(subject_id string, caveat_context object)` — returning a list of strings sourced from `LookupResult.Definite` only. Verifiable: for `extsvc/folder.gen.go`'s `LookupFolderViewResources`, `grep "lookup_folder_view_resources" example/authzed/extsvc/opa.gen.go` matches; the binding's body in `opa.gen.go` contains the literal substring `result.Definite` and does not reference `result.Conditional`. The total count of `rego.Function2(` calls equals the count of Lookup methods in the same package.

5. Generated `opa.gen.go` compiles cleanly under the repo build. Verifiable: `go build ./example/authzed/...` exits 0.

6. Generated bindings call the existing `authz.Engine` methods directly through the typed interface — no new gRPC connection, no new auth wiring. Verifiable: `grep "engine\\.Check\\|engine\\.Lookup" example/authzed/extsvc/opa.gen.go` matches at least once; `grep -c "authzed\\.NewClient\\|grpc\\.Dial" example/authzed/extsvc/opa.gen.go` returns `0`.

7. Round-trip regression: `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed && git diff --quiet example/authzed/` exits 0 (the committed files regenerate byte-identically).

8. Without `--emit-opa`, codegen output is unchanged from current behavior. Verifiable: `go run ./cmd/authzed-codegen --output /tmp/out example/schema.zed` (no flag) produces the same files as today's commit; `diff -r /tmp/out example/authzed/` shows differences only in the absence of `opa.gen.go` files.

9. New e2e test in `example/authzed/extsvc/extsvc_opa_test.go` starts a SpiceDB testcontainer, creates a `rego.New` instance, calls `extsvc.RegisterSpiceDBBuiltins(r, engine, ctx)`, evaluates a Rego policy that invokes both the no-caveat path (`extsvc.check_folder_view(uid, rid, {})`) and the with-caveat path (passing a populated `caveat_context` map exercising at least one caveat parameter type from `extsvc/schema.zed`). Asserts both results equal the corresponding direct `engine.CheckFolderView` / `engine.CheckFolderViewWithCaveat` call. Verifiable: file exists; `go test ./example/authzed/extsvc/...` exits 0 with Docker available; skips cleanly via `spicedbtest.ErrDockerUnavailable` when Docker absent.

10. `go vet ./...` exits 0 across the repository after generation.

## Out of Scope

- **`Create<Rel>Relations` methods.** Reason: write side effects don't fit Rego's pure-function evaluation model; writing through Rego is an anti-pattern.
- **Variant-named `_with_context` separate builtins** (Shape A from the design exploration). Reason: rejected in favor of uniform 3-arg / 2-arg signatures (Shape B); always-required `caveat_context` simplifies the codegen template at the cost of an extra `{}` argument at no-caveat call sites.
- **Multi-arity overload under a single name** (Shape C from the design exploration). Reason: OPA's `rego.Function*` public API binds one arity per registration; multi-arity would require either undocumented duplicate-name registration or a sibling `.rego` helper file. Out of scope; if call-site ergonomics surface as a real cost, the helper-Rego mitigation is the trigger.
- **Build-tag opt-in for the generated package** (e.g. `//go:build opa`). Reason: `--emit-opa` flag is the opt-in mechanism for the codegen consumer; build tags inside generated `.go` files would require the generator to maintain two compile paths. Deferred to a follow-up if dependency-closure pain surfaces from real users.
- **README documentation** of the new feature with worked Rego example. Reason: tracked as a separate scope per RFC-001's documentation work item, sequenced after this codegen ships.
- **`runtime.NewRuntime` HTTP server scaffolding** in generated code. Reason: caller wires this if they want polyglot HTTP access. The codegen emits builtins, not server initialization.
- **Custom OPA decision-log / bundle-distribution wiring** in generated code. Reason: caller's domain — codegen does not configure OPA infrastructure beyond the builtin registrations.
- **Wildcard-aware Check methods** in builtins (`Read<X><Y>Wildcards`). Reason: wildcards are a separate API path per ADR-002 / ADR-003; binding them through Rego requires a different signature. Follow-up if needed.
- **Conditional Lookup result surfacing in Rego.** Reason: Lookup builtins return `Definite` only; surfacing `Conditional` would require returning a typed struct and a Rego shape for `LookupConditionalEntry` — distinct binding shape outside this scope.
- **CEL bindings (Pattern 2)** and **Rego HTTP helpers (Pattern 3)** from RFC-001. Reason: ADR-004 explicitly rejected Option D (build all three concurrently); Patterns 2 and 3 stay in the documented-hand-roll tier until separate R2 qualification.

## Risks

- **OPA's transitive dependency closure adds substantial weight to the repo's `go.mod`** — decision-log plugins, bundle plugins, status plugins, and the HTTP server bring in roughly 100 indirect packages. Mitigation: pin `github.com/open-policy-agent/opa` at a specific version in `go.mod` and document the expected version range in the SPEC. The example `opa.gen.go` files commit OPA into the repo's dep graph; consumers control their own graph via the `--emit-opa` flag (off by default). If a user reports build-time or binary-size pain, the build-tag mitigation (Out of Scope item 3) is the trigger to ship.

- **Round-trip determinism for `opa.gen.go` files breaks under OPA library upgrades** — `opa/rego` API surface or CEL semantics shifts could change emit shape across versions. Mitigation: use only stable public API from `github.com/open-policy-agent/opa/rego` (`RegisterBuiltin1`, `RegisterBuiltin2`, `types.NewFunction`, `types.S`, `types.B`, `types.A`, `ast.NewTerm`). Avoid any `internal/` package import. Pin OPA in `go.mod` so version drift surfaces as a dependency upgrade PR, not a silent codegen diff.

- **Context propagation pattern is not picked by the codegen** — `RegisterSpiceDBBuiltins(r, engine, ctx)` captures `ctx` by closure, forcing the caller to either recreate the registration per request OR commit to a long-lived `ctx` with cancellation. Mitigation: SC9's e2e test exercises one specific pattern (per-request `rego.New` + `RegisterSpiceDBBuiltins`); document both patterns in the SPEC's Sequence section. Don't pick the wrong default — leave the choice to the caller.

- **Lookup builtins return only `Definite`, dropping `Conditional` entries silently** — a Rego policy calling `lookup_<x>_resources(subj, ctx)` against a schema with caveat-aware Lookup paths receives a partial list even when `ctx` provides caveat values, because `LookupResources` may still return `Conditional` entries when context is incomplete. Mitigation: emit a Go doc comment on each Lookup builtin in `opa.gen.go` stating "returns Definite results only; Conditional entries are not surfaced to Rego". Out of Scope item 8 documents the limitation; the follow-up scope (if demand arises) adds a separate `lookup_<x>_resources_with_conditional` shape.

- **Rego→structpb numeric coercion across the caveat-context boundary.** Rego numbers are JSON numbers (float64 in Go); SpiceDB caveats expect `int`, `uint`, `double`, `bytes`, `duration`, `timestamp`, `ipaddress` per AUZ-018. A Rego policy passing `{"current_hour": 14}` lands as `float64(14)` in Go; SpiceDB's CEL caveat expects `int`. Mitigation: rely on `structpb.NewStruct(m)` which preserves Rego's value as-is; SpiceDB's CEL evaluator coerces numeric types at evaluation time. SC9 e2e exercises at least one caveat parameter type from `extsvc` (which has `duration`, `timestamp`, and `ipaddress` caveats per AUZ-018) to verify the coercion works end-to-end. The SPEC documents the type mapping table for callers writing Rego policies against caveat-aware bindings.

- **Method-name collision across packages** if two generated packages export `check_doc_view` (same `Resource.Permission` pair in different `definition` blocks). Mitigation: use the Rego package namespace as the prefix (`extsvc.check_doc_view` vs `bookingsvc.check_doc_view`); SC3's naming rule encodes this. Collisions become impossible at the Rego import level — `import data.spicedb.extsvc` vs `import data.spicedb.bookingsvc`.

## Assumptions

- **A1 [VERIFIED]:** ADR-004 commits the codegen project to Pattern 4 (OPA Go builtins) as the first Tier 2 codegen extension. Evidence: `docs/ADR-004-opa-go-builtins.md`, Status: Accepted, dated 2026-05-10. The R2 demand qualification under RFC-001 is the project owner's explicit selection.

- **A2 [VERIFIED]:** The codegen's existing `internal/templates/` structure supports adding a new template file via the `//go:embed` pattern. Evidence: `internal/templates/embed.go` declares each embedded template explicitly (e.g. `//go:embed object.go.tmpl` + `var ObjectTemplate []byte`); adding `//go:embed opa.go.tmpl` + `var OPATemplate []byte` follows the existing pattern unchanged.

- **A3 [VERIFIED]:** Each generated `<entity>.gen.go` exposes Check / Lookup methods on a typed receiver — the codegen pattern is consistent across `bookingsvc`, `menusvc`, `extsvc`. Evidence: `example/authzed/extsvc/folder.gen.go`, `example/authzed/bookingsvc/employee.gen.go`, `example/authzed/menusvc/menuitem.gen.go` all expose methods with the signature `func (<entity>) Check<Perm>(ctx, Inputs) (bool, error)` and `func (<entity>) Lookup<Perm>Resources(ctx, ...) (LookupResult, error)`.

- **A4 [VERIFIED]:** SpiceDB testcontainer setup is established via `pkg/authz/spicedb/spicedbtest.Start(ctx, schema)` and is the canonical e2e harness for this project's generated stubs. Evidence: every existing e2e test in `example/authzed/{bookingsvc,menusvc,extsvc}/` uses this pattern (per AUZ-005); CI runs them. The `spicedbtest.ErrDockerUnavailable` sentinel handles Docker-absent environments cleanly.

- **A5 [EXTERNAL FACT]:** `github.com/open-policy-agent/opa/rego` exposes `Rego.RegisterBuiltin1`, `Rego.RegisterBuiltin2`, `types.NewFunction`, `types.S`, `types.B`, `types.A`, `ast.NewTerm` as stable public API for emitting custom builtins. Evidence: package documentation at https://pkg.go.dev/github.com/open-policy-agent/opa/rego.

- **A6 [HYPOTHESIS]:** The OPA dependency closure on the repo's `go.mod` is acceptable given the codegen project commits to Pattern 4 as a first-class feature. Verification: post-implementation, run `go mod graph | wc -l` before and after; if the indirect closure exceeds 200 new entries OR the binary size of `cmd/authzed-codegen` grows beyond 30 MB, revisit the build-tag mitigation.

- **A7 [VERIFIED]:** SpiceDB's `CheckPermission` and `LookupResources` RPCs accept caveat context as `*structpb.Struct`. Evidence: existing typed `Check<X>WithCaveat` codegen at `pkg/authz/spicedb/crud.go` (per AUZ-007) constructs `Context: structpb.MustNewStruct(...)` and passes it on the wire; SpiceDB's CEL evaluator coerces structpb fields to caveat parameter types at evaluation time (per AUZ-018 caveat parameter expansion). The `*structpb.Struct` type accepts arbitrary JSON-shaped maps via `structpb.NewStruct(map[string]any)` from the standard library.

## History

<!-- managed by `harness history-update` — do not hand-edit -->
