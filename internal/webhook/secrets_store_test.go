package webhook_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/events"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/webhook"
)

func assertSaveKeepsStoredSecrets(t *testing.T, store webhook.Store, tc tenant.Context) {
	t.Helper()
	ctx := tenant.Into(t.Context(), tc)
	e := newSealedEndpoint(t, tc)
	require.NoError(t, store.Save(ctx, e))
	stale, err := store.ByID(ctx, e.ID)
	require.NoError(t, err)

	rotated := webhook.SealedSecrets{Primary: []byte("new-primary"), Secondary: []byte("old-primary")}
	require.NoError(t, store.SaveSecrets(ctx, e.ID, e.Secrets, rotated))

	updateEndpoint(t, stale, "https://example.org/v2", "renamed", []events.Type{events.PostUnpublished}, false)
	require.NoError(t, store.Save(ctx, stale), "an update from a copy loaded before the rotation")

	got, err := store.ByID(ctx, e.ID)
	require.NoError(t, err)
	assert.Equal(t, rotated, got.Secrets, "Save must never revert a rotated secret")
	assert.Equal(t, "https://example.org/v2", got.URL)
	assert.Equal(t, "renamed", got.Description)
	assert.False(t, got.Active)
}

func assertSaveSecretsRotatesAndClearsSecondary(t *testing.T, store webhook.Store, tc tenant.Context) {
	t.Helper()
	ctx := tenant.Into(t.Context(), tc)
	e := newSealedEndpoint(t, tc)
	require.NoError(t, store.Save(ctx, e))

	rotated := webhook.SealedSecrets{Primary: []byte("new-primary"), Secondary: []byte("old-primary")}
	require.NoError(t, store.SaveSecrets(ctx, e.ID, e.Secrets, rotated))
	got, err := store.ByID(ctx, e.ID)
	require.NoError(t, err)
	assert.Equal(t, rotated, got.Secrets)
	assert.False(t, got.UpdatedAt.Before(e.UpdatedAt), "updated_at moves forward")

	retired := webhook.SealedSecrets{Primary: rotated.Primary}
	require.NoError(t, store.SaveSecrets(ctx, e.ID, rotated, retired))
	got, err = store.ByID(ctx, e.ID)
	require.NoError(t, err)
	assert.Nil(t, got.Secrets.Secondary, "a nil secondary must clear the column")
	assert.Equal(t, []byte("new-primary"), got.Secrets.Primary)

	empty := webhook.SealedSecrets{Primary: []byte("third-primary"), Secondary: []byte{}}
	require.NoError(t, store.SaveSecrets(ctx, e.ID, retired, empty), "an empty secondary must not trip the length CHECK")
	got, err = store.ByID(ctx, e.ID)
	require.NoError(t, err)
	assert.Nil(t, got.Secrets.Secondary)
}

func assertSaveSecretsRefusesAStaleExpected(t *testing.T, store webhook.Store, tc tenant.Context) {
	t.Helper()
	ctx := tenant.Into(t.Context(), tc)
	e := newSealedEndpoint(t, tc)
	require.NoError(t, store.Save(ctx, e))

	rotated := webhook.SealedSecrets{Primary: []byte("new-primary"), Secondary: []byte("old-primary")}
	require.NoError(t, store.SaveSecrets(ctx, e.ID, e.Secrets, rotated))

	err := store.SaveSecrets(ctx, e.ID, e.Secrets, webhook.SealedSecrets{Primary: []byte("racer-primary")})
	assert.True(t, webhook.IsSecretConflictError(err), "a stale primary: got %T: %v", err, err)

	stalePrimary := webhook.SealedSecrets{Primary: []byte("other-primary"), Secondary: rotated.Secondary}
	err = store.SaveSecrets(ctx, e.ID, stalePrimary, webhook.SealedSecrets{Primary: []byte("racer-primary")})
	assert.True(t, webhook.IsSecretConflictError(err), "a stale primary with a matching secondary: got %T: %v", err, err)

	staleSecondary := webhook.SealedSecrets{Primary: rotated.Primary, Secondary: []byte("other-secondary")}
	err = store.SaveSecrets(ctx, e.ID, staleSecondary, webhook.SealedSecrets{Primary: rotated.Primary})
	assert.True(t, webhook.IsSecretConflictError(err), "a stale secondary: got %T: %v", err, err)

	noSecondary := webhook.SealedSecrets{Primary: rotated.Primary}
	err = store.SaveSecrets(ctx, e.ID, noSecondary, webhook.SealedSecrets{Primary: []byte("racer-primary")})
	assert.True(t, webhook.IsSecretConflictError(err), "a nil expected secondary against a stored one: got %T: %v", err, err)

	got, err := store.ByID(ctx, e.ID)
	require.NoError(t, err)
	assert.Equal(t, rotated, got.Secrets, "a refused write must leave the stored secrets unchanged")

	err = store.SaveSecrets(ctx, uuid.New(), e.Secrets, rotated)
	assert.True(t, webhook.IsNotFoundError(err), "a missing endpoint: got %T: %v", err, err)
}

func assertSaveSecretsOfAnotherOrgIsNotFound(t *testing.T, store webhook.Store, a, b tenant.Context) {
	t.Helper()
	ownerCtx := tenant.Into(t.Context(), a)
	victim := newSealedEndpoint(t, a)
	require.NoError(t, store.Save(ownerCtx, victim))

	err := store.SaveSecrets(tenant.Into(t.Context(), b), victim.ID, victim.Secrets,
		webhook.SealedSecrets{Primary: []byte("attacker-primary")})
	assert.True(t, webhook.IsNotFoundError(err), "org B must not rewrite org A's secrets, got %T: %v", err, err)

	got, err := store.ByID(ownerCtx, victim.ID)
	require.NoError(t, err)
	assert.Equal(t, victim.Secrets, got.Secrets, "org A's secrets must be untouched")
}

func TestSQLite_SaveKeepsStoredSecrets(t *testing.T) {
	store, _, tc := newSQLiteStore(t)
	assertSaveKeepsStoredSecrets(t, store, tc)
}

func TestSQLite_SaveSecretsRotatesAndClearsSecondary(t *testing.T) {
	store, _, tc := newSQLiteStore(t)
	assertSaveSecretsRotatesAndClearsSecondary(t, store, tc)
}

func TestSQLite_SaveSecretsRefusesAStaleExpected(t *testing.T) {
	store, _, tc := newSQLiteStore(t)
	assertSaveSecretsRefusesAStaleExpected(t, store, tc)
}

func TestSQLite_Hijack_SaveSecretsOfAnotherOrgIsNotFound(t *testing.T) {
	store, sqlDB, a := newSQLiteStore(t)
	assertSaveSecretsOfAnotherOrgIsNotFound(t, store, a, seedTenant(t, sqlDB))
}
