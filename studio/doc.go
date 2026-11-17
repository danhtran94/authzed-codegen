// Package studio exposes the codegen's schema understanding to
// out-of-tree tooling (visual debuggers, editors, linters) at runtime,
// without generating or compiling Go code.
//
// EXPERIMENTAL: this package has no compatibility guarantee. Exported
// identifiers may change or disappear in any v2.x release, including
// patch releases. Pin an exact version if you depend on it.
//
// Boundaries (see docs/ADR-007-studio-experimental-package.md):
//   - studio may import internal/generator; it must not re-export
//     internal types. Every exported type is defined in this package.
//   - studio holds headless engine logic only — no UI, desktop-shell,
//     or database-connector code, and no dependencies beyond what the
//     root go.mod already requires.
//   - pkg/authz and generated code must not import studio.
package studio
