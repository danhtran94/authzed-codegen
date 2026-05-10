<!-- approved -->

# AUZ-022: OPA builtin subject arg — keyed object instead of "type:id" string

| Field      | Value                                              |
|------------|----------------------------------------------------|
| Status     | Done                                               |
| Created    | 2026-05-10                                         |
| Assignee   | Danh Tran                                          |
| Source     | jobs/AUZ-021-opa-global-builtins.md                |
| Blocked by | —                                                  |

<!-- Parent-job follow-up: AUZ-019 chose a "type:id" string for the subject -->
<!-- arg (forces sprintf("extsvc/user:%s",[id]) in every policy). This job -->
<!-- replaces it with a keyed object {"extsvc/user": id-or-ids} that mirrors -->
<!-- the codegen's own typed Check<X>Inputs shape, lets OPA type-check the -->
<!-- keys, and supports multi-subject. [1.14.0] is unreleased, so this is an -->
<!-- amendment to that version, not a breaking change to a shipped API. -->

## Goal

Change the generated OPA builtins' subject argument from a `"type:id"` string to a **keyed object** whose keys are the permission's allowed SpiceDB subject types (full namespace, e.g. `extsvc/user`) and whose values are a single id string or a list of id strings. The codegen already knows each permission's allowed subject types (the resolver's permission tree); it emits the builtin Decl with exactly those keys as `types.StaticProperty` entries — so OPA's compiler rejects unknown keys at policy-compile time. The binding iterates the object's present keys, maps each to its SpiceDB type, and calls `Engine.CheckPermission` per key with OR semantics — exactly what the typed `CheckFolderBrowse(Inputs{User, Group, Role})` does. Lookup builtins use the first present key (matching the typed `Lookup<Perm><Resource>Resources` behavior).

Before / after at the call site:

    # before (AUZ-019/021):
    extsvc.check_folder_browse(sprintf("extsvc/user:%s", [input.user.id]), input.resource.id, {})

    # after (AUZ-022):
    extsvc.check_folder_browse({"extsvc/user": input.user.id}, input.resource.id, {})

    # multi-subject (OR), now expressible:
    extsvc.check_folder_browse({"extsvc/user": ["alice","bob"], "extsvc/group": ["admins"]}, input.resource.id, {})

Both `SpiceDBBuiltins` (per-instance) and `RegisterSpiceDBBuiltinsGlobal` (global) get the new shape — they share the `{{ define }}` fragments. `parseSubject` / `errMalformedSubject` are removed (no more `:` splitting). Round-trip regenerates byte-identically; e2e tests + the `example/opa-embed` policy + SPEC-013 + CHANGELOG update to the object form.

