package generator

import (
	"os"
	"testing"

	"github.com/authzed/spicedb/pkg/schemadsl/compiler"
	"github.com/authzed/spicedb/pkg/schemadsl/input"
	"github.com/stretchr/testify/require"
)

const fixtureSchemaPath = "../../example/schema.zed"

// adaptFixtureSchema compiles example/schema.zed exactly as
// cmd/authzed-codegen does and returns the adapted views.
func adaptFixtureSchema(t *testing.T) ([]*DefinitionView, map[string][]ParamSpec) {
	t.Helper()
	src, err := os.ReadFile(fixtureSchemaPath)
	require.NoError(t, err)
	return adaptSchema(t, fixtureSchemaPath, string(src))
}

// adaptSchema compiles a schema string the way cmd/authzed-codegen does.
func adaptSchema(t *testing.T, name, src string) ([]*DefinitionView, map[string][]ParamSpec) {
	t.Helper()
	compiled, err := compiler.Compile(
		compiler.InputSchema{Source: input.Source(name), SchemaString: src},
		compiler.RequirePrefixedObjectType(),
	)
	require.NoError(t, err)
	defs, caveatMap, err := AdaptDefinitions(compiled.CaveatDefinitions, compiled.ObjectDefinitions)
	require.NoError(t, err)
	return defs, caveatMap
}
