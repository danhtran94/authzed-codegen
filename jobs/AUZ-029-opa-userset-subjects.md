<!-- approved -->
# AUZ-029: Userset subjects in OPA builtins (`"<type>#<subrelation>"` keys)

| Field      | Value                                          |
|------------|------------------------------------------------|
| Status     | Done                                           |
| Created    | 2026-11-19                                     |
| Assignee   | Claude                                         |
| Source     | docs/spec-013-opa-go-builtins-codegen.md       |
| Blocked by | —                                              |

## Goal

Generated OPA builtins accept a userset subject key `"<type>#<subrelation>"`. Check routes it through `Engine.CheckPermissionUserset`, and lookup routes it through `Engine.LookupResourcesUserset` (AUZ-027). Semantics match the typed userset inputs: literal userset match, AND across keys for check, and direct-subject precedence for lookup. Release target: `v2.4.0`.

## Problem

Since AUZ-027 the typed `Check<Perm>` and `Lookup<Perm><Resource>Resources` methods answer userset inputs, but the OPA builtins could not express them. `subjectEntries` (`internal/templates/opa.go.tmpl`) treated every key as an object type, so `{"extsvc/team#admin": "t1"}` was sent to SpiceDB as object type `extsvc/team#admin`. SpiceDB returned an error, and OPA, in its default non-strict mode, turned that into an **undefined** result. This was verified on `main` (`d2b0f9e`): the new tests below return `<nil>` where `true`, `false` and `[folder]` are expected.

## Solution

- **`subjectEntry.SubRelation`:** `subjectEntries` splits each key at `#`. A key `"<type>#"` (empty sub-relation) is an error.
- **Sort order:** direct entries first, then userset entries, each group by key. Lookup takes the first entry, which preserves the typed API's direct-subject precedence; a plain lexical sort would put `extsvc/team#admin` before `extsvc/user`.
- **`checkImpl`:** a userset entry calls `CheckPermissionUserset` (with `nil` or the caveat context map). Direct entries work as before.
- **`lookupImpl`:** a userset first entry calls `LookupResourcesUserset`.

The key syntax mirrors SpiceDB's own userset notation (`team:t1#admin`). Calls with direct keys only are unaffected. A key containing `#` used to produce an undefined result, so a policy that relied on that, for example through a fallback rule or the old lexical lookup order, can now see a defined result (see CHANGELOG).

## Workstreams

| # | Task | File | Status |
|---|------|------|--------|
| 1.1 | `subjectEntry.SubRelation`, key parsing, empty-sub-relation error, direct-first sort | `internal/templates/opa.go.tmpl` | [x] |
| 1.2 | Userset dispatch in `checkImpl` and `lookupImpl` | same | [x] |
| 1.3 | Regenerate; no `func` / `type` line changes | `example/authzed/**/opa.gen.go` | [x] |
| 2.1 | `TestOPA_UsersetSubject_CheckAndLookup` (literal match, other team denied, lookup) | `example/authzed/extsvc/extsvc_opa_test.go` | [x] |
| 2.2 | `TestOPA_UsersetSubject_WithCaveat` (matching / wrong tenant, caveated lookup) | same | [x] |
| 2.3 | `TestOPA_UsersetSubject_MixedKeys` (AND across direct + userset; lookup direct-first) | same | [x] |
| 2.4 | `TestOPA_UsersetSubject_EmptySubRelation_Errors` (strict mode errors; default mode undefined) | same | [x] |
| 2.5 | `TestOPA_UsersetSubject_MalformedKeys_Error`: empty sub-relation with empty ids, second `#`, empty type, malformed key beside a direct key (review of #21) | same | [x] |
| 3.1 | README OPA section: userset keys; correct the builtin-error claim | `README.md` | [x] |
| 3.2 | `## [2.4.0] - Unreleased` | `CHANGELOG.md` | [x] |

## What Stays Unchanged

- Builtin names, arity and declared types (the subject stays a dynamic object).
- Engine, `pkg/authz`, and typed generated code.

## Discoveries & Decisions During Implementation

### [Implementer] Builtin errors are undefined, not eval failures, by default

`TestOPA_UsersetSubject_EmptySubRelation_Errors` first expected `Eval` to return an error and got `nil`. OPA only surfaces builtin errors as evaluation errors under `rego.StrictBuiltinErrors(true)`; by default the call is undefined. README had claimed "any other engine error fails the Rego eval", so it is corrected here. The test now asserts both modes. In default mode the result is undefined, which a `default allow := false` policy treats as deny, so it fails closed.

### [Implementer] Regression proven against pre-fix generated code

`main`'s `opa.gen.go` files were temporarily checked out with the new tests in place. Every userset assertion returned `<nil>`. The new code was then restored, and a round-trip regeneration was zero-diff.

### [Reviewer] CodeRabbit on #21: key validation order and overclaims

All five findings were valid:

- Key validation ran after the empty-id skip, so `{"extsvc/team#": []}` returned `false` / `[]` instead of an error.
- `"extsvc/team#admin#"` passed `strings.Cut` with sub-relation `admin#`.
- The template's header comment still described static compile-time-checked keys.
- The job doc claimed no working policy changes behavior.
- The README equated "undefined" with "denied" regardless of other rules.

Fixes: keys are validated before ids (empty type, empty sub-relation, second `#`), and task 2.5 pins this with four cases, including a malformed key beside a valid direct key. The comment, job doc, CHANGELOG and README were reworded to match.
