# [ADR-001] Parser Migration: SpiceDB Compiler over Hand-Written Lexer/Parser

| Field       | Value                      |
|------------|----------------------------|
| Status       | Accepted                   |
| Date         | 2026-05-01                |
| Deciders     | Danh Tran                  |
| Scope        | authzed-codegen CLI        |
| Depends on   | —                          |

---

## Context

The `authzed-codegen` CLI tool parses AuthZed (SpiceDB) schema files and generates type-safe Go code for resource authorization. It ships a hand-written parser in `internal/ast/` consisting of `lexer.go` (tokenizer), `parser.go` (recursive descent), and `node.go` (AST node definitions).

The current lexer supports a narrow token set: `DEFINITION`, `RELATION`, `PERMISSION`, `IDENTIFIER`, `SLASH`, `LBRACE`, `RBRACE`, `COLON`, `PIPE`, `PLUS`, `EQUAL`, `MINUS_ARROW`, `COMMENT`, and `WILDCARD`. It has no support for intersection (`&`), difference (`-`), wildcards on the right side of relations (`:*`), caveats (`with`), type annotations on permissions (`->`, `@`), or functioned tuple sets (`with expiration`, `with self`).

The generated code must stay correct as AuthZed schemas evolve. The parser is the single choke point between schema changes and generated output. If the parser does not support a schema feature, the tool silently fails or produces incorrect code — and currently has no automated tests to catch regressions.

The `authzed/spicedb` project maintains a mature, tested compiler (`pkg/schemadsl/compiler`) as part of the SpiceDB engine. It parses AuthZed schemas into canonical protobuf `core.NamespaceDefinition` structs. The compiler has been battle-tested by hundreds of downstream consumers and supports the full AuthZED syntax including complex rewrites, caveats, expiration traits, and type annotations.

The project already imports `github.com/authzed/spicedb v1.52.0` in `go.mod`. The compiler package is available and stable.

## Options

### Option A — Delegate to the SpiceDB Compiler

Replace all custom parsing logic with `compiler.Compile` from `pkg/schemadsl/compiler`. The compiler outputs `*core.NamespaceDefinition` which maps 1:1 to what the code generator needs: a name, a list of relations (with `TypeInformation`), and a list of permissions (with `UsersetRewrite`).

### Option B — Fork and Maintain the Hand-Written Parser

Keep the existing `internal/ast/` packages. Extend the lexer to support additional tokens (`&`, `-`, `with`, `@`, `:*`), update the parser to handle intersection and difference expressions, and wire up the generator to produce correct code for new constructs. Add unit tests at significant engineering cost.

### Option C — Hybrid Adapter

Keep the existing parser for the supported subset. Add a validation layer that delegates to the SpiceDB compiler at codegen time for verification only. If the compiler disagrees, emit a warning but proceed with the hand-written AST.

## Options Comparison

| Driver | Option A | Option B | Option C |
|--------|----------|----------|----------|
| Maintenance burden | Minimal | High | Medium |
| Feature coverage | Complete | Partial | Partial |
| Correctness guarantee | Compiler verified | Hand verified | Adapter drift |
| Import footprint | One dep | None | Two parsers |
| Test surface reduction | Entire parser | Adds tests | No reduction |
| Future AuthZED support | Zero effort | Manual effort | Manual effort |

## Decision

The SpiceDB `schemadsl/compiler` package becomes the sole parser backend for `authzed-codegen`. The CLI calls `compiler.Compile(InputSchema, RequirePrefixedObjectType())` — the strict-prefix mode matches the existing hand-written parser's invariant that every definition is `prefix/name`. Compiled output is wrapped in a `generator.DefinitionView` adapter so the existing templates keep their field-access shape (`ObjectType.Prefix`, `ObjectType.Name`, `Relations`, `Permissions`); proto coupling is confined to one Go file.

