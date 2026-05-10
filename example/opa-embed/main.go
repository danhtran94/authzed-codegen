// Command opa-embed is a runnable demo of the all-embedded deployment
// shape from RFC-001: one Go binary composing SpiceDB (via testcontainers),
// the OPA Rego runtime, and the generated SpiceDBBuiltins from AUZ-019,
// with an HTTP endpoint serving policy decisions.
//
// Run:
//
//	go run ./example/opa-embed            # binds :8181, brings up SpiceDB via Docker
//	go run ./example/opa-embed --port 9000
//
// Query (after the demo seeds a few relationships at startup):
//
//	curl -s -X POST localhost:8181/v1/data/authz/allow \
//	  -d '{"input":{"user":{"id":"alice","role":"viewer"},"resource":{"id":"demo-folder"}}}'
//	# => {"result":true}   (alice is a viewer of demo-folder)
//
//	curl -s -X POST localhost:8181/v1/data/authz/allow \
//	  -d '{"input":{"user":{"id":"banned-user","role":"admin"},"resource":{"id":"demo-folder"}}}'
//	# => {"result":false}  (deny override wins even over the admin role)
//
// The SpiceDB-backed policy eval goes through this program's own HTTP
// handler (not OPA's standard runtime) because OPA's runtime.NewRuntime
// has no hook for per-instance custom builtins — see AUZ-020 Discoveries.
// Docker is required: SpiceDB runs in a testcontainer. State is lost on
// restart (MemDB datastore). Local-only; not production-hardened.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	_ "embed"

	"github.com/open-policy-agent/opa/v1/rego"

	bookingsvc "github.com/danhtran94/authzed-codegen/example/authzed/bookingsvc"
	extsvc "github.com/danhtran94/authzed-codegen/example/authzed/extsvc"
	menusvc "github.com/danhtran94/authzed-codegen/example/authzed/menusvc"
	"github.com/danhtran94/authzed-codegen/pkg/authz"
	"github.com/danhtran94/authzed-codegen/pkg/authz/spicedbtest"
)

//go:embed policy/policy.rego
var policyModule string

const schemaPath = "example/schema.zed"

func main() {
	port := flag.Int("port", 8181, "HTTP listen port for the policy-decision endpoint")
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
	fmt.Println("seeded demo relationships: extsvc/folder:demo-folder#viewer@extsvc/user:alice (+ bob)")

	// Build the SpiceDB-backed Rego builtin options once. The closures
	// capture this long-lived ctx (Pattern P2 from SPEC-013); fine for a
	// demo. The same options are reused across many rego.New calls below.
	builtinOpts := make([]func(*rego.Rego), 0, 8)
	builtinOpts = append(builtinOpts, extsvc.SpiceDBBuiltins(sb.Engine, ctx)...)
	builtinOpts = append(builtinOpts, bookingsvc.SpiceDBBuiltins(sb.Engine, ctx)...)
	builtinOpts = append(builtinOpts, menusvc.SpiceDBBuiltins(sb.Engine, ctx)...)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /v1/data/authz/allow", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input map[string]any `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, fmt.Sprintf("bad request body: %v", err), http.StatusBadRequest)
			return
		}

		opts := make([]func(*rego.Rego), 0, len(builtinOpts)+3)
		opts = append(opts, builtinOpts...)
		opts = append(opts,
			rego.Query("data.authz.allow"),
			rego.Module("policy.rego", policyModule),
			rego.Input(body.Input),
		)
		rs, err := rego.New(opts...).Eval(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("policy eval: %v", err), http.StatusInternalServerError)
			return
		}

		result := false
		if len(rs) > 0 && len(rs[0].Expressions) > 0 {
			if b, ok := rs[0].Expressions[0].Value.(bool); ok {
				result = b
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	})

	srv := &http.Server{Addr: fmt.Sprintf(":%d", *port), Handler: mux}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	fmt.Printf("policy-decision endpoint listening on :%d  (POST /v1/data/authz/allow, GET /health)\n", *port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatalf("http server: %v", err)
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
