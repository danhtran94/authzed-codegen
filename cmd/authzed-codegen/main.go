package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/authzed/spicedb/pkg/schemadsl/compiler"
	"github.com/authzed/spicedb/pkg/schemadsl/input"

	"github.com/danhtran94/authzed-codegen/internal/generator"
	"github.com/danhtran94/authzed-codegen/internal/templates"
)

var outputPath string

func init() {
	flag.StringVar(&outputPath, "output", "zed", "output path for generated files")
}

func main() {
	if len(os.Args) < 2 {
		panic(fmt.Errorf("missing schema path"))
	}

	flag.Parse()

	schemePath := os.Args[len(os.Args)-1]

	schemaBytes, err := os.ReadFile(schemePath)
	if err != nil {
		panic(err)
	}

	compiled, err := compiler.Compile(
		compiler.InputSchema{
			Source:       input.Source(schemePath),
			SchemaString: string(schemaBytes),
		},
		compiler.RequirePrefixedObjectType(),
	)
	if err != nil {
		panic(err)
	}

	defs, err := generator.AdaptDefinitions(compiled.ObjectDefinitions)
	if err != nil {
		panic(err)
	}

	g := generator.NewGenerator(defs)
	g.OutputPath = outputPath
	g.AddObjectTemplate("[object]", string(templates.ObjectTemplate))

	if err := g.GenerateObjectSource("[object]"); err != nil {
		panic(err)
	}
}