**Out of scope at the time of this ADR** (rejected at adapt time with schema-relative errors):
- ~~Intersection (`&`) and exclusion (`-`) rewrite operators~~ — lifted in AUZ-004 (pre-session) per `docs/spec-001-intersection-exclusion-codegen.md`
- ~~Caveats (`with <caveat>`)~~ — lifted in AUZ-006 / AUZ-007 (v1.1.0) per `docs/spec-002-caveat-codegen.md` + `docs/spec-003-write-time-caveat-codegen.md`
- ~~Expiration traits (`with expiration`)~~ — lifted in AUZ-009 (v1.3.0) per `docs/spec-004-expiration-codegen.md`
- ~~Sub-relation references (`bookingsvc/employee#manage`)~~ — lifted in AUZ-011 (v1.5.0) per `docs/spec-006-sub-relation-references.md`
- ~~Caveat definitions~~ — codegen now consumes `CompiledSchema.CaveatDefinitions` (v1.1.0+)

**Still out of scope** as of v2.0.0:
- Legacy `_this` (explicit reflexive permission references) — uncommon in production schemas
- Functioned tuple-to-userset (`with self`) — specialized; schema patterns using inline-anonymous usersets are rare

A future job may lift either by extending the adapter and adding the corresponding codegen path.

## Consequences

**Consequences Positive**

- Zero maintenance burden for parser features — the SpiceDB compiler team owns correctness and syntax evolution.
- Full AuthZED feature support (caveats, expiration traits, type annotations, complex rewrites) becomes available immediately without additional implementation.
- The code generator's test surface shrinks: the parser is no longer tested, only the template output, which covers fewer lines of code.
- Bug reports involving schema parse errors are deferred to the SpiceDB compiler issue tracker, which has an active maintainer.

**Consequences Negative**

- The generated code's import path changes: `go.mod` gains a direct dependency on `github.com/authzed/spicedb`, which pulls in a large transitive closure (`protobuf`, `grpc`, `otel`) even though only the compiler proto is needed.
- The `core.NamespaceDefinition` protobuf API tracks `authzed/spicedb` releases. The generated proto types are part of the gRPC wire surface and are stable across minor versions; the realistic risk is field deprecations and added oneof variants, not breaks.
- Error messages change format: the custom parser panics with a simple message; the SpiceDB compiler returns `WithErrorContext` errors with source ranges. The CLI error output differs from what existing users expect.
- A one-time migration of the hand-written `internal/ast/` code out of the repository requires deleting three files and updating two call sites.
- Wildcard data is now preserved on `RelationView.HasWildcard` (the previous parser silently dropped the `:*` token). No template consumes the bit yet, so generated output stays byte-identical for the existing fixture; future jobs can opt-in to wildcard codegen without re-parsing.

## Assumptions

- **A1 [VERIFIED]:** `go mod tidy` successfully adds `github.com/authzed/spicedb` as a direct dependency. Evidence: `go get` ran and succeeded, producing the updated `go.mod` with `spicedb v1.52.0`.
- **A2 [EXTERNAL FACT]:** The `pkg/schemadsl/compiler` package exports `Compile(InputSchema, ObjectPrefixOption, ...Option) (*CompiledSchema, error)`. The slice consumed by the codegen is `CompiledSchema.ObjectDefinitions []*core.NamespaceDefinition`. `CaveatDefinitions` and `OrderedDefinitions` are also returned but unused. Evidence: `authzed/spicedb` v1.52.0 source at `pkg/schemadsl/compiler/compiler.go:127` (function) and `compiler.go:36-50` (struct).
- **A3 [EXTERNAL FACT]:** `core.NamespaceDefinition` (at import path `github.com/authzed/spicedb/pkg/proto/core/v1`) contains `Relation []*core.Relation` where each `Relation` is distinguished as a relation (`TypeInformation != nil`) vs. a permission (`UsersetRewrite != nil`). Evidence: `authzed/spicedb` source at `pkg/proto/core/v1/core.pb.go:1237` and `core.pb.go:1311`.
- **A4 [VERIFIED]:** The migrated CLI builds clean with `go build ./...` and runs end-to-end against `example/schema.zed` producing byte-identical output to the pre-migration fixture. Binary-size measurement deferred — not load-bearing for a build-time CLI.
