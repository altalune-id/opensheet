package user_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/testutil/fakes"
	"altalune.id/template/internal/user"
)

func newGenesisService(t *testing.T, store *fakes.User, email string) *user.Service {
	t.Helper()
	return user.NewService(store, user.GenesisConfig{Email: email}, newTestLogger(), noopUnexpected())
}

func seedGenesisUser(t *testing.T, store *fakes.User, email string, admin bool) *user.User {
	t.Helper()
	u, err := user.New(email, "Seed", user.SourceOIDC)
	require.NoError(t, err)
	u.IsAdmin = admin
	require.NoError(t, store.Save(context.Background(), u))
	return u
}

func TestReconcileGenesisAdmin(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		configured  string
		seedEmail   string
		seedAdmin   bool
		wantOutcome user.Outcome
		wantAdmin   bool
	}{
		{"unconfigured", "", "", false, user.OutcomeUnconfigured, false},
		{"no matching user", "root@x.co", "", false, user.OutcomeUnclaimed, false},
		{"matching user promoted", "root@x.co", "root@x.co", false, user.OutcomeClaimed, true},
		{"already admin", "root@x.co", "root@x.co", true, user.OutcomeSatisfied, true},
		{"different user untouched", "root@x.co", "other@x.co", false, user.OutcomeUnclaimed, false},
		{"configured address is normalized", "  Root@X.Co  ", "root@x.co", false, user.OutcomeClaimed, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := fakes.NewUser()
			var seeded *user.User
			if tc.seedEmail != "" {
				seeded = seedGenesisUser(t, store, tc.seedEmail, tc.seedAdmin)
			}
			svc := newGenesisService(t, store, tc.configured)

			got, err := svc.ReconcileGenesisAdmin(context.Background())
			require.NoError(t, err)
			require.Equal(t, tc.wantOutcome, got)

			if seeded == nil {
				return
			}
			after, err := store.ByID(context.Background(), seeded.ID)
			require.NoError(t, err)
			require.Equal(t, tc.wantAdmin, after.IsAdmin)
		})
	}
}

func TestReconcileGenesisAdmin_WritesNothingWhenUnclaimed(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	svc := newGenesisService(t, store, "root@x.co")

	got, err := svc.ReconcileGenesisAdmin(context.Background())
	require.NoError(t, err)
	require.Equal(t, user.OutcomeUnclaimed, got)
	require.Zero(t, store.Len(), "an unclaimed genesis address must not create a user")
}

func TestReconcileGenesisAdmin_WritesNothingWhenAnotherUserExists(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	other := seedGenesisUser(t, store, "other@x.co", false)
	svc := newGenesisService(t, store, "root@x.co")

	got, err := svc.ReconcileGenesisAdmin(context.Background())
	require.NoError(t, err)
	require.Equal(t, user.OutcomeUnclaimed, got)
	require.Equal(t, 1, store.Len(), "an unclaimed genesis address must not create a user")

	after, err := store.ByID(context.Background(), other.ID)
	require.NoError(t, err)
	require.False(t, after.IsAdmin, "an unrelated user must not be promoted")
}

func TestReconcileGenesisAdmin_IsIdempotent(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	seeded := seedGenesisUser(t, store, "root@x.co", false)
	svc := newGenesisService(t, store, "root@x.co")

	first, err := svc.ReconcileGenesisAdmin(context.Background())
	require.NoError(t, err)
	require.Equal(t, user.OutcomeClaimed, first)

	second, err := svc.ReconcileGenesisAdmin(context.Background())
	require.NoError(t, err)
	require.Equal(t, user.OutcomeSatisfied, second, "a second pass must not re-promote")

	require.Equal(t, 1, store.Len())
	after, err := store.ByID(context.Background(), seeded.ID)
	require.NoError(t, err)
	require.True(t, after.IsAdmin)
}

func TestReconcileGenesisAdmin_LookupFailureIsReported(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	store.ByEmailErr = errors.New("boom")
	store.StickyError = true
	svc := newGenesisService(t, store, "root@x.co")

	got, err := svc.ReconcileGenesisAdmin(context.Background())
	require.Error(t, err)
	require.Empty(t, got)
	require.Zero(t, store.Len())
}
