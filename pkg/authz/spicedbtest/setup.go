// Package spicedbtest spins up an ephemeral SpiceDB Docker container,
// loads a schema, and exposes a ready-to-use spicedb.Engine for tests.
//
// Each `go test` invocation runs a separate package binary, so each
// test package's TestMain calls Start to launch its own container.
// Container names are auto-generated to avoid cross-package collisions
// when packages run in parallel under `go test ./...`.
package spicedbtest

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	authzed "github.com/authzed/authzed-go/v1"
	"github.com/authzed/grpcutil"
	"github.com/danhtran94/authzed-codegen/pkg/authz/spicedb"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ErrDockerUnavailable is returned by Start when no Docker daemon
// is reachable. Callers should treat this as a skip signal rather
// than a hard failure.
var ErrDockerUnavailable = errors.New("docker not available")

const presharedKey = "test-secret-key"

// Sandbox owns a running SpiceDB container, an authzed-go client,
// and the spicedb.Engine wrapper. Close terminates the container.
type Sandbox struct {
	Engine    *spicedb.Engine
	Addr      string
	terminate func(context.Context) error
}

// Close stops the underlying container. Safe to call from defer.
func (s *Sandbox) Close(ctx context.Context) error {
	if s == nil || s.terminate == nil {
		return nil
	}
	return s.terminate(ctx)
}

// Start launches a SpiceDB container, writes the given schema via the
// SchemaService.WriteSchema RPC, and returns a Sandbox wrapping a
// spicedb.Engine bound to the container. Returns ErrDockerUnavailable
// when no docker binary is on PATH so callers can call t.Skip.
func Start(ctx context.Context, schemaSDL string) (*Sandbox, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, ErrDockerUnavailable
	}

	container, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: tc.ContainerRequest{
			Image:      "ghcr.io/authzed/spicedb:latest",
			Entrypoint: []string{"spicedb"},
			Cmd: []string{
				"serve", "datastore", "memory",
				"--grpc-preshared-key", presharedKey,
			},
			ExposedPorts: []string{"50051/tcp"},
			WaitingFor: wait.ForAll(
				wait.ForListeningPort("50051/tcp").SkipInternalCheck(),
				wait.ForLog("grpc server started serving").WithStartupTimeout(60*time.Second),
			).WithDeadline(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		return nil, fmt.Errorf("start spicedb container: %w", err)
	}

	cleanup := func(c context.Context) error { return container.Terminate(c) }

	portCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	mappedPort, err := container.MappedPort(portCtx, "50051/tcp")
	if err != nil {
		_ = cleanup(ctx)
		return nil, fmt.Errorf("map spicedb port: %w", err)
	}
	addr := "localhost:" + mappedPort.Port()

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpcutil.WithInsecureBearerToken(presharedKey),
	}
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		_ = cleanup(ctx)
		return nil, fmt.Errorf("dial spicedb: %w", err)
	}

	schemaClient := v1.NewSchemaServiceClient(conn)
	if err := writeSchemaWithRetry(ctx, schemaClient, schemaSDL, 5, 2*time.Second); err != nil {
		_ = conn.Close()
		_ = cleanup(ctx)
		return nil, fmt.Errorf("write schema: %w", err)
	}
	_ = conn.Close()

	authzedClient, err := authzed.NewClient(addr, opts...)
	if err != nil {
		_ = cleanup(ctx)
		return nil, fmt.Errorf("create authzed client: %w", err)
	}

	return &Sandbox{
		Engine:    spicedb.NewEngine(authzedClient, 3*time.Second),
		Addr:      addr,
		terminate: cleanup,
	}, nil
}

func writeSchemaWithRetry(ctx context.Context, c v1.SchemaServiceClient, sdl string, attempts int, delay time.Duration) error {
	var lastErr error
	for range attempts {
		_, err := c.WriteSchema(ctx, &v1.WriteSchemaRequest{Schema: sdl})
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(delay)
	}
	return lastErr
}
