package webhook_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/webhook"
)

func TestSQLite_Hijack_ByIDOfAnotherOrgIsNotFound(t *testing.T) {
	store, sqlDB, a := newSQLiteStore(t)
	b := seedTenant(t, sqlDB)

	victim := newSealedEndpoint(t, a)
	require.NoError(t, store.Save(tenant.Into(t.Context(), a), victim))

	_, err := store.ByID(tenant.Into(t.Context(), b), victim.ID)
	assert.True(t, webhook.IsNotFoundError(err), "org B must not read org A's endpoint, got %T: %v", err, err)
}

func TestSQLite_Hijack_ListNamingAnotherOrgIsEmpty(t *testing.T) {
	store, sqlDB, a := newSQLiteStore(t)
	b := seedTenant(t, sqlDB)
	require.NoError(t, store.Save(tenant.Into(t.Context(), a), newSealedEndpoint(t, a)))

	got, err := store.List(tenant.Into(t.Context(), b), a.OrgID, a.ProjectID)
	require.NoError(t, err)
	assert.Empty(t, got, "naming org A's scope from org B's context must list nothing")
}

func TestSQLite_Hijack_SaveOntoAnotherOrgsRowIsNotFound(t *testing.T) {
	t.Run("reskinned with the caller's own scope but another org's row id", func(t *testing.T) {
		store, sqlDB, a := newSQLiteStore(t)
		b := seedTenant(t, sqlDB)
		ownerCtx := tenant.Into(t.Context(), a)
		otherCtx := tenant.Into(t.Context(), b)

		victim := newSealedEndpoint(t, a)
		require.NoError(t, store.Save(ownerCtx, victim))

		// NOTE: org B posts its own scope with a row id it does not own, so only the conflict clause's org predicate can catch it.
		hijack := newSealedEndpoint(t, b)
		hijack.ID = victim.ID
		hijack.URL = "https://attacker.example/steal"

		err := store.Save(otherCtx, hijack)
		assert.True(t, webhook.IsNotFoundError(err), "got %T: %v", err, err)

		got, err := store.ByID(ownerCtx, victim.ID)
		require.NoError(t, err)
		assert.Equal(t, validURL, got.URL, "org A's endpoint must be untouched")
		assert.Equal(t, a.OrgID, got.OrgID)

		listed, err := store.List(otherCtx, b.OrgID, b.ProjectID)
		require.NoError(t, err)
		assert.Empty(t, listed, "the refused upsert must not have landed in org B either")
	})

	t.Run("a fresh row naming another org", func(t *testing.T) {
		store, sqlDB, a := newSQLiteStore(t)
		b := seedTenant(t, sqlDB)

		planted := newSealedEndpoint(t, a)
		err := store.Save(tenant.Into(t.Context(), b), planted)
		assert.True(t, webhook.IsNotFoundError(err), "got %T: %v", err, err)

		_, err = store.ByID(tenant.Into(t.Context(), a), planted.ID)
		assert.True(t, webhook.IsNotFoundError(err), "org B must not plant an endpoint in org A")
	})
}

func TestSQLite_Hijack_DeleteOfAnotherOrgsRowIsNotFound(t *testing.T) {
	store, sqlDB, a := newSQLiteStore(t)
	b := seedTenant(t, sqlDB)
	ownerCtx := tenant.Into(t.Context(), a)

	victim := newSealedEndpoint(t, a)
	require.NoError(t, store.Save(ownerCtx, victim))

	err := store.Delete(tenant.Into(t.Context(), b), victim.ID)
	assert.True(t, webhook.IsNotFoundError(err), "got %T: %v", err, err)

	_, err = store.ByID(ownerCtx, victim.ID)
	require.NoError(t, err, "org A's endpoint must survive org B's delete")
}

func TestSQLite_Hijack_ListAttemptsOfAnotherOrgIsEmpty(t *testing.T) {
	store, sqlDB, a := newSQLiteStore(t)
	b := seedTenant(t, sqlDB)
	ownerCtx := tenant.Into(t.Context(), a)

	e := newSealedEndpoint(t, a)
	require.NoError(t, store.Save(ownerCtx, e))
	delivery := uuid.New()
	require.NoError(t, store.SaveAttempt(ownerCtx, newAttempt(a, e.ID, delivery, 1, time.Now())))

	got, err := store.ListAttempts(tenant.Into(t.Context(), b), e.ID, delivery)
	require.NoError(t, err)
	assert.Empty(t, got, "org B must not read org A's attempts")
}
