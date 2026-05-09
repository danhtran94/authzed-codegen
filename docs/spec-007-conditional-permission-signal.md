# [SPEC-007] Conditional Permission Rich Signal

| Field      | Value                                                |
|------------|------------------------------------------------------|
| Status     | Accepted                                             |
| Created    | 2026-05-09                                           |
| Author     | Danh Tran                                            |
| Implements | (closes silent collapse documented in CHANGELOG v1.1.0+) |

---

## Overview

This SPEC adds a typed error path for SpiceDB's `CONDITIONAL_PERMISSION` Permissionship — today the codegen collapses it to `ErrPermissionDenied` and silently drops the `PartialCaveatInfo.MissingRequiredContext` field. Recoverable failures (the caller forgot to supply some caveat keys) become indistinguishable from hard denies (the user genuinely lacks permission). After this SPEC, `Check<Perm>` paths reaching caveats can return a `*ConditionalPermissionError` carrying `MissingKeys []string`; existing callers that match `errors.Is(err, ErrPermissionDenied)` keep working unchanged via a custom `Is` method, and new callers can additionally detect the conditional case via `errors.Is(err, ErrConditionalPermission)` + `errors.As(err, &cpe)` to extract the missing keys for a retry.

**What this component does:** Add `ErrConditionalPermission` sentinel error and `ConditionalPermissionError` typed struct in `pkg/authz/authz.go`. The struct carries `MissingKeys []string` and implements custom `Is` method matching both `ErrConditionalPermission` AND `ErrPermissionDenied` (backward compat). Update `errorIfDenied` in `pkg/authz/spicedb/crud.go` to detect `Permissionship == PERMISSIONSHIP_CONDITIONAL_PERMISSION` and return the rich error; `PERMISSIONSHIP_NO_PERMISSION` continues to return the existing `ErrPermissionDenied` sentinel. The single change to `errorIfDenied` propagates the rich signal to every existing caller — `CheckPermission`, `CheckPermissionWithCaveat`, `CheckPermissionUserset` — without per-method changes. Generated `Check<Perm>` methods inherit the new behavior automatically.

**What this component does not do:** Surface `CONDITIONAL_PERMISSION` results from Lookup paths. Per A1 — `LookupResources` / `LookupSubjects` already filter `Permissionship != HAS_PERMISSION` silently per AUZ-008. Surfacing the conditional-but-recoverable subset of Lookup results would change the return shape from `[]ID` to a struct combining definite + conditional results; deferred until concrete demand. Generate template changes — the rich error flows through the existing `(bool, error)` return shape, no codegen diff. Modify any `Check<Perm>Inputs` struct shapes — caller still supplies the same caveat fields, the Engine just returns more detail when context is missing. Provide automatic retry — the SPEC exposes the missing keys; deciding whether to fetch and retry is the caller's concern.

---

## Interface Contracts

### Runtime types — `pkg/authz/authz.go`

```go
// ErrConditionalPermission is the sentinel error indicating SpiceDB returned
// PERMISSIONSHIP_CONDITIONAL_PERMISSION — the permission would be granted
// IF the caller supplied additional caveat context (the keys named on the
// returned *ConditionalPermissionError).
//
// For backward compatibility with v1.5 and earlier, errors satisfying this
// sentinel also satisfy ErrPermissionDenied — callers checking the existing
// deny pattern (`errors.Is(err, ErrPermissionDenied)`) keep working without
// changes; callers wanting the rich signal use `errors.As` to extract a
// *ConditionalPermissionError and inspect MissingKeys.
var ErrConditionalPermission = errors.New("conditional permission")

// ConditionalPermissionError is the typed error returned by Check<Perm>
// methods when SpiceDB indicates conditional permission. MissingKeys is the
// caveat parameter names from PartialCaveatInfo.MissingRequiredContext —
// the wire-level signal from SpiceDB telling the caller which keys to
// fetch and retry with.
type ConditionalPermissionError struct {
    MissingKeys []string
}

func (e *ConditionalPermissionError) Error() string {
    return fmt.Sprintf("conditional permission: missing %v", e.MissingKeys)
}

// Is supports both:
//   errors.Is(err, ErrConditionalPermission)  → true (rich-signal path)
//   errors.Is(err, ErrPermissionDenied)       → true (backward-compat path)
func (e *ConditionalPermissionError) Is(target error) bool {
    return target == ErrConditionalPermission || target == ErrPermissionDenied
}
```

