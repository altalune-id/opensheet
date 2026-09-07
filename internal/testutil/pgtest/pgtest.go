//go:build integration

// Package pgtest spins up ephemeral Postgres via testcontainers for integration tests.
package pgtest

import (
	"context"
	"database/sql"
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

	"altalune.id/template/nanoid"
)

const (
	envDSN       = "TEST_PG_DSN"
	labelOwner   = "id.altalune.pgtest"
	staleAfter   = 30 * time.Minute
	schemaPrefix = "pgt_"
	schemaLen    = 16
)

//nolint:gochecknoglobals // the stale-container sweep runs once per test binary, not once per test.
var sweepOnce sync.Once

// Handle wraps an ephemeral or shared Postgres instance for a test.
type Handle struct {
	DSN       string
	Schema    string
	container testcontainers.Container
}

// Close terminates the underlying container (no-op when reusing an external Postgres via TEST_PG_DSN).
func (h *Handle) Close() error {
	if h == nil || h.container == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return h.container.Terminate(ctx)
}

// New returns a fresh Postgres for the test, reusing TEST_PG_DSN when that is set.
// NOTE: Ryuk cannot boot on macOS+podman without a rootful privileged machine
// (https://golang.testcontainers.org/system_requirements/using_podman/), and t.Cleanup does not run
// on timeout or SIGINT — so containers are labelled and stale ones are swept here instead.
func New(t *testing.T) *Handle {
	t.Helper()
	if dsn := os.Getenv(envDSN); dsn != "" {
		return &Handle{DSN: dsn, Schema: uniqueSchema(t)}
	}

	t.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	sweepOnce.Do(func() { sweepStale(t) })

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	c, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("altempl_test"),
		postgres.WithUsername("altempl"),
		postgres.WithPassword("altempl"),
		testcontainers.WithLabels(map[string]string{labelOwner: "true"}),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("pgtest: cannot start Postgres container (need docker or podman socket): %v", err)
		return nil
	}

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = c.Terminate(ctx)
		t.Fatalf("pgtest: connection string: %v", err)
	}

	h := &Handle{DSN: dsn, Schema: uniqueSchema(t), container: c}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Logf("pgtest: terminate: %v", err)
		}
	})
	return h
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

// OpenDB returns a *sql.DB whose search_path is pinned to the handle's own schema, dropped when the test ends.
// NOTE: isolating per handle instead of resetting the public schema is what lets packages run in parallel against one shared TEST_PG_DSN.
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
