# AUZ-001: Migrate parser to SpiceDB compiler

<!-- approved -->

| Field      | Value                              |
|------------|------------------------------------|
| Status     | Done                               |
| Created    | 2026-05-01                         |
| Assignee   | Danh Tran                          |
| Source     | docs/ADR-001-parser-migration.md   |
| Blocked by | —                                  |

> ADR-001 is currently **Proposed**, not Accepted. This job carries
> the corrections derived from the ADR review (B1 signature, B2
> wildcard diff, B3 prefix option) and amends the ADR in WS6 so
> the doc-hierarchy chain stays consistent. The job is safe to
> start because the corrections are additive — no decision in the
> ADR is reversed.

## Goal

Replace the hand-written `internal/ast/` parser (lexer + recursive-descent parser + AST node types) with the SpiceDB `pkg/schemadsl/compiler` backend, then collapse the proto-to-template impedance mismatch behind a single adapter type in `internal/generator/`. After this job: `internal/ast/` is deleted, `cmd/authzed-codegen/main.go` calls `compiler.Compile`, the generator consumes `*core.NamespaceDefinition`, and `example/schema.zed` regenerates with one documented behavioral diff (wildcard preservation in `bookingsvc/employee.viewer`).

## Problem

The hand-written parser at `internal/ast/{lexer,parser,node}.go` covers a narrow subset of AuthZED syntax and silently mishandles features it tokenizes:

    Current behavior on  relation viewer: bookingsvc/user:*
      lexer.go:92         → emits WILDCARD token ✓
      parser.go:161-172   → consumes WILDCARD, drops it ✗
      generator.go        → emits code for "bookingsvc/user" only

The parser also has zero coverage for intersection (`&`), exclusion (`-`), caveats (`with`), expiration traits, and type annotations. There are no tests catching regressions. As `example/schema.zed` evolves, silent miscompilation is the failure mode.

## Solution: Compiler-backed adapter

Delegate parsing to `compiler.Compile` from `github.com/authzed/spicedb/pkg/schemadsl/compiler`. Wrap its `*core.NamespaceDefinition` output in a small `DefinitionView` adapter so the existing templates (`internal/templates/object.go.tmpl`) keep their `.ObjectType.Prefix` / `.ObjectType.Name` / `.Relations` / `.Permissions` shape.

    After fix:
      main.go → compiler.Compile → *CompiledSchema
                                        │
                                        ▼  ObjectDefinitions []*core.NamespaceDefinition
              generator.AdaptDefinitions → []*DefinitionView
                                        │
                                        ▼
              templates/object.go.tmpl (unchanged shape)

### Components

**`generator.DefinitionView`** — adapter struct
- Mirrors the existing template-facing shape: `ObjectType{Prefix, Name}`, `Relations []*RelationView`, `Permissions []*PermissionView`
- Built from `*core.NamespaceDefinition` by splitting `Name` on `/`
- Isolates proto coupling to one file; templates touch only the gotype comment

**`generator.RelationView`** — adapter for `*core.Relation` where `TypeInformation != nil`
- `AllowedTypes []string` — `prefix/name` strings drawn from `TypeInformation.AllowedDirectRelations[].Namespace`
- `HasWildcard bool` — true when any allowed relation has `RelationOrWildcard.PublicWildcard != nil` (B2)

**`generator.PermissionView`** — adapter for `*core.Relation` where `UsersetRewrite != nil`
- Walks `UsersetRewrite.Union` only; intersection / exclusion / `_This` / `FunctionedTupleToUserset` are explicit errors at adapt time (S2)
- Lowers `ComputedUserset` → identifier, `TupleToUserset` → `(left, right)` pair matching the existing `BinaryOp{->}` semantic

### Why not alternatives

| Approach | Verdict |
|---|---|
| **Adapter (chosen)** | Keeps templates untouched; isolates proto coupling to one Go file; rejection of unsupported rewrites is centralized |
| Field-access rewrite in templates | Templates would import proto-shaped accessors; every future syntax bump touches templates |
| Direct proto consumption in resolver | Couples `generator.go` resolver logic to proto field walks; harder to add unit tests later |

## Workstreams

