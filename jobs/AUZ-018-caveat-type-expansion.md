# AUZ-018: Caveat Type Expansion (duration / timestamp / ipaddress)

| Field      | Value                                              |
|------------|----------------------------------------------------|
| Status     | Done                                                |
| Created    | 2026-05-09                                         |
| Assignee   | danhtran94                                         |
| Source     | (audit finding from AUZ-017 follow-up — no SPEC)   |
| Blocked by | —                                                  |

<!-- approved -->
<!-- source: free-prose-deliberate (reason: small additive change identified during AUZ-017 schema-coverage audit; size doesn't warrant a full SPEC) -->

---

## Goal

Extend `caveatTypeToGo` to surface SpiceDB's `duration`, `timestamp`, and `ipaddress` caveat parameter types as typed Go values on generated `<Caveat>Args` structs. Today these fall back to `any`, forcing callers to construct `interface{}`-shaped values manually. After this job: `duration` → `*time.Duration` with `.String()` conversion at the encoding site; `timestamp` → `*time.Time` with `.Format(time.RFC3339)` conversion; `ipaddress` → `*string` (caller passes pre-formatted IP — avoiding a `net` package import for a rarely-used type). `map<K,V>` stays as `any` (no clean Go mapping). Caller DX improves for the common time-based caveat patterns (rate limiting, session windows, time-bound permissions).

## Problem

    Current (post-v1.12.0):
      caller declares `caveat session_valid(expires timestamp) { ... }`
        → caveatTypeToGo("timestamp") → "any"
          → generated SessionValidArgs.Expires *any
            ✗ caller has to remember the wire format (RFC 3339 string)
            ✗ no compile-time type safety on the timestamp value
            ✗ same friction for duration (caller manages "1h30m"-style strings)

SpiceDB's caveat-type registration (`pkg/caveats/types/basic.go:79-117`) confirms these are sent as STRINGS on the wire — server-side parsed via `time.ParseDuration` and `time.Parse(time.RFC3339, ...)`. The codegen can convert typed Go values to wire strings at the encoding site, giving callers proper Go types without surfacing the wire format.

## Solution: Typed Go scalars + per-type conversion at structpb encoding site

    After fix:
      caller passes typed value:
        Caveats: SessionValidArgs{
            Expires: &time.Time{...},   // proper Go type
            Window:  &time.Duration{}, // proper Go type
        }
        → generated CheckPerm body emits:
            caveatCtx["expires"] = c.Expires.Format(time.RFC3339)
            caveatCtx["window"]  = c.Window.String()
          → structpb stores as standard string values
            → SpiceDB parses server-side ✓

`ipaddress` stays as `*string` — surfacing `*net.IP` would add a new import; users with typed IP values call `.String()` once at the call site.

### Components

**`caveatTypeToGo` extension** — three new case branches in `internal/generator/adapter.go:396-422`:
- `case "duration":` → `*time.Duration`
- `case "timestamp":` → `*time.Time`
- `case "ipaddress":` → `*string`

**`caveatValueExpr` template helper** — new helper in `internal/generator/generator.go` returning the right Go expression for converting a typed caveat field to a structpb-compatible value:
- `*time.Duration` → `c.Param.String()`
- `*time.Time` → `c.Param.Format(time.RFC3339)`
- `*string`, `*int`, `*bool`, etc. → `*c.Param` (existing `deref` semantic)

**Template body update** — replace `{{ deref $param.GoType }}c.{{ snakeToPascal $param.Name }}` with `{{ caveatValueExpr $param.GoType (printf "c.%s" (snakeToPascal $param.Name)) }}`. Five sites in `object.go.tmpl` (write-time + check-time caveat encoding).

**Schema fixture additions** — three new caveats on `extsvc/`:
- `caveat extsvc/within_window_d(window duration) { window > duration("0s") }` — exercises duration
- `caveat extsvc/before_deadline(deadline timestamp) { deadline > timestamp("2024-01-01T00:00:00Z") }` — exercises timestamp
- `caveat extsvc/from_subnet(client_ip ipaddress) { client_ip == ipaddress("10.0.0.1") }` — exercises ipaddress

Plus relations on `extsvc/folder` to wire each caveat into a permission for round-trip testing.

## Why not alternatives

| Approach | Verdict |
|---|---|
| **Typed Go scalars + per-type conversion** (chosen) | Best DX. Users pass real Go types. Conversion at codegen-emit site keeps runtime simple. |
| Use `*string` for all three | Rejected. Loses type safety on duration/timestamp where Go has clean stdlib types. |
| Use `*string` for ipaddress, typed for others | Chosen — see Components above. `net.IP` import isn't worth forcing on every gen file when ipaddress caveats are rare. |
| Auto-convert in runtime `serializeCaveatMap` | Rejected. Reflection-based type switches at runtime are slower and harder to debug than codegen-emitted converters. |
| Surface `*netip.Addr` (Go 1.18+) instead of `*string` for ipaddress | Rejected. Same import-cost concern; `netip` is even less universal than `net.IP` in existing codebases. Could revisit if a real user requests it. |

## Workstreams

### 1. Adapter — extend `caveatTypeToGo`

| #   | Task | File | Status |
|-----|------|------|--------|
| 1.1 | Add `case "duration":` returning `"*time.Duration"` to `caveatTypeToGo` | `internal/generator/adapter.go` | [x] |
| 1.2 | Add `case "timestamp":` returning `"*time.Time"` | same | [x] |
| 1.3 | Add `case "ipaddress":` returning `"*string"` | same | [x] |

**Key details:** Existing fallthrough to `any` for `map<K,V>` stays unchanged. Per the SpiceDB type registration in `pkg/caveats/types/basic.go` — `duration` / `timestamp` are wire-encoded as strings (parsed server-side); `ipaddress` is a string in standard IP-string format.

### 2. Template helper — `caveatValueExpr`

| #   | Task | File | Status |
|-----|------|------|--------|
| 2.1 | Add `caveatValueExpr(goType, varExpr string) string` to FuncMap. Returns `varExpr + ".String()"` for `*time.Duration`, `varExpr + ".Format(time.RFC3339)"` for `*time.Time`, `"*" + varExpr` for other pointer types, `varExpr` for non-pointer types | `internal/generator/generator.go` | [x] |
| 2.2 | Update `internal/templates/object.go.tmpl` — replace 5 occurrences of `{{ deref $param.GoType }}c.{{ snakeToPascal $param.Name }}` with `{{ caveatValueExpr $param.GoType (printf "c.%s" (snakeToPascal $param.Name)) }}` | `internal/templates/object.go.tmpl` | [x] |
| 2.3 | Keep the existing `deref` helper available for any non-caveat use (audit; remove if unused after WS2.2 swap) | `internal/generator/generator.go` | [x] |

**Key details:** The existing `deref` template helper returns just `"*"` or `""`; the new `caveatValueExpr` returns a complete Go expression. Different shape; not interchangeable. Five emission sites in the template — three for caveat write paths (Create / Wildcard-Create / Caveat-permission Check) and two for Check / Lookup paths reading caveat input.

### 3. Schema fixture additions

| #   | Task | File | Status |
|-----|------|------|--------|
| 3.1 | Add `caveat extsvc/within_window_d(window duration) { window > duration("0s") }` | `example/schema.zed` | [x] |
| 3.2 | Add `caveat extsvc/before_deadline(deadline timestamp) { deadline > timestamp("2024-01-01T00:00:00Z") }` | same | [x] |
| 3.3 | Add `caveat extsvc/from_subnet(client_ip ipaddress) { client_ip.in_cidr("10.0.0.0/24") }` (CEL has no ipaddress() constructor; use `in_cidr` member function instead — see Discoveries) | same | [x] |
| 3.4 | Add three relations on `extsvc/folder` wiring each caveat: `relation duration_viewer: extsvc/user with extsvc/within_window_d`; `relation deadline_viewer: extsvc/user with extsvc/before_deadline`; `relation subnet_viewer: extsvc/user with extsvc/from_subnet`. Each gets a corresponding permission for Check exercising. | same | [x] |
| 3.5 | Run codegen — `go run ./cmd/authzed-codegen --output example/authzed example/schema.zed` — commit regenerated `folder.gen.go` | `example/authzed/extsvc/folder.gen.go` | [x] |

**Key details:** Caveat names use a numerical suffix (`_d` for duration) to avoid collision with the existing `within_window` (which uses `list<string>` + `string` params). Per AUZ-007 ext fixtures already exercise scalars and lists; AUZ-018 fixtures exercise the new typed scalars.

### 4. E2E tests — round-trip verification

| #   | Task | Status |
|-----|------|--------|
| 4.1 | E2E: duration grant — write `duration_viewer` for u1 with caveat pre-bound `Window: time.Hour`; Check evaluates `window > 0s` → granted — `example/authzed/extsvc/extsvc_test.go` | [x] |
| 4.2 | E2E: duration deny — write with caveat pre-bound `Window: 0s` (zero duration); Check evaluates `window > 0s` → denied | [x] |
| 4.3 | E2E: timestamp grant — write `deadline_viewer` for u1 with caveat pre-bound `Deadline: future-time`; Check → granted | [x] |
| 4.4 | E2E: timestamp deny — write with caveat pre-bound `Deadline: past-time` (before 2024); Check → denied | [x] |
| 4.5 | E2E: ipaddress grant — write `subnet_viewer` for u1 with `ClientIp: "10.0.0.5"` (matching CIDR); Check → granted | [x] |
| 4.6 | E2E: ipaddress deny — write with `ClientIp: "192.168.1.1"` (not matching); Check → denied | [x] |
| 4.7 | E2E: regression sweep — full e2e suite passes after WS1+WS3 — `go test ./pkg/authz/spicedb/... ./example/authzed/...` | [x] |

### 5. Documentation + release prep

| #   | Task | Status |
|-----|------|--------|
| 5.1 | Add `[1.13.0]` entry to `CHANGELOG.md` documenting the type expansion, conversion semantics at codegen-emit site, and the `*string` choice for ipaddress | [x] |
| 5.2 | Update `README.md` Caveats section — add a note listing the supported caveat parameter types and the `time` package convenience for duration/timestamp | [x] |
| 5.3 | Tag `v1.13.0` after merge; create GitHub release | [x] |

## Design Decisions

### Type-typed Go for duration/timestamp; `*string` for ipaddress
The cost-benefit tilts on import overhead. `time` is already a universal import (AUZ-010 made it always-imported in generated files). Adding `net` for ipaddress would touch every `.gen.go` file regardless of usage. ipaddress caveats are rare; users with typed IPs call `.String()` once at the call site.

### Conversion at codegen-emit site, not in runtime
Codegen knows the type at generation time. Per-type conversion in the emitted body is faster (no reflection) and easier to debug than a type-switch in `serializeCaveatMap`. Mirrors the existing pattern where `*string`, `*int`, etc. are converted via the `deref` helper at the emit site.

### Keep `map<K,V>` as `any`
No clean Go mapping. `map[string]any` could work for `map<string, any>` but SpiceDB's typed maps support arbitrary key/value types; surfacing them safely needs more thought. Defer.

### Caveat name disambiguation in fixtures
The existing `extsvc/within_window` uses `list<string>` + `string` params. Adding `extsvc/within_window_d` (separate namespace position via the `_d` suffix) avoids cross-fixture name collision and keeps both types' tests self-contained.

## What Stays Unchanged

- All existing `Engine.*` method signatures
- `pkg/authz/spicedb/crud.go` `serializeCaveatMap` — the runtime is type-agnostic; codegen converts typed values to strings before they reach `structpb.NewStruct`
- All existing caveat fixtures in `example/schema.zed` (only NEW caveats added)
- Per-namespace `.gen.go` files for definitions without the new caveats — byte-identical to v1.12.0
- The `deref` template helper — preserved for any non-caveat callers
- Codegen idempotency invariant — `git diff --quiet example/authzed/` zero-diff after a second pass

## Implementation Order

    1. WS1 Adapter         ← three case branches in caveatTypeToGo
    2. WS2 Template helper  ← caveatValueExpr + 5 template sites
    3. WS3 Schema fixture   ← depends on WS1+WS2 (codegen needs to handle the new types)
    4. WS4 E2E tests        ← depends on WS3 (fixture in place)
    5. WS5 Docs + release   ← last; depends on test pass

WS1 + WS2 land as one commit (atomic). WS3 follows in the same commit (regen requires both adapter + template). WS4 lands separately for review clarity. WS5 closes.

## Notes

- Round-trip the example fixture before declaring any generator change done.
- Full e2e suite must pass: `go test ./pkg/authz/spicedb/... ./example/authzed/...`.
- Version bump is `1.13.0` (minor) — purely additive; `any` fallback path remains intact for `map<K,V>` and unknown types.
- `harness validate-pr-checklist` will hard-block a push with `Status=Done` while any task row is `[ ]`.
- Test caveat expressions use literal duration / timestamp / ipaddress constructors (`duration("0s")`, `timestamp("2024-01-01T00:00:00Z")`, `ipaddress("10.0.0.1")`) — verify these are valid CEL syntax for SpiceDB's caveat evaluator.

## Discoveries & Decisions During Implementation

### [Implementer] CEL has no `ipaddress(...)` constructor literal

Initial fixture used `client_ip == ipaddress("10.0.0.1")` for the ipaddress caveat. SpiceDB's compiler rejected at codegen time: "undeclared reference to 'ipaddress' (in container '')". Investigation: SpiceDB registers IPAddress as a `cel.OpaqueType` with the `in_cidr` member function as the only public operator (per `~/go/pkg/mod/github.com/authzed/spicedb@v1.52.0/pkg/caveats/types/ipaddress.go:74-95`). There's no global `ipaddress(...)` constructor function in CEL — values are only constructed by SpiceDB's wire decoder when caveat context arrives.

Fixture updated to use the natural pattern: `client_ip.in_cidr("10.0.0.0/24")`. CIDR-membership is the documented use case for the ipaddress type. Direct equality comparison between two ipaddress values would work if SpiceDB exposed it, but it's not in v1.52's CEL setup.

This is a SpiceDB limitation, not a codegen issue. Documented in the fixture comment for future readers who try the constructor pattern.

### [Implementer] Existing caveat fixtures regenerated byte-identical

The template swap from `{{ deref ... }}c.<field>` to `{{ caveatValueExpr ... (printf "c.%s" ...) }}` looked like a bigger change than it was. For all existing caveat parameter types (string, bool, int, uint, double, bytes, list), the new helper falls through to the same `*c.<field>` semantic. Verified via `diff -q` against the v1.12.0 baseline — all existing relation caveats regenerate byte-identical. Only the new `WithinWindowDArgs` / `BeforeDeadlineArgs` / `FromSubnetArgs` structs added new lines.

### [Implementer] Multiple Edit `replace_all: true` doesn't catch indentation variants

Five template sites had the same logical content but different indentation (8-space inside CreateRelations vs 6-space inside Check/Lookup). The first `replace_all: true` swap matched only 2 of 5. Had to do two separate Edits with the right leading whitespace per site. Lesson: Edit's string match is whitespace-exact; visually-identical lines aren't always identical.