`MissingKeys` is a slice of caveat parameter names (e.g. `["tenant"]`) — values directly from `PartialCaveatInfo.MissingRequiredContext`. Empty slice is possible when SpiceDB returns CONDITIONAL without specific missing keys (rare; per A2 — usually means the caveat expression returned an indeterminate value rather than missing context). Callers should treat empty `MissingKeys` as "conditional but no recovery hint."

### `errorIfDenied` extension — `pkg/authz/spicedb/crud.go`

```go
func errorIfDenied(res *v1.CheckPermissionResponse, err error) error {
    if err != nil {
        return err
    }

    switch res.Permissionship {
    case v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION:
        return nil

    case v1.CheckPermissionResponse_PERMISSIONSHIP_CONDITIONAL_PERMISSION:
        var missing []string
        if pci := res.PartialCaveatInfo; pci != nil {
            missing = pci.MissingRequiredContext
        }
        return &authz.ConditionalPermissionError{MissingKeys: missing}

    default:
        return authz.ErrPermissionDenied
    }
}
```

The function is the single point of error construction across `CheckPermission`, `CheckPermissionWithCaveat`, `CheckPermissionUserset`. Updating it propagates the rich signal to every generated `Check<Perm>` method without per-method codegen changes (per the design pick — no template diff needed).

### Generated `Check<Perm>` — no change

Generated method bodies route through `engine.CheckPermission*` and bubble up whatever error the engine returns. The richer error type is transparent to the codegen layer:

```go
// Generated body (unchanged):
err := authz.GetEngine(ctx).CheckPermissionWithCaveat(ctx, ..., caveatCtx)
if err != nil {
    return false, err  // err may now be *ConditionalPermissionError
}
```

Caller pattern at the call site (illustrative — caller code, not codegen):

```go
err := folder.CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
    User: []extsvc.User{user},
    // Caveats: ... — caller forgot to supply tenant
})
switch {
case err == nil:
    // granted — proceed
case errors.Is(err, authz.ErrConditionalPermission):
    var cpe *authz.ConditionalPermissionError
    errors.As(err, &cpe)
    // cpe.MissingKeys == ["tenant"] — fetch from request context and retry
    return retryWithMissing(ctx, cpe.MissingKeys)
case errors.Is(err, authz.ErrPermissionDenied):
    // hard deny — user genuinely lacks permission
    return Forbidden
default:
    // wire / transport error
    return err
}
```

### Lookup paths — unchanged in this SPEC

`LookupResources` / `LookupSubjects` / their `WithCaveat` variants continue to filter `Permissionship != HAS_PERMISSION` silently per AUZ-008. Per A1 — surfacing the conditional-but-recoverable subset would change return shape (e.g. add a `[]ConditionalEntry{ID, MissingKeys}` slice alongside the typed `[]ID`). Deferred to a future SPEC if real demand surfaces.

---

## Sequence

Wire flow when caller supplies incomplete caveat context:

```
caller code:

    err := folder.CheckTenantedBrowse(ctx, extsvc.CheckFolderTenantedBrowseInputs{
        User: []extsvc.User{user},
        // Caveats omitted entirely OR Caveats.TenantMatch.Tenant == nil
    })
         │
         ▼
generated method body:

    ├─► caveatCtx == nil  (no fields populated)
    │
    └─► engine.CheckPermissionWithCaveat(ctx, ..., nil /*caveatCtx*/)

         │
         ▼
*spicedb.Engine.CheckPermissionWithCaveat:

    ├─► serializeCaveatMap(nil) → returns nil structpb.Struct
    │
    └─► client.CheckPermission(ctx, &CheckPermissionRequest{
            Subject: ...,
            Context: nil,  ← no caveat context
        })
              │
              ▼
SpiceDB evaluator:

    folder:f1 #tenanted_browse → tenanted_viewer with extsvc/tenant_match
        │
        ├─► tuple exists, but tenant_match needs `tenant` parameter to evaluate
        ├─► no Context provided → caveat eval returns INDETERMINATE
        ├─► response: PERMISSIONSHIP_CONDITIONAL_PERMISSION
        │   PartialCaveatInfo.MissingRequiredContext = ["tenant"]
              │
              ▼
errorIfDenied receives the response:
    ├─► switch on Permissionship:
    │     CONDITIONAL_PERMISSION → return &ConditionalPermissionError{MissingKeys: ["tenant"]}
    │
    └─► generated method returns (false, &ConditionalPermissionError{...})

         │
         ▼
caller code matches the rich error:

    errors.Is(err, ErrConditionalPermission)  → true
    errors.As(err, &cpe)                      → cpe.MissingKeys = ["tenant"]
    errors.Is(err, ErrPermissionDenied)       → true (backward compat)
```