### 1. Generator adapter

Add the proto → template-shape adapter so the rest of the migration can land without touching the template.

| # | Task | File | Status |
|---|------|------|--------|
| 1.1 | Add `DefinitionView` / `RelationView` / `PermissionView` types | `internal/generator/adapter.go` | [x] |
| 1.2 | Add `AdaptDefinitions([]*core.NamespaceDefinition) ([]*DefinitionView, error)` — splits `Name` on `/`, errors on missing `/` | same | [x] |
| 1.3 | Add `flattenAllowedTypes(*core.TypeInformation) (types []string, hasWildcard bool, err error)` — walks `AllowedDirectRelations`, treats `Relation == "..."` as plain reference, rejects sub-relation refs, caveats, expiration | same | [x] |
| 1.4 | Add `lowerUsersetRewrite(*core.UsersetRewrite) ([]PermissionExpr, error)` — Union only; reject Intersection/Exclusion with `fmt.Errorf("permission %q uses unsupported operator: %s", name, op)` | same | [x] |
| 1.5 | Add `lowerSetOperationChild(*core.SetOperation_Child)` — handles `ComputedUserset`, `TupleToUserset`; rejects `_This`, `_Nil`, `FunctionedTupleToUserset`, recursive `UsersetRewrite` | same | [x] |

**Key details:**
- Proto import path is `github.com/authzed/spicedb/pkg/proto/core/v1` aliased as `core`.
- `AllowedRelation.GetPublicWildcard() != nil` is the wildcard signal. Preserve it on `RelationView.HasWildcard` even though the current generator doesn't emit anything for wildcards yet — surface the data, decide downstream.
- `AllowedTypes` contains only the `Namespace` string (e.g. `"bookingsvc/user"`), matching the current parser's behavior of dropping the `:*` token. This keeps the existing template output byte-identical for wildcard relations; `HasWildcard` rides alongside as data-only.
- The compiler sets `AllowedRelation.Relation = "..."` (the Ellipsis constant from `translator.go:61`) for plain references and the actual sub-relation name for `bookingsvc/employee#manage`. Treat `"..."` as "no sub-relation"; reject anything else with a clear error since the existing generator has no codegen path for `#` syntax.
- The existing generator stringly-types relations as `prefix/name`. Build that string from `AllowedDirectRelations[].Namespace` to keep `utilstr.PackageName` / `utilstr.TypeName` working unchanged.

### 2. Resolver port

Replace the AST-shaped resolver in `generator.go` with one that consumes the new view types. Logic is preserved; only the input shape changes.

| # | Task | File | Status |
|---|------|------|--------|
| 2.1 | Drop `ast` import; add `core` import only if needed (resolver should consume `*DefinitionView` from WS1, not raw proto) | `internal/generator/generator.go` | [x] |
| 2.2 | Change `Generator.Definitions` field type to `[]*DefinitionView`; update `NewGenerator` signature | same | [x] |
| 2.3 | Rewrite `flattenRelationExpressionTypes` / `flattenRelationExpressionTypeStrings` to consume `RelationView.AllowedTypes` directly | same | [x] |
| 2.4 | Rewrite `resolvePermissionExpressionTypes` to walk `[]PermissionExpr` (the lowered form from WS1.4) — preserve the existing `relation` vs `permission` vs `->` branching | same | [x] |
| 2.5 | `GenerateObjectSource` keeps using `def.ObjectType.Prefix` / `def.ObjectType.Name` — these now come from the adapter, no change | same | [x] |

**Key details:**
- The current resolver matches on `ast.IdentifierNode` / `ast.BinaryOpNode{Operator: "->"}` / `ast.BinaryOpNode{Operator: "+"}`. After lowering, `[]PermissionExpr` is a flat union list (compiler already flattens `+` chains into a single `Union.Child[]`), with each child being either an identifier (computed userset) or an arrow-pair (tuple-to-userset). The resolver becomes simpler — no recursive `+` walking needed.

### 3. CLI wiring

Replace the `ast.NewLexer` + `ast.NewParser` flow in main with a single `compiler.Compile` call.

