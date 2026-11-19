//go:build integration

// Package pgtest spins up ephemeral Postgres via testcontainers for integration tests.
package pgtest

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"altalune.id/opensheet/nanoid"
)

const (
	envDSN       = "TEST_PG_DSN"
	labelOwner   = "id.altalune.pgtest"
	staleAfter   = 30 * time.Minute
	schemaPrefix = "pgt_"
	schemaLen    = 16
)

//nolint:gochecknoglobals // one server per test binary; every handle gets its own schema on it.
var server struct {
	main bool
	once sync.Once
	ctr  testcontainers.Container
	dsn  string
	err  error
}

// Runner runs a test binary's tests and returns its exit code; *testing.M is one.
type Runner interface {
	Run() int
}

type wrapped struct{ next Runner }

// Wrap returns a Runner that runs next, then removes the container New started.
func Wrap(next Runner) Runner { return wrapped{next: next} }

func (w wrapped) Run() int {
	server.main = true
	if err := os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true"); err != nil {
		fmt.Fprintf(os.Stderr, "pgtest: disable ryuk: %v\n", err)
	}
	code := w.next.Run()
	if err := testcontainers.TerminateContainer(server.ctr); err != nil {
		fmt.Fprintf(os.Stderr, "pgtest: remove container: %v\n", err)
	}
	return code
}

// Main runs the tests, removes the container and its volumes, and exits with the tests' code; call it from TestMain.
func Main(m Runner) {
	os.Exit(Wrap(m).Run())
}

// Handle is one test's private schema on the shared Postgres server.
type Handle struct {
	DSN    string
	Schema string
}

// New returns a handle with a fresh schema, reusing TEST_PG_DSN when set; it skips when no container starts.
func New(t *testing.T) *Handle {
	t.Helper()
	if !server.main {
		t.Fatalf("pgtest: TestMain must call pgtest.Main(m)")
	}
	dsn := os.Getenv(envDSN)
	if dsn == "" {
		server.once.Do(func() { server.dsn, server.err = startContainer(t) })
		if server.err != nil {
			t.Skipf("pgtest: cannot start Postgres container (need a container runtime socket): %v", server.err)
		}
		dsn = server.dsn
	}
	return &Handle{DSN: dsn, Schema: uniqueSchema(t)}
}

// NOTE: Ryuk cannot boot on every container runtime (https://golang.testcontainers.org/system_requirements/using_podman/); Main removes the container and the label sweep removes one left by a killed test binary.
func startContainer(t *testing.T) (string, error) {
	t.Helper()
	sweepStale(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	c, err := postgres.Run(ctx, "postgres:18.7-alpine",
		postgres.WithDatabase("opensheet_test"),
		postgres.WithUsername("opensheet"),
		postgres.WithPassword("opensheet"),
		testcontainers.WithLabels(map[string]string{labelOwner: "true"}),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return "", fmt.Errorf("start postgres: %w", err)
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return "", fmt.Errorf("connection string: %w", err)
	}
	server.ctr = c
	return dsn, nil
}

func sweepStale(t *testing.T) {
	t.Helper()
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		t.Logf("pgtest: sweep: docker provider: %v", err)
		return
	}
	defer func() { _ = provider.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cli := provider.Client()
	found, err := cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", labelOwner+"=true"),
	})
	if err != nil {
		t.Logf("pgtest: sweep: container list: %v", err)
		return
	}

	cutoff := time.Now().Add(-staleAfter).Unix()
	for _, ctr := range found.Items {
		if ctr.Created > cutoff {
			continue
		}
		if _, rErr := cli.ContainerRemove(ctx, ctr.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); rErr != nil {
			t.Logf("pgtest: sweep: remove %s: %v", ctr.ID, rErr)
			continue
		}
		t.Logf("pgtest: sweep: removed stale container %s", ctr.ID)
	}
}

// DSNWithUser returns baseDSN with its credentials replaced by user and pass.
func DSNWithUser(t *testing.T, baseDSN, user, pass string) string {
	t.Helper()
	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("pgtest: parse DSN %q: %v", baseDSN, err)
	}
	u.User = url.UserPassword(user, pass)
	return u.String()
}

// OpenDB returns a *sql.DB whose search_path is pinned to the handle's own schema, dropped when the test ends. NOTE: isolating per handle instead of resetting the public schema is what lets packages run in parallel against one shared TEST_PG_DSN.
func (h *Handle) OpenDB(t *testing.T) *sql.DB {
	t.Helper()
	if h.Schema == "" {
		h.Schema = uniqueSchema(t)
	}
	connCfg, err := pgx.ParseConfig(h.DSN)
	if err != nil {
		t.Fatalf("pgtest: parse DSN: %v", err)
	}
	connCfg.RuntimeParams["search_path"] = h.Schema

	sqlDB := stdlib.OpenDB(*connCfg)
	if err := sqlDB.PingContext(t.Context()); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("pgtest: ping: %v", err)
	}

	quoted := pgx.Identifier{h.Schema}.Sanitize()
	if _, err := sqlDB.ExecContext(t.Context(), "CREATE SCHEMA IF NOT EXISTS "+quoted); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("pgtest: create schema %s: %v", h.Schema, err)
	}
	// NOTE: t.Context() is already canceled by the time cleanups run, so teardown needs its own context.
	t.Cleanup(func() {
		if _, dropErr := sqlDB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+quoted+" CASCADE"); dropErr != nil {
			t.Errorf("pgtest: leaked schema %s: %v", h.Schema, dropErr)
		}
		_ = sqlDB.Close()
	})
	return sqlDB
}

func uniqueSchema(t *testing.T) string {
	t.Helper()
	id, err := nanoid.New(schemaLen)
	if err != nil {
		t.Fatalf("pgtest: nanoid: %v", err)
	}
	return schemaPrefix + strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(id))
}
