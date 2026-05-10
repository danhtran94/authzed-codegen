package templates

import (
	_ "embed"
)

//go:embed object.go.tmpl
var ObjectTemplate []byte

//go:embed schema.go.tmpl
var SchemaTemplate []byte

//go:embed opa.go.tmpl
var OPATemplate []byte