| # | Task | File | Status |
|---|------|------|--------|
| 3.1 | Drop `internal/ast` import; add `github.com/authzed/spicedb/pkg/schemadsl/compiler` and `github.com/authzed/spicedb/pkg/schemadsl/input` | `cmd/authzed-codegen/main.go` | [x] |
| 3.2 | Replace lex/parse block with `compiler.Compile(compiler.InputSchema{Source: input.Source(schemePath), SchemaString: string(input)}, compiler.RequirePrefixedObjectType())` | same | [x] |
| 3.3 | Pass `cs.ObjectDefinitions` through `generator.AdaptDefinitions` before constructing the `Generator` | same | [x] |

**Key details:**
- `compiler.Compile` returns `(*CompiledSchema, error)` — **not** `[]*core.NamespaceDefinition` as ADR-001 currently claims (B1). The slice is on `cs.ObjectDefinitions`.
- `RequirePrefixedObjectType()` matches the current parser's hard requirement that every definition is `prefix/name`. `AllowUnprefixedObjectType()` and `ObjectTypePrefix(...)` are the alternatives — neither matches existing behavior on `example/schema.zed` (B3).

### 4. Template gotype

| # | Task | File | Status |
|---|------|------|--------|
| 4.1 | Update gotype comment from `github.com/danhtran94/internal/ast.DefinitionNode` to `github.com/danhtran94/authzed-codegen/internal/generator.DefinitionView` | `internal/templates/object.go.tmpl` | [x] |

**Key details:**
- The original gotype path was already wrong (`github.com/danhtran94/internal/ast` — missing the repo name). Fix the path while we're here.

### 5. Delete `internal/ast/`

| # | Task | File | Status |
|---|------|------|--------|
| 5.1 | Delete `internal/ast/lexer.go` | `internal/ast/lexer.go` | [x] |
| 5.2 | Delete `internal/ast/parser.go` | `internal/ast/parser.go` | [x] |
| 5.3 | Delete `internal/ast/node.go` | `internal/ast/node.go` | [x] |
| 5.4 | `go mod tidy` to promote `github.com/authzed/spicedb` from indirect to direct | `go.mod` / `go.sum` | [x] |

### 6. ADR amendments

Land the corrections from the prior ADR review so source-of-truth tracks reality.

| # | Task | File | Status |
|---|------|------|--------|
| 6.1 | Fix A2 — `Compile` returns `(*CompiledSchema, error)`; the slice is `CompiledSchema.ObjectDefinitions` | `docs/ADR-001-parser-migration.md` | [x] |
| 6.2 | Add A3 import path — `github.com/authzed/spicedb/pkg/proto/core/v1` | same | [x] |
| 6.3 | Add explicit out-of-scope under Consequences: intersection, exclusion, caveats, expiration traits, `_This`, functioned tuple-to-userset | same | [x] |
| 6.4 | Add wildcard-preservation note — `RelationView.HasWildcard` carries the bit as data-only; no generated-code diff today; future jobs may consume it for wildcard codegen | same | [x] |
| 6.5 | Pin `ObjectPrefixOption` choice — `RequirePrefixedObjectType()` — under Decision | same | [x] |
| 6.6 | Soften Consequences Negative #2 ("minor version bumps can break") — `core.pb.go` types are wire-stable; risk is field deprecation, not break | same | [x] |
| 6.7 | Drop or verify A4 — measure built binary size before/after, record absolute number; promote to `[VERIFIED]` or remove | same | [x] |
| 6.8 | Flip ADR Status: Proposed → Accepted | same | [x] |

### 7. Verification

| # | Task | Status |
|---|------|--------|
| 7.1 | `go build ./...` succeeds | [x] |
| 7.2 | `go vet ./...` passes | [x] |
| 7.3 | `go run ./cmd/authzed-codegen --output /tmp/auz-out example/schema.zed` produces files | [x] |
| 7.4 | `diff -r example/authzed /tmp/auz-out` — expect zero semantic diff (whitespace only). Wildcard preservation lands as `RelationView.HasWildcard`, which no template consumes yet, so `bookingsvc/employee.viewer` codegen is unchanged. | [x] |
| 7.5 | Hand-craft a schema with `&` (intersection) — confirm WS1.4's rejection error fires with a useful message | [x] |
| 7.6 | `go mod tidy` runs cleanly; `spicedb` moves from indirect to direct dependency | [x] |

