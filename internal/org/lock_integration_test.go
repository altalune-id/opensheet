//go:build integration

package org_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/schema"
)

const lockRounds = 10

type lockFixture struct {
	svc   *org.Service
	store org.Store
	orgID uuid.UUID
	a, b  uuid.UUID
}

func newLockFixture(t *testing.T) *lockFixture {
	t.Helper()
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)
	cfg := config.Defaults()
	cfg.DB.Driver = "postgres"
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))

	dbCfg := db.DBConfig{Driver: db.DriverPostgres, Schema: h.Schema, TablePrefix: cfg.DB.TablePrefix}
	pool, pc := db.Pool{W: sqlDB, R: sqlDB}, tenant.NewPgConn(sqlDB)
	store := org.NewStore(dbCfg, pool, pc)
	unexpected := func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		return apperror.New("opensheet.unexpected", msg, codes.Internal, &apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(cause)
	}
	f := &lockFixture{
		svc:   org.NewService(store, capabilities.Capabilities{}, slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected, tenant.NewUnitOfWork(dbCfg, pool, pc)),
		store: store,
		a:     uuid.New(),
		b:     uuid.New(),
	}
	now := time.Now().UTC()
	for _, id := range []uuid.UUID{f.a, f.b} {
		_, err := sqlDB.ExecContext(t.Context(),
			"INSERT INTO "+cfg.DB.TablePrefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, $2, '', '', false, $3, $3)",
			id, id.String()+"@x.co", now)
		require.NoError(t, err)
	}
	o, err := org.NewOrg("race", "Race", f.a)
	require.NoError(t, err)
	f.orgID = o.ID
	require.NoError(t, store.Save(tenant.WithOrg(t.Context(), f.orgID), o))
	return f
}

func (f *lockFixture) bothOwners(t *testing.T) {
	t.Helper()
	ctx := tenant.WithOrg(t.Context(), f.orgID)
	for _, id := range []uuid.UUID{f.a, f.b} {
		m, err := org.NewMembership(f.orgID, id, org.RoleOwner)
		require.NoError(t, err)
		require.NoError(t, f.store.SaveMembership(ctx, m))
	}
}

func (f *lockFixture) owners(t *testing.T) int {
	t.Helper()
	ms, err := f.store.ListMembers(tenant.WithOrg(t.Context(), f.orgID), f.orgID)
	require.NoError(t, err)
	n := 0
	for _, m := range ms {
		if m.Role == org.RoleOwner {
			n++
		}
	}
	return n
}

func race(first, second func() error) [2]error {
	var wg sync.WaitGroup
	var errs [2]error
	start := make(chan struct{})
	wg.Go(func() { <-start; errs[0] = first() })
	wg.Go(func() { <-start; errs[1] = second() })
	close(start)
	wg.Wait()
	return errs
}

func requireOneWinner(t *testing.T, errs [2]error) {
	t.Helper()
	failed := 0
	for _, err := range errs {
		if err != nil {
			refused := authz.IsLastOwnerError(err) || authz.IsEscalationError(err) || org.IsOwnerRemovalError(err) || authz.IsDeniedError(err)
			require.True(t, refused, "the loser is refused as the last owner or as a demoted or removed actor, got %T: %v", err, err)
			failed++
		}
	}
	require.Equal(t, 1, failed, "exactly one change succeeds")
}

func TestOrg_SetRole_TwoOwnersDemotedConcurrently(t *testing.T) {
	f := newLockFixture(t)
	for range lockRounds {
		f.bothOwners(t)
		asA, asB := boundAs(t, f.orgID, f.a, org.RoleOwner), boundAs(t, f.orgID, f.b, org.RoleOwner)
		errs := race(
			func() error { return f.svc.SetRole(asA, f.orgID, f.b, org.RoleAdmin, uuid.Nil) },
			func() error { return f.svc.SetRole(asB, f.orgID, f.a, org.RoleAdmin, uuid.Nil) },
		)
		requireOneWinner(t, errs)
		require.Equal(t, 1, f.owners(t), "one owner remains")
	}
}

func TestOrg_RemoveLastOwnerRacingDemotion(t *testing.T) {
	f := newLockFixture(t)
	for range lockRounds {
		f.bothOwners(t)
		asA, asB := boundAs(t, f.orgID, f.a, org.RoleOwner), boundAs(t, f.orgID, f.b, org.RoleOwner)
		errs := race(
			func() error { return f.svc.RemoveMember(asA, f.orgID, f.b) },
			func() error { return f.svc.SetRole(asB, f.orgID, f.a, org.RoleAdmin, uuid.Nil) },
		)
		requireOneWinner(t, errs)
		require.Equal(t, 1, f.owners(t), "one owner remains")
	}
}

func TestPostgres_SetRoleAndRemoveMemberLockUnderRLS(t *testing.T) {
	f := newDefinerFixture(t)
	svc := newDefinerService(t, f)
	ids := f.seedOwners(t, 2)
	a, b := ids[0], ids[1]
	o, err := svc.Create(context.Background(), org.CreateRequest{Slug: "rls-" + f.slug, Name: "RLS", OwnerID: a})
	require.NoError(t, err)
	scoped := tenant.WithOrg(t.Context(), o.ID)
	m, err := org.NewMembership(o.ID, b, org.RoleOwner)
	require.NoError(t, err)
	require.NoError(t, f.store.SaveMembership(scoped, m))
	asA := boundAs(t, o.ID, a, org.RoleOwner)

	require.NoError(t, svc.SetRole(asA, o.ID, b, org.RoleAdmin, uuid.Nil))
	got, err := f.store.MembershipOf(scoped, o.ID, b)
	require.NoError(t, err)
	require.Equal(t, org.RoleAdmin, got.Role)

	require.NoError(t, svc.RemoveMember(asA, o.ID, b))
	_, err = f.store.MembershipOf(scoped, o.ID, b)
	require.True(t, org.IsMembershipMissingError(err) || org.IsNotFoundError(err), "want the membership gone, got %T: %v", err, err)
}