Backward compatibility check — existing v1.5 caller code:

```
caller (v1.5 era):
    err := folder.CheckTenantedBrowse(ctx, ...)
    if errors.Is(err, authz.ErrPermissionDenied) {
        return Forbidden
    }
         │
         ▼
SPEC-007 returns *ConditionalPermissionError on CONDITIONAL:
    │
    └─► errors.Is(err, ErrPermissionDenied)
        → custom Is method on *ConditionalPermissionError
        → returns true (target == ErrPermissionDenied case)
              │
              ▼
caller gets Forbidden — SAME behavior as v1.5 ✓
```

---

## Errors

| Error class | Trigger | Layer |
|---|---|---|
| `nil` | `Permissionship == HAS_PERMISSION` (existing — unchanged) | Engine |
| `*ConditionalPermissionError{MissingKeys}` | `Permissionship == CONDITIONAL_PERMISSION`. NEW; replaces the previous silent collapse to `ErrPermissionDenied`. Backward-compat: `errors.Is(_, ErrPermissionDenied)` still returns true via custom `Is` method. | Engine |
| `ErrPermissionDenied` (sentinel) | `Permissionship == NO_PERMISSION` (existing — unchanged) | Engine |
| Underlying gRPC error | Transport failure, malformed request. Bubbles up unwrapped (existing). | Engine |
| `serialize caveat params: <wrapped>` (existing) | Caveat context contains protobuf-incompatible values. Bubbles up before the wire call. | Engine |

---

## Constraints

- **C1.** `ConditionalPermissionError` is returned as a pointer (`*ConditionalPermissionError`). Required for `errors.As` to work — `errors.As` requires the target to be a non-nil pointer to a struct or interface implementing the error chain.

- **C2.** The custom `Is` method satisfies BOTH `ErrConditionalPermission` and `ErrPermissionDenied`. Per the backward-compat design pick — existing v1.5 callers checking `errors.Is(err, ErrPermissionDenied)` see no behavior change. New callers can additionally check `errors.Is(err, ErrConditionalPermission)` to distinguish the recoverable case.

- **C3.** `MissingKeys` may be an empty slice. Per A2 — SpiceDB sometimes returns CONDITIONAL without populated `MissingRequiredContext` (e.g. when the caveat expression returned an indeterminate value rather than missing context). Callers should not assume non-empty.

- **C4.** Lookup paths (`LookupResources` / `LookupSubjects` / `*WithCaveat`) are unchanged. Per A1 — they continue to filter `Permissionship != HAS_PERMISSION` silently. The rich signal applies to Check paths only in this SPEC.

- **C5.** Generated `Check<Perm>` method bodies are unchanged. The richer error flows through the existing `(bool, error)` return; no template diff. Per the design pick (zero codegen change).

- **C6.** Backward compat preserved at the `errors.Is(_, ErrPermissionDenied)` level. The `(bool, error)` return shape is preserved. Existing tests checking `assert.ErrorIs(t, err, authz.ErrPermissionDenied)` keep passing for both `NO_PERMISSION` and `CONDITIONAL_PERMISSION` cases. New tests verify the rich-signal path explicitly.

- **C7.** No retry logic in the engine. Per the SPEC's "what it does not do" — the engine surfaces the missing keys; the caller decides whether to fetch context and retry. A future helper could wrap this pattern (e.g. `CheckPermissionWithFetcher(ctx, ..., fetcher func([]string) map[string]any)`), but adding the helper is out of scope here.

