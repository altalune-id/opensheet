package user_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pdb "altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/user"
)

func TestSQLiteStore_ByIDP(t *testing.T) {
	t.Parallel()
	db, dbcfg := openMemSQLite(t)
	store := user.NewStore(dbcfg, pdb.Pool{W: db, R: db})
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

// NOTE: users_idp_idx is partial on idp_issuer IS NOT NULL, so "" instead of NULL collides every password user.
func TestSQLiteStore_PasswordUsersWriteNullIDP(t *testing.T) {
	t.Parallel()
	db, dbcfg := openMemSQLite(t)
	store := user.NewStore(dbcfg, pdb.Pool{W: db, R: db})
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

func TestSQLiteStore_DuplicateIDPReportsIDPField(t *testing.T) {
	t.Parallel()
	db, dbcfg := openMemSQLite(t)
	store := user.NewStore(dbcfg, pdb.Pool{W: db, R: db})
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
	assert.Equal(t, "idp_subject", dup.Field)
	assert.Equal(t, "sub-1", dup.Value)
}

// SECURITY: the regression the IdP-first lookup order prevents — email-first breaks login when the IdP address changes.
func TestSQLiteStore_EnsureFromOIDC_EmailChangedAtIDP(t *testing.T) {
	t.Parallel()
	db, dbcfg := openMemSQLite(t)
	store := user.NewStore(dbcfg, pdb.Pool{W: db, R: db})
	svc := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	ctx := t.Context()

	first, err := svc.EnsureFromOIDC(ctx, user.Claims{Issuer: "https://idp", Subject: "sub-1", Email: "old@x.id", EmailVerified: true, Name: "A"})
	require.NoError(t, err)

	second, err := svc.EnsureFromOIDC(ctx, user.Claims{Issuer: "https://idp", Subject: "sub-1", Email: "new@x.id", EmailVerified: true, Name: "A"})
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

func TestEnsureFromOIDC_StoresAndBackfillsIDP(t *testing.T) {
	t.Parallel()
	st := fakes.NewUser()
	svc := user.NewService(st, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	ctx := context.Background()

	u, err := svc.EnsureFromOIDC(ctx, user.Claims{Issuer: "https://idp", Subject: "sub-1", Email: "a@x.id", EmailVerified: true, Name: "A"})
	require.NoError(t, err)
	require.Equal(t, "https://idp", u.IDPIssuer)
	require.Equal(t, "sub-1", u.IDPSubject)
	got, err := st.ByIDP(ctx, "https://idp", "sub-1")
	require.NoError(t, err)
	require.Equal(t, u.ID, got.ID)

	legacy, err := user.New("b@x.id", "B", user.SourceOIDC)
	require.NoError(t, err)
	require.NoError(t, st.Save(ctx, legacy))
	u2, err := svc.EnsureFromOIDC(ctx, user.Claims{Issuer: "https://idp", Subject: "sub-2", Email: "b@x.id", EmailVerified: true, Name: "B"})
	require.NoError(t, err)
	require.Equal(t, legacy.ID, u2.ID)
	require.Equal(t, "https://idp", u2.IDPIssuer)
	require.Equal(t, "sub-2", u2.IDPSubject)

	backfilled, err := st.ByIDP(ctx, "https://idp", "sub-2")
	require.NoError(t, err)
	require.Equal(t, legacy.ID, backfilled.ID)
}

func TestEnsureFromOIDC_UnknownSubjectIsNotFound(t *testing.T) {
	t.Parallel()
	st := fakes.NewUser()
	_, err := st.ByIDP(context.Background(), "https://idp", "missing")
	require.True(t, user.IsNotFoundError(err), "want NotFoundError, got %v", err)
}

func TestSQLiteStore_EnsureFromOIDC_RelinksAVerifiedEmailFromAnotherIssuer(t *testing.T) {
	t.Parallel()
	db, dbcfg := openMemSQLite(t)
	store := user.NewStore(dbcfg, pdb.Pool{W: db, R: db})
	svc := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	ctx := t.Context()

	old, err := svc.EnsureFromOIDC(ctx, user.Claims{Issuer: "https://old-idp", Subject: "old-sub", Email: "ana@x.id", EmailVerified: true, Name: "Ana"})
	require.NoError(t, err)

	got, err := svc.EnsureFromOIDC(ctx, user.Claims{Issuer: "https://new-idp", Subject: "new-sub", Email: "ana@x.id", EmailVerified: true, Name: "Ana"})
	require.NoError(t, err)
	assert.Equal(t, old.ID, got.ID, "a verified email keeps the account across an issuer switch")

	byNew, err := store.ByIDP(ctx, "https://new-idp", "new-sub")
	require.NoError(t, err, "bearer tokens from the new issuer must resolve the user")
	assert.Equal(t, old.ID, byNew.ID)
	_, err = store.ByIDP(ctx, "https://old-idp", "old-sub")
	assert.True(t, user.IsNotFoundError(err), "the old link must be gone, got %v", err)
}

func TestEnsureFromOIDC_RefusesAnUnverifiedEmailForAnUnknownSubject(t *testing.T) {
	t.Parallel()
	st := fakes.NewUser()
	svc := user.NewService(st, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	ctx := context.Background()

	victim, err := svc.EnsureFromOIDC(ctx, user.Claims{Issuer: "https://idp", Subject: "sub-1", Email: "ana@x.id", EmailVerified: true, Name: "Ana"})
	require.NoError(t, err)

	_, err = svc.EnsureFromOIDC(ctx, user.Claims{Issuer: "https://idp", Subject: "attacker", Email: "ana@x.id", Name: "Eve"})
	require.True(t, user.IsEmailUnverifiedError(err), "want *EmailUnverifiedError, got %v", err)
	still, err := st.ByIDP(ctx, "https://idp", "sub-1")
	require.NoError(t, err)
	assert.Equal(t, victim.ID, still.ID, "an unverified email must not move the link")

	_, err = svc.EnsureFromOIDC(ctx, user.Claims{Issuer: "https://idp", Subject: "fresh", Email: "new@x.id", Name: "New"})
	require.True(t, user.IsEmailUnverifiedError(err), "want *EmailUnverifiedError for a new user, got %v", err)
	_, err = st.ByEmail(ctx, "new@x.id")
	assert.True(t, user.IsNotFoundError(err), "no user is created from an unverified email, got %v", err)
}

func TestEnsureFromOIDC_SubjectWinsOverEmail(t *testing.T) {
	t.Parallel()
	st := fakes.NewUser()
	svc := user.NewService(st, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	ctx := context.Background()

	a, err := svc.EnsureFromOIDC(ctx, user.Claims{Issuer: "https://idp", Subject: "sub-a", Email: "a@x.id", EmailVerified: true, Name: "A"})
	require.NoError(t, err)
	b, err := svc.EnsureFromOIDC(ctx, user.Claims{Issuer: "https://idp", Subject: "sub-b", Email: "b@x.id", EmailVerified: true, Name: "B"})
	require.NoError(t, err)

	got, err := svc.EnsureFromOIDC(ctx, user.Claims{Issuer: "https://idp", Subject: "sub-a", Email: "b@x.id", Name: "A"})
	require.NoError(t, err, "a known subject signs in even with an unverified email")
	assert.Equal(t, a.ID, got.ID, "the subject decides the account, never the email")
	assert.Equal(t, "a@x.id", got.Email, "an unverified email never replaces the stored one")
	other, err := st.ByIDP(ctx, "https://idp", "sub-b")
	require.NoError(t, err)
	assert.Equal(t, b.ID, other.ID)
}

func TestEnsureFromOIDC_RefusesAVerifiedEmailLinkedToAnotherSubjectAtTheSameIssuer(t *testing.T) {
	t.Parallel()
	st := fakes.NewUser()
	svc := user.NewService(st, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	ctx := context.Background()

	owner, err := svc.EnsureFromOIDC(ctx, user.Claims{Issuer: "https://idp", Subject: "sub-1", Email: "ana@x.id", EmailVerified: true, Name: "Ana"})
	require.NoError(t, err)

	_, err = svc.EnsureFromOIDC(ctx, user.Claims{Issuer: "https://idp", Subject: "sub-2", Email: "ana@x.id", EmailVerified: true, Name: "Other"})
	require.True(t, user.IsIdentityConflictError(err), "want *IdentityConflictError, got %v", err)
	still, err := st.ByIDP(ctx, "https://idp", "sub-1")
	require.NoError(t, err)
	assert.Equal(t, owner.ID, still.ID, "another subject at the same issuer must not take the account")
}