## Problem

    Today (AUZ-019/021):
      check_<resource>_<perm>(subject string, resource_id string, ctx object) -> bool
        subject = "extsvc/user:alice"   ← caller does sprintf("extsvc/user:%s",[id])
        - one subject only (string can't carry multiple)
        - no compile-time validation of the type prefix
        - mismatch with the codegen's own typed CheckFolderBrowseInputs{User, Group, Role}

    After AUZ-022:
      check_<resource>_<perm>(subject object, resource_id string, ctx object) -> bool
        subject = {"extsvc/user": "alice"}   or   {"extsvc/user": ["a","b"], "extsvc/group": ["g"]}
        - Decl declares exactly the allowed-type keys → OPA rejects {"banana": "x"} at compile time
        - multi-subject OR falls out naturally (mirrors the typed method's OR-over-non-empty-slices)
        - faithful to the typed Go API: {"extsvc/user": [...]} ↔ Inputs{User: [...]}
        - Lookup: first present key wins (matches Lookup<Perm><Resource>Resources)

The codegen needs the per-permission allowed subject types — that's the resolver's permission tree (`ParseDefinitions(g.Definitions).GetPermissionTree()` → `tree["<prefix>/<name>/<perm>"]` = `[]string` of allowed namespaces). The OPA codegen (`GenerateOPASource`) doesn't build it today; WS1 adds it (a `permTypes` template func closing over the tree).

## What Stays Unchanged

- `resource_id` (2nd arg of Check) and `caveat_context` (3rd arg of Check, 2nd of Lookup) — unchanged: a string and an object respectively
- The two exported functions `SpiceDBBuiltins` / `RegisterSpiceDBBuiltinsGlobal` — names, signatures `(engine, ctx)` / `(engine, ctx) []func(*rego.Rego)`, the `{{ define }}`-block structure — all unchanged; only the subject-arg Decl + the subject-parsing inside the closures change
- `--emit-opa` flag, `internal/generator/adapter.go`, `pkg/authz/` — untouched
- Existing `<entity>.gen.go` files — untouched (only `opa.gen.go` regenerates)
- The error→term mapping (`checkResultToTerm`), the structpb helpers (`termToStructpb`, `astValueToInterface`, `structpbToMap`), the `LookupResult.Definite`-only behavior — unchanged
- `example/opa-embed/main.go` — unchanged (the subject is constructed inside `policy.rego`, not passed in the HTTP body); only `policy.rego` changes
- The demo's curl recipes — unchanged (the HTTP body's `input` shape is unchanged; the policy maps `input.user.id` into the object internally)
- `[1.14.0]` version line — this is an amendment to the unreleased version, not a new version bump (no git tag exists for 1.14.0)

## Workstreams

### 1. Generator — make the permission tree available to the template

| # | Task | File | Status |
|---|------|------|--------|
| 1.1 | Decide the subject-arg Decl. Investigated a static-property object keyed by the permission's allowed types (`types.NewObject([]*StaticProperty{...allowed types...}, nil)`) — would have needed a `permTypes` template func + `ParseDefinitions(g.Definitions).GetPermissionTree()` wired into the template. **Abandoned**: OPA's static object properties are effectively required-all, so declaring `object<extsvc/group, extsvc/role, extsvc/user>` makes the type checker reject the common single-key call `{"extsvc/user": x}` (`rego_type_error: invalid argument(s)`). Landed on a **fully-dynamic** object Decl — `types.NewObject(nil, types.NewDynamicProperty(types.S, types.NewAny(types.S, types.NewArray(nil, types.S))))`. `permTypes` + the tree resolution were added then reverted in `internal/generator/opa.go`; see Discoveries. | `internal/generator/opa.go` (no net change), `internal/templates/opa.go.tmpl` | [x] |
| 1.2 | (folded into 1.1) — with the fully-dynamic Decl the template doesn't need per-permission allowed-type knowledge: the closure iterates whatever keys the caller passed and calls `Engine.CheckPermission` per key; SpiceDB validates the subject type server-side (a bogus type → a non-`ErrPermissionDenied` error → the Rego eval fails). | — | [x] |

**Key details:** The object key is the SpiceDB namespace verbatim (`extsvc/user`, not the PascalCase Go field name `User`). JSON object keys are arbitrary strings, so `{"extsvc/user": ...}` is legal Rego. This avoids the short-name-collision problem (`extsvc/user` vs `othersvc/user` both → `user`) the typed codegen disambiguates via `IDFieldName` — the full namespace is unambiguous by construction.

### 2. Template — object subject arg

| # | Task | File | Status |
|---|------|------|--------|
| 2.1 | `checkDecl` / `lookupDecl`: subject arg `types.S` → `types.NewObject([]*types.StaticProperty{ ...one per allowed type from permTypes... }, nil)` where each property is `types.NewStaticProperty("<namespace>", types.NewAny(types.S, types.NewArray(nil, types.S)))` (accept a single id string OR a list of id strings) | `internal/templates/opa.go.tmpl` | [x] |
| 2.2 | `checkImpl`: replace the `subjStr`/`parseSubject` block with: assert `subjTerm.Value.(ast.Object)`; iterate present keys; for each `(namespace, value)` collect `[]authz.ID` via a new `idsFromTerm` helper (string → one-element slice; array of strings → slice; anything else → error); call `engine.CheckPermission(ctx, res, perm, authz.Type(namespace), ids)` (or `CheckPermissionWithCaveat` when `caveatCtx != nil`); accumulate with OR semantics — return `BooleanTerm(true)` as soon as one key grants; if all keys deny, `BooleanTerm(false)`; on a system error (non-`ErrPermissionDenied`/`ErrConditionalPermission`) from any key, fail the eval | same | [x] |
| 2.3 | `lookupImpl`: replace the subject block with: assert object; iterate keys in a deterministic order (sorted); take the FIRST present key, collect its ids, call `engine.LookupResources` / `LookupResourcesWithCaveat` for that `(namespace, ids)`; if no keys present, return an empty `[]string`; Go doc comment: "uses the first present subject-type key; pass exactly one key for predictable results — matches the typed Lookup<Perm><Resource>Resources behavior" | same | [x] |
| 2.4 | Add `idsFromTerm(t *ast.Term) ([]authz.ID, error)` helper: `ast.String` → `[]authz.ID{authz.ID(s)}`; `*ast.Array` of strings → slice; anything else → `subjectError("subject value must be a string or list of strings")` | same | [x] |
| 2.5 | Add a subject-object iteration helper if it keeps the closures readable: `subjectEntries(t *ast.Term) ([]subjectEntry, error)` returning sorted `[]struct{ Namespace string; IDs []authz.ID }` — used by both check and lookup closures | same | [x] |
| 2.6 | Remove `parseSubject`, `errMalformedSubject` (no longer used). Keep `subjectError`, `errMalformedCaveatContext`, `termToStructpb`, `astValueToInterface`, `structpbToMap`, `checkResultToTerm` | same | [x] |

**Key details:** Per SPEC-013 C1, the Decl's static properties must be emitted in a deterministic order — sort the `permTypes` result before iterating. The closure's key iteration must also be deterministic (sort `obj`'s keys) so the OR short-circuit and the Lookup "first key" are stable. The `checkImpl` OR semantics exactly mirror the typed `CheckFolderBrowse` method (which loops over non-empty subject slices and `Engine.CheckPermission`s each, returning `true` only if all succeed — wait: re-check the typed method's logic in WS2 implementation; the generated `CheckFolderBrowse` returns `false, err` on the first denial, `true` only if every non-empty slice's check succeeds — that's AND, not OR. Decide during implementation whether the object form should match that AND or use OR; AND matches the typed method; document the choice. **Tentative: match the typed method — AND across present keys.** Reconsider if it feels wrong.).

### 3. Regenerate fixtures

| # | Task | File | Status |
|---|------|------|--------|
| 3.1 | `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed` | (regenerate) | [x] |
| 3.2 | Verify each `example/authzed/{bookingsvc,menusvc,extsvc}/opa.gen.go` — Check builtins now have an object-typed subject arg with the allowed-type keys; `parseSubject` is gone; `idsFromTerm` is present | `example/authzed/*/opa.gen.go` | [x] |
| 3.3 | Round-trip: regen twice → identical (md5) | (verify) | [x] |

### 4. Update e2e tests + demo policy

| # | Task | File | Status |
|---|------|------|--------|
| 4.1 | `extsvc_opa_test.go`: change all `"extsvc/user:opa-u-1"` style args to `{"extsvc/user": "opa-u-1"}`; same for the with-caveat (`extsvc/user` subject) and Lookup tests; add a new test case `TestOPA_CheckFolderBrowse_MultiSubject` exercising `{"extsvc/user": ["a","b"], "extsvc/group": ["g"]}` against a schema with multiple allowed types (or `{"extsvc/user": ["a","b"]}` with two ids if a clean multi-type fixture isn't handy); the `TestOPA_GlobalRegistration` test's Rego also updates to the object form | `example/authzed/extsvc/extsvc_opa_test.go` | [x] |
| 4.2 | `example/opa-embed/policy/policy.rego`: `extsvc.check_folder_browse(sprintf("extsvc/user:%s", [input.user.id]), input.resource.id, {})` → `extsvc.check_folder_browse({"extsvc/user": input.user.id}, input.resource.id, {})` | `example/opa-embed/policy/policy.rego` | [x] |
| 4.3 | `example/opa-embed/README.md`: the policy-snippet reference / "what this demonstrates" mention of the subject form updates to the object shape; the curl recipes are UNCHANGED (HTTP `input` body is unchanged) — only the inline policy description changes | `example/opa-embed/README.md` | [x] |

### 5. Doc updates

| # | Task | File | Status |
|---|------|------|--------|
| 5.1 | SPEC-013 Interface Contracts: the subject arg is now an object `{"<namespace>": id-or-[]id}` whose keys are the permission's allowed SpiceDB subject types (declared as `types.StaticProperty` entries → compile-time validated); Check is AND-across-present-keys (matching the typed method) — or OR, per the WS2 decision; Lookup uses the first present key. Update the "as-shipped naming" block. Add a Constraint C9 noting the deterministic-key-order requirement (Decl props + closure iteration) | `docs/spec-013-opa-go-builtins-codegen.md` | [x] |
| 5.2 | AUZ-019 Discoveries: the "Subject argument shape — type:id string" entry gets a forward note that AUZ-022 replaced the string with a keyed object | `jobs/AUZ-019-opa-go-builtins-codegen.md` | [x] |
| 5.3 | CHANGELOG `[1.14.0]`: the "Subject-argument format" note + the demo bullet update to the object form; mention the multi-subject capability; note this amends the unreleased 1.14.0 (no behavior was ever shipped with the string form externally) | `CHANGELOG.md` | [x] |

### 6. Verification

| # | Task | Status |
|---|------|--------|
| 6.1 | `go build ./...` exits 0 | [x] |
| 6.2 | `go vet ./...` exits 0 | [x] |
| 6.3 | `go mod tidy` produces no diff | [x] |
| 6.4 | `go test ./pkg/authz/spicedb/... ./example/authzed/...` passes (incl. updated + new multi-subject test) | [x] |
| 6.5 | Round-trip: `go run ./cmd/authzed-codegen --output example/authzed --emit-opa example/schema.zed && git diff --quiet example/authzed/` exits 0 | [x] |
| 6.6 | Demo manual run: `go run ./example/opa-embed --port 18181 &`; `/health` → 200; `POST /v1/data/authz/allow` granted (alice/ReBAC, carol/RBAC) + denied (bob/other, banned/admin); `GET /v1/policies` → the (object-form) policy; clean SIGTERM | [x] |

## Design Decisions

### Key = full SpiceDB namespace, not the PascalCase Go field name
The object key is `extsvc/user` (verbatim namespace), not `User` or `user`. JSON object keys allow `/`, so it's legal Rego. This is unambiguous by construction — `extsvc/user` vs `othersvc/user` are distinct keys, no disambiguation needed (the typed codegen needs `IDFieldName` mangling because Go field names can't contain `/`). It also matches what `Engine.CheckPermission` takes as the subject type.

### Value = string OR list of strings
`{"extsvc/user": "alice"}` (single) and `{"extsvc/user": ["alice","bob"]}` (multiple) both work — the Decl declares the value type as `types.NewAny(types.S, types.NewArray(nil, types.S))`, and `idsFromTerm` normalizes both to `[]authz.ID`. The list form mirrors `Inputs{User: []User{...}}`.

### Check semantics across multiple present keys
**Tentative: AND** (every present key's check must succeed) — matching the generated typed `CheckFolderBrowse` method, which loops over non-empty subject slices and returns `false` on the first denial. To be confirmed in WS2 implementation by re-reading the typed method; if the typed method is actually OR, match that instead. Document the chosen semantics in SPEC-013 + the builtin's Go doc + the README. (For the common single-key case the distinction doesn't matter.)

### Lookup uses the first present key
`LookupResources` takes one subject type. The typed `Lookup<Perm><Resource>Resources` uses the first non-empty slice. The OPA Lookup binding does the same — iterate keys in sorted order, take the first present one. Documented; callers wanting predictable Lookup results pass exactly one key.

### `[1.14.0]` amendment, not a new version
AUZ-019/020/021 are all under the unreleased `[1.14.0]` CHANGELOG entry (no git tag). AUZ-022 changes the subject-arg shape before 1.14.0 ships — so the string form never reaches a released artifact. The CHANGELOG `[1.14.0]` bullets that describe the string form are rewritten to the object form; no `[1.15.0]` / `[2.0.0]` bump.

## Implementation Order

```
WS1 — Generator: permTypes func (tree wired into the template)   ← unblocks WS2
   ▼
WS2 — Template: object subject arg (Decl + closures + idsFromTerm)  ← depends on WS1
   ▼
WS3 — Regenerate fixtures                                          ← depends on WS2
   ▼
WS4 — Update e2e tests + demo policy        ┐ both depend on WS3
WS5 — Doc updates (SPEC-013, AUZ-019, CHANGELOG)  ┤ WS5 can parallel WS4
   ▼                                              │
WS6 — Verification (incl. demo run)               ┘ ← depends on WS1-5
```

## Notes

- **The typed-method semantics check (WS2)**: before finalizing the OR-vs-AND choice for multi-key Check, re-read a generated `CheckFolderBrowse` body (`example/authzed/extsvc/folder.gen.go`). The current read is "AND" — every non-empty subject slice's `Engine.CheckPermission` must succeed; `false, err` on the first denial. The object form should match whatever the typed method does. Record the decision in Discoveries.
- **`permTypes` keying**: `tree` is keyed `"<prefix>/<name>/<perm>"` where `<prefix>/<name>` is `def.ObjectType.String()`. The template has `$def` (with `.ObjectType.String`) and `$perm` (with `.Name`) in scope, so `permTypes $def.ObjectType.String $perm.Name` resolves. The result needs sorting for deterministic Decl-prop order.
- **Empty allowed-types edge case**: a permission whose `tree` entry is empty (no subject can satisfy it — unusual but possible with certain schema shapes) gets an empty-object Decl and a closure returning `false`. Comment it; don't crash.
- **Demo HTTP body unchanged**: `example/opa-embed/main.go` and the curl recipes don't change — the subject object is built *inside* `policy.rego` from `input.user.id`; the HTTP request's `input` shape (`{"user":{"id":...,"role":...},"resource":{"id":...}}`) is untouched.
- **`go.mod` unchanged**: no new dependencies — this is a template + closure-logic change only.

## Discoveries & Decisions During Implementation

### [Implementer] Static-property object Decl rejected — OPA's static object props are required-all; using a fully-dynamic Decl
The original plan (WS1) declared the subject arg as `types.NewObject([]*types.StaticProperty{ <one per allowed subject type> }, nil)` so OPA would type-check the keys at policy-compile time. This needed a `permTypes` template func + `ParseDefinitions(g.Definitions).GetPermissionTree()` wired into `GenerateOPASource`. It built and regenerated fine, but the e2e tests failed at policy-compile time: `rego_type_error: extsvc.check_folder_browse: invalid argument(s) — have: (object<extsvc/user: string>, string, object[any: any]) — want: (object<extsvc/group: ..., extsvc/role: ..., extsvc/user: ...>, string, object[string: any], boolean)`. Root cause: OPA's static object properties behave as **required-all** for assignability — an object literal must satisfy every declared static key. So `{"extsvc/user": x}` (one key) isn't assignable to `object<extsvc/group, extsvc/role, extsvc/user>` (three keys), which breaks the common single-key call. `types.StaticProperty` has no `Optional` flag. Resolution: the subject arg is a **fully-dynamic** object — `types.NewObject(nil, types.NewDynamicProperty(types.S, types.NewAny(types.S, types.NewArray(nil, types.S))))` — accepts any string key with a string-or-`[]string` value. Trade-off: bogus subject-type *keys* aren't caught at policy-compile time (a `{"banana": "x"}` typo surfaces as a runtime error from SpiceDB's `CheckPermission`, which returns a non-`ErrPermissionDenied` error → the Rego eval fails). The *value* type IS compile-time checked (a non-string/non-array fails OPA's type checker). `permTypes` + the tree resolution were reverted from `internal/generator/opa.go` (no net change to that file beyond the AUZ-021 state); the unused-`permTypes` path never shipped.

### [Implementer] Check semantics: AND across present subject-type keys (matches the typed method)
Re-read the generated typed `CheckFolderBrowse(Inputs{User, Group, Role})` body: it loops over non-empty subject slices and `Engine.CheckPermission`s each, returning `false, err` on the *first* denial — i.e. **AND** (every present subject type must be granted). The object-form binding mirrors this: `subjectEntries` yields namespace-sorted `(namespace, ids)` pairs; the closure loops them, `Engine.CheckPermission`s each, returns `false` on the first non-granted key, `true` only if all keys grant. For the common single-key call this is just one check. Lookup uses the first present key (sorted namespace order) — matching `Lookup<Perm><Resource>Resources`'s "first non-empty slice wins". Empty subject `{}` → Check returns `false`, Lookup returns `[]string{}` (the typed method errors with `ErrNoInput`; for a Rego builtin returning `false`/empty is more useful than failing the eval). `subjectEntries` sorts because OPA's `ast.Object.Foreach` order is not deterministic — needed for stable AND short-circuit, stable Lookup first-key, and byte-identical regeneration (SPEC-013 C9).

### [Implementer] e2e: object-form tests + multi-subject coverage
Updated all `extsvc_opa_test.go` call sites from `"extsvc/user:opa-u-1"` to `{"extsvc/user": "opa-u-1"}` (the no-caveat, with-caveat match/mismatch, Lookup, and `TestOPA_GlobalRegistration` cases). Added `TestOPA_CheckFolderBrowse_MultiSubject`: seeds `folder:opa-ms-1#viewer@extsvc/user:ms-u` AND `folder:opa-ms-1#viewer@extsvc/group:ms-g` (folder.viewer accepts user|group|role as direct subjects), then asserts `{"extsvc/user":"ms-u","extsvc/group":"ms-g"}` → true (both granted, AND), `{"extsvc/user":"ms-u","extsvc/group":"ms-g-missing"}` → false (group key denies → AND fails), `{"extsvc/user":["ms-u"]}` → true (list-of-ids value form). `example/opa-embed/policy/policy.rego`'s ReBAC leg changed from `sprintf("extsvc/user:%s",[input.user.id])` to `{"extsvc/user": input.user.id}`; demo manual run re-verified — `/health` 200, alice/ReBAC + carol/RBAC → true, bob/other + banned/admin → false, `/v1/policies` returns the (object-form) policy, clean SIGTERM.

### [Implementer] No further surprises
WS3 (regen — deterministic, both functions emit the object Decl), WS5 (SPEC-013 +C9 + subject-arg rewrite, AUZ-019 Discovery forward note, CHANGELOG `[1.14.0]` rewrite), WS6 (build / vet / mod tidy / test all clean; round-trip deterministic) proceeded as planned. `go.mod`/`go.sum` unchanged (no new deps — template + closure-logic change only). Round-trip vs the AUZ-021 commit: only the 3 `opa.gen.go` files changed (subject arg `types.S` → fully-dynamic object; `parseSubject`/`errMalformedSubject` gone; `subjectEntries`/`idsFromTerm` added; `checkResultToTerm` → `checkOneGranted`); no `<entity>.gen.go`/`schema.gen.go` churn.