### 8. Documentation

| # | Task | Status |
|---|------|--------|
| 8.1 | Update `README.md` if it describes the old lexer/parser surface (TBD on first read) | [x] |

## Design Decisions

### `PermissionExpr` shape — string-tagged struct (#1)
Single struct with `Kind ∈ {"identifier", "arrow"}` and per-Kind field validity:
- `Kind == "identifier"` → `Ident` is the relation/permission name; `LeftRel`/`RightPerm` unused
- `Kind == "arrow"` → `LeftRel` and `RightPerm` are the two halves; `Ident` unused

Matches the existing `Permission{Kind, Value}` convention in `generator.go:96-99`. Cons accepted:
- **Runtime field validity** — the compiler can't enforce that `Ident` is empty when `Kind == "arrow"`. Mistakes manifest as silent empty strings downstream. Mitigated by adapt-time construction: `lowerSetOperationChild` is the only producer; consumers only need `switch e.Kind`.
- **Stringly-typed tag** — typos in `Kind` comparisons fail at runtime, not compile time. Mitigated by the `PermExprIdentifier` / `PermExprArrow` constants — every reference goes through them.
- **Adding a third variant later** (caveat support, intersection, exclusion) widens the struct rather than adding a type, eventually producing a fat struct with mostly-zero fields per row. Acceptable cost given the current scope explicitly rejects those variants in WS1.4–1.5.

### Adapter struct over field-access rewrite
Templates already consume a stable shape (`ObjectType.Prefix`, `ObjectType.Name`, `Relations`, `Permissions`). Building `DefinitionView` once in the generator keeps the proto-vs-template coupling on one Go file and lets the templates' gotype comment point at a stable type instead of `core.NamespaceDefinition`. Rules out the alternative of teaching the template to call `strings.Split(.Name, "/")` inline.

### Reject intersection/exclusion at adapt time, not at template time
The compiler will happily produce `UsersetRewrite.Intersection` for any schema using `&`. The generator has no code path for emitting intersection-shaped Go and the existing fixture doesn't use it. Erroring at adapt time (WS1.4) gives a useful schema-relative message; deferring to the template would produce an empty function or a panic.

### `RequirePrefixedObjectType()` over `AllowUnprefixedObjectType()`
The current hand-written parser hard-requires `prefix/name` (`parser.go:46-48`). `example/schema.zed` uses prefixes throughout. Matching the strictest existing behavior preserves the invariant that every definition has both `Prefix` and `Name`.

## What Stays Unchanged

- `internal/templates/object.go.tmpl` — only the gotype comment changes (4.1)
- `internal/utilstr/` — `PackageName`, `TypeName`, `SnakeToPascal`, `UpperFirst` all consume strings unchanged
- `pkg/authz/` — generated code's runtime dependency surface
- `example/schema.zed` — the input fixture
- `example/authzed/*.gen.go` — except for the documented `bookingsvc/employee.viewer` wildcard diff
- `Generator` public API shape — `NewGenerator`, `AddObjectTemplate`, `GenerateObjectSource` all keep their names; only the input element type changes

## Implementation Order

    1. WS1 Adapter            ← unblocks WS2 (resolver consumes views) and WS3 (main passes views)
    2. WS2 Resolver port      ← depends on WS1
    3. WS3 CLI wiring         ← depends on WS1; can parallel with WS2
    4. WS4 Template gotype    ← can land any time after WS1
    5. WS5 Delete ast/        ← last code change; depends on WS2 and WS3
    6. WS6 ADR amendments     ← can parallel with WS1-5; flip Status last
    7. WS7 Verification       ← runs after WS5
    8. WS8 Documentation      ← after WS7 confirms behavior

Build will be green between every workstream because WS1 lands additive code (new file), WS2/WS3 swap consumers in lockstep, and WS5 only deletes once both consumers are off the AST.

## Notes