---

## Assumptions

- **A1 [VERIFIED]:** Lookup paths already silently filter `CONDITIONAL_PERMISSION` per AUZ-008. Evidence: `pkg/authz/spicedb/crud.go:494,542` — `data.Permissionship != LOOKUP_PERMISSIONSHIP_HAS_PERMISSION` skips the entry; AUZ-008's `TestFolder_LookupTenantedBrowse_*_NoCaveat` tests verify the silent filter.

- **A2 [EXTERNAL FACT]:** SpiceDB's `CheckPermissionResponse.PartialCaveatInfo` is an optional field — non-nil only when `Permissionship == CONDITIONAL_PERMISSION`. The `MissingRequiredContext` field within may be empty if the caveat expression returned an indeterminate value rather than identifying specific missing keys. Evidence: `go doc github.com/authzed/authzed-go/proto/authzed/api/v1 PartialCaveatInfo` confirms the optional-when-missing shape; SpiceDB's CEL evaluator returns INDETERMINATE for missing-context AND for ambiguous expressions.

- **A3 [VERIFIED]:** Go's `errors.Is` invokes the target's custom `Is` method when present. Per `go doc errors.Is` — "An error is considered to match a target if it is equal to that target or if it implements a method `Is(error) bool` such that `Is(target)` returns true." Custom `Is` matching multiple targets is the documented pattern for backward-compat error wrapping.

- **A4 [VERIFIED]:** Go's `errors.As` dereferences typed errors via the chain. `errors.As(err, &cpe)` where `cpe *ConditionalPermissionError` works whether the err is the typed pointer directly OR wrapped via `fmt.Errorf("...%w", cpe)`. Evidence: standard library docs.

- **A5 [HYPOTHESIS]:** No existing test relies on `CONDITIONAL_PERMISSION` collapsing silently to `ErrPermissionDenied` in a way that would observe behavior beyond the `errors.Is(_, ErrPermissionDenied)` match. Verification deferred to WS3 — running the full e2e suite after the change. If a test uses something like `err.Error() == "permission denied"` (string match), update it.

- **A6 [VERIFIED]:** Existing AUZ-006/007 caveat tests like `TestFolder_CheckTenantedBrowse_NoCaveat` and `TestFolder_CheckTenantedBrowse_WrongTenant` already exercise the CONDITIONAL_PERMISSION wire response (they expect deny when caveat eval is false or context is missing). Per the C6 backward-compat constraint, those tests keep passing because `errors.Is(_, ErrPermissionDenied)` continues to match the new typed error.

---

## Unresolved Questions

(none)

---

## Summary

Net change scope:

| File | Change |
|---|---|
| `pkg/authz/authz.go` | Add `ErrConditionalPermission` sentinel error, `ConditionalPermissionError` struct with `MissingKeys []string` field, custom `Error()` and `Is(target error) bool` methods. Add `errors`, `fmt` imports if not already present. |
| `pkg/authz/spicedb/crud.go` | Update `errorIfDenied` to switch on `Permissionship`: HAS_PERMISSION → nil; CONDITIONAL_PERMISSION → typed pointer error with MissingKeys from `PartialCaveatInfo.MissingRequiredContext`; default → existing `ErrPermissionDenied`. |
| `example/authzed/extsvc/extsvc_test.go` | Add e2e tests demonstrating the three semantic cases distinguish: granted, conditional-with-missing-keys, hard-denied. Verify backward-compat: `errors.Is(_, ErrPermissionDenied)` matches all three deny cases. |
| `internal/templates/object.go.tmpl` | NO CHANGES. The rich error flows through the existing `(bool, error)` return. |
| `example/authzed/**/*.gen.go` | NO REGENERATION required. Codegen output is byte-identical to v1.5.0. |

E2E tests cover: caveat-context-supplied → granted (existing pattern, regression check); caveat-context-missing → conditional with `MissingKeys` populated; caveat-eval-false (e.g. wrong tenant value) → hard deny (NOT conditional — eval returned false, not indeterminate); backward-compat assertion that all deny cases still match `errors.Is(err, ErrPermissionDenied)`.

---

## History

(History is owned by `harness history-update` — do not hand-edit.)
