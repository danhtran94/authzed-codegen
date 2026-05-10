// Command opa-embed is a runnable demo of the all-embedded deployment
// shape from RFC-001: one Go binary composing SpiceDB (via testcontainers),
// OPA's runtime (runtime.NewRuntime — the standard OPA server), and the
// generated SpiceDB builtins from AUZ-019/AUZ-021, serving policy decisions
// over OPA's standard HTTP API.
//
// Run (from the repo root — the demo reads example/schema.zed and the
// policy dir by relative path):
//
//	go run ./example/opa-embed            # binds :8181, brings up SpiceDB via Docker
//	go run ./example/opa-embed --port 9000
//
// Query (after the demo seeds a few relationships at startup):
//
//	curl -s -X POST localhost:8181/v1/data/authz/allow \
//	  -d '{"input":{"user":{"id":"alice","role":"viewer"},"resource":{"id":"demo-folder"}}}'
//	# => {"result":true}   (alice is a viewer of demo-folder — the ReBAC leg)
//
//	curl -s -X POST localhost:8181/v1/data/authz/allow \
//	  -d '{"input":{"user":{"id":"carol","role":"admin"},"resource":{"id":"demo-folder"}}}'
//	# => {"result":true}   (carol has the admin role — the RBAC leg)
//
//	curl -s -X POST localhost:8181/v1/data/authz/allow \
//	  -d '{"input":{"user":{"id":"banned-user","role":"admin"},"resource":{"id":"demo-folder"}}}'
//	# => {"result":false}  (deny override beats even the admin role)
//
//	curl -s localhost:8181/v1/policies          # → the loaded policy.rego
//	curl -s -o /dev/null -w "%{http_code}\n" localhost:8181/health  # → 200
//
// The SpiceDB builtins are registered into OPA's process-global registry
// via RegisterSpiceDBBuiltinsGlobal BEFORE runtime.NewRuntime, so OPA's
// standard /v1/data evaluation resolves them. Docker is required: SpiceDB
// runs in a testcontainer (MemDB datastore — state lost on restart).
// Local-only; not production-hardened.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/open-policy-agent/opa/v1/runtime"

	bookingsvc "github.com/danhtran94/authzed-codegen/example/authzed/bookingsvc"
	extsvc "github.com/danhtran94/authzed-codegen/example/authzed/extsvc"
	menusvc "github.com/danhtran94/authzed-codegen/example/authzed/menusvc"
	"github.com/danhtran94/authzed-codegen/pkg/authz"
	"github.com/danhtran94/authzed-codegen/pkg/authz/spicedbtest"
)

const (
	schemaPath = "example/schema.zed"
	policyDir  = "example/opa-embed/policy"
)

func main() {
	port := flag.Int("port", 8181, "HTTP listen port for OPA's standard server")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		fatalf("read schema %s: %v (run from the repo root)", schemaPath, err)
	}

	fmt.Println("starting embedded SpiceDB via testcontainers (Docker required)…")
	sb, err := spicedbtest.Start(ctx, string(schema))
	if err != nil {
		if errors.Is(err, spicedbtest.ErrDockerUnavailable) {
			fmt.Println("Docker is not available — this demo needs Docker to run SpiceDB. Exiting.")
			return
		}
		fatalf("start SpiceDB sandbox: %v", err)
	}
	defer func() { _ = sb.Close(context.Background()) }()
	authz.SetDefaultEngine(sb.Engine)

	if err := seed(ctx); err != nil {
		fatalf("seed relationships: %v", err)
	}
	fmt.Println("seeded demo relationships: extsvc/folder:demo-folder#viewer@extsvc/user:{alice,bob}")

	// Register the SpiceDB-backed builtins into OPA's PROCESS-GLOBAL
	// registry — this MUST happen before runtime.NewRuntime, which builds
	// its compiler (and thus reads the global builtin universe) at
	// construction time. Per-instance options (SpiceDBBuiltins(...) →
	// []func(*rego.Rego)) would be invisible to runtime.NewRuntime; the
	// global form (RegisterSpiceDBBuiltinsGlobal) is what it picks up.
	// Call each once.
	extsvc.RegisterSpiceDBBuiltinsGlobal(sb.Engine, ctx)
	bookingsvc.RegisterSpiceDBBuiltinsGlobal(sb.Engine, ctx)
	menusvc.RegisterSpiceDBBuiltinsGlobal(sb.Engine, ctx)

	addr := fmt.Sprintf(":%d", *port)
	addrs := []string{addr}
	diagAddrs := []string{}
	rt, err := runtime.NewRuntime(ctx, runtime.Params{
		Addrs:                  &addrs,
		DiagnosticAddrs:        &diagAddrs,
		Paths:                  []string{policyDir}, // loads policy.rego → data.authz
		GracefulShutdownPeriod: 5,
		EnableVersionCheck:     false, // no phone-home for a demo
	})
	if err != nil {
		fatalf("build OPA runtime: %v", err)
	}

	fmt.Printf("OPA server listening on %s  (POST /v1/data/authz/allow, GET /v1/policies, GET /health)\n", addr)
	if err := rt.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fatalf("OPA server: %v", err)
	}
	fmt.Println("shut down")
}

// seed writes a couple of demo relationships through the generated
// Create<Rel>Relations methods so the README's curl examples have data
// to match against. Schema is already applied by spicedbtest.Start.
func seed(ctx context.Context) error {
	folder := extsvc.Folder("demo-folder")
	if err := folder.CreateViewerRelations(ctx, extsvc.FolderViewerObjects{
		User: []extsvc.User{"alice", "bob"},
	}); err != nil {
		return fmt.Errorf("create viewer relations: %w", err)
	}
	return nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
