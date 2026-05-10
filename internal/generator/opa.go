package generator

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/template"

	"github.com/danhtran94/authzed-codegen/internal/utilstr"
)

// OPAPackageView is the data passed to opa.go.tmpl. One per Go package
// (i.e. per ObjectType.Prefix). Definitions are sorted by ObjectType.Name
// then Permissions within each definition are sorted by Name — together
// they pin a deterministic registration order in the generated
// RegisterSpiceDBBuiltins body, satisfying SPEC-013 C1.
type OPAPackageView struct {
	PackageName string
	Definitions []*DefinitionView
}

// GenerateOPASource emits opa.gen.go for each per-package group of
// DefinitionViews. Groups are formed by ObjectType.Prefix (matching the
// existing per-namespace generation directory layout). Within each
// package the definitions and their permissions are sorted alphabetically
// for round-trip determinism per SPEC-013 C1.
//
// Triggered when the CLI is invoked with --emit-opa. Writes
// <OutputPath>/<package>/opa.gen.go per package.
func (g *Generator) GenerateOPASource(tmplStr string) error {
	groups := groupByPackage(g.Definitions)

	mapFuncs := template.FuncMap{
		"snakeName": strings.ToLower,
	}

	tmpl, err := template.New("opa").Funcs(mapFuncs).Parse(tmplStr)
	if err != nil {
		return err
	}

	for pkgName, defs := range groups {
		view := OPAPackageView{
			PackageName: pkgName,
			Definitions: defs,
		}

		buf := bytes.Buffer{}
		if _, err := buf.WriteString(SOURCE_HEADER); err != nil {
			return err
		}
		if err := tmpl.Execute(&buf, view); err != nil {
			return fmt.Errorf("execute opa template for %s: %w", pkgName, err)
		}

		dir := fmt.Sprintf("%s/%s", g.OutputPath, pkgName)
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			return err
		}
		if err := os.WriteFile(fmt.Sprintf("%s/opa.gen.go", dir), buf.Bytes(), os.ModePerm); err != nil {
			return err
		}
	}

	return nil
}

// groupByPackage buckets DefinitionViews by ObjectType.Prefix and sorts
// each bucket's definitions by Name. Each definition's Permissions slice
// is also sorted in place by Name. The result feeds the per-package
// template execution so RegisterSpiceDBBuiltins emits its rego.Function*
// stanzas in alphabetical (Resource, Permission) order — required for
// the round-trip regeneration check (SPEC-013 C1, scope SC7).
func groupByPackage(defs []*DefinitionView) map[string][]*DefinitionView {
	groups := map[string][]*DefinitionView{}
	for _, d := range defs {
		pkg := utilstr.PackageName(d.ObjectType.Prefix)
		groups[pkg] = append(groups[pkg], d)
	}

	for _, list := range groups {
		sort.Slice(list, func(i, j int) bool {
			return list[i].ObjectType.Name < list[j].ObjectType.Name
		})
		for _, d := range list {
			sort.Slice(d.Permissions, func(i, j int) bool {
				return d.Permissions[i].Name < d.Permissions[j].Name
			})
		}
	}

	return groups
}