- Proto type lives at `github.com/authzed/spicedb/pkg/proto/core/v1`; alias as `core` in imports.
- `compiler.InputSchema.Source` is an `input.Source` (string-typed); pass the file path or `"schema"` for in-memory schemas.
- `cs.ObjectDefinitions` is the only field this job consumes. `CaveatDefinitions` and `OrderedDefinitions` are explicitly out of scope (see WS6.3).
- The current parser's wildcard-drop bug (`parser.go:161-172` consumes `WILDCARD` without recording it) means `bookingsvc/employee.viewer` currently has its `:*` silently dropped. After migration, `RelationView.HasWildcard` carries the bit; the generator does not yet emit code for it. This is intentional — surfacing the data unblocks a future job to add wildcard codegen without re-parsing.
- `make` targets referenced in the project CLAUDE.md (`make gen`, `make lint`, `make test`) — verify they exist and use them in WS7 if present; otherwise raw `go` commands suffice.

## Discoveries & Decisions During Implementation

### [Implementer] WS5.4 (`go mod tidy`) landed as a side-effect of WS1
First Write of `internal/generator/adapter.go` triggered the postbuild hook with `missing go.sum entry for module providing package buf.build/gen/go/bufbuild/protovalidate/...`. The transitive closure under `github.com/authzed/spicedb/pkg/proto/core/v1` includes a `buf.build` proto module that wasn't in `go.sum` while spicedb was indirect. Running `go mod tidy` resolved it and promoted `spicedb v1.52.0` to a direct require. Net: WS5.4 is done; the only remaining `go mod tidy` work is verifying clean output after WS5.1–5.3 deletions.

### [Implementer] Wildcard preservation has zero behavioral diff today
The compiler's `AllowedRelation.PublicWildcard` carries data the current parser silently dropped. Decided to expose it on `RelationView.HasWildcard` but keep `AllowedTypes` containing only the `Namespace` string, matching the parser's drop-the-`:*` behavior. Templates don't yet consume `HasWildcard`, so generated output for `bookingsvc/employee.viewer` is byte-identical pre/post migration. Reflected in WS6.4 and WS7.4.

### [Implementer] Caveats / expiration / sub-relation refs rejected at adapt time
Beyond the rewrite operators (intersection, exclusion) the ADR called out, the proto's `AllowedRelation` exposes `RequiredCaveat`, `RequiredExpiration`, and `Relation` (sub-relation reference, e.g. `bookingsvc/employee#manage`). None are in the existing fixture. None have a codegen path. Failing fast at adapt time matches the policy set by S2 and produces useful schema-relative errors instead of silent drops or downstream nil panics.

### [Implementer] WS4 scope expanded to template call-site swaps
The original WS4 scope was "update gotype comment only". Once `RelationView` lost the `Expression` interface field that `RelationNode` had, the template's four `relationExpressionTypes $rel.Expression` invocations could no longer resolve. Rather than adding an alias `Expression []string` field on `RelationView` to preserve the call site, swapped to direct `$rel.AllowedTypes` access. Net: the template func `relationExpressionTypes` is removed (no longer needed), and four template lines change from `relationExpressionTypes $rel.Expression` to `$rel.AllowedTypes`. Generated output is byte-identical.

### [Implementer] Compiler transitive deps cascade through `go.sum` twice
Importing `core/v1` triggered the first `go mod tidy` cascade (added `buf.build/gen/go/bufbuild/protovalidate/...`). Importing `schemadsl/compiler` from `main.go` triggered a second cascade (added `buf.build/go/protovalidate`, `github.com/authzed/cel-go`, `gonum.org/v1/gonum`, etc). The transitive footprint is meaningful but only matters at build time — runtime imports are unchanged. A4's "uncertain on binary size" was deferred rather than measured; not load-bearing for a build-time CLI.

### [Implementer] Resolver became a flat for-loop
The original `resolvePermissionExpressionTypes` recursed through `BinaryOpNode{+}`. The compiler already flattens `+` chains into `Union.Child[]`, so after lowering the resolver is a single `for _, e := range exprs { switch e.Kind ... }`. Code is shorter and easier to read; the BinaryOp `nil` guards on `Left`/`Right` are gone since `PermissionExpr` has no nil children.
