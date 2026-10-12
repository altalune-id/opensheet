//go:build integration

package user_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/user"
)

func TestPostgres_User_ByIDP(t *testing.T) {
	store := newPostgresStoreForTest(t)
	ctx := t.Context()

	u, err := user.New("oidc@example.com", "OIDC", user.SourceOIDC)
	require.NoError(t, err)
	u.IDPIssuer = "https://idp.example"
	u.IDPSubject = "sub-42"
	require.NoError(t, store.Save(ctx, u))

	got, err := store.ByIDP(ctx, "https://idp.example", "sub-42")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
	assert.Equal(t, "https://idp.example", got.IDPIssuer)
	assert.Equal(t, "sub-42", got.IDPSubject)
	assert.Equal(t, user.SourceOIDC, got.Source)

	_, err = store.ByIDP(ctx, "https://idp.example", "nope")
	assert.True(t, user.IsNotFoundError(err), "want NotFoundError, got %v", err)
}

// NOTE: only a real Postgres insert proves the null is typed to the column and lands as NULL.
func TestPostgres_User_PasswordUsersWriteNullIDP(t *testing.T) {
	store := newPostgresStoreForTest(t)
	ctx := t.Context()

	for _, email := range []string{"local1@example.com", "local2@example.com"} {
		u, err := user.New(email, "Local", user.SourceLocal)
		require.NoError(t, err)
		u.PasswordHash = "$argon2id$stub"
		require.NoError(t, store.Save(ctx, u), "a second password user must not collide on users_idp_idx")

		got, err := store.ByID(ctx, u.ID)
		require.NoError(t, err)
		assert.Empty(t, got.IDPIssuer)
		assert.Empty(t, got.IDPSubject)
	}
}

func TestPostgres_User_DuplicateIDPReportsIDPField(t *testing.T) {
	store := newPostgresStoreForTest(t)
	ctx := t.Context()

	first, err := user.New("first@example.com", "First", user.SourceOIDC)
	require.NoError(t, err)
	first.IDPIssuer = "https://idp.example"
	first.IDPSubject = "sub-1"
	require.NoError(t, store.Save(ctx, first))

	second, err := user.New("second@example.com", "Second", user.SourceOIDC)
	require.NoError(t, err)
	second.IDPIssuer = "https://idp.example"
	second.IDPSubject = "sub-1"

	var dup *user.AlreadyExistsError
	require.ErrorAs(t, store.Save(ctx, second), &dup)
	assert.Equal(t, "idp_subject", dup.Field, "the idp index must be told apart from the email index")
	assert.Equal(t, "sub-1", dup.Value)
}

// SECURITY: the regression the IdP-first lookup order prevents — email-first breaks login when the IdP address changes.
func TestPostgres_EnsureFromOIDC_EmailChangedAtIDP(t *testing.T) {
	store := newPostgresStoreForTest(t)
	svc := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	ctx := t.Context()

	first, err := svc.EnsureFromOIDC(ctx, user.Claims{Issuer: "https://idp", Subject: "sub-1", Email: "old@x.id", Name: "A"})
	require.NoError(t, err)

	second, err := svc.EnsureFromOIDC(ctx, user.Claims{Issuer: "https://idp", Subject: "sub-1", Email: "new@x.id", Name: "A"})
	require.NoError(t, err, "a changed IdP email must resolve via users_idp_idx, not collide on it")
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, "new@x.id", second.Email)

	byIDP, err := store.ByIDP(ctx, "https://idp", "sub-1")
	require.NoError(t, err)
	assert.Equal(t, first.ID, byIDP.ID)
	assert.Equal(t, "new@x.id", byIDP.Email)

	_, err = store.ByEmail(ctx, "old@x.id")
	assert.True(t, user.IsNotFoundError(err), "the old address must no longer resolve, got %v", err)
}
