package user_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/testutil/fakes"
	"altalune.id/template/internal/user"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func noopUnexpected() apperror.UnexpectedFunc {
	return func(_ context.Context, message string, cause error, _ ...any) *apperror.AppError {
		return apperror.New(apperror.CodeUnexpectedError, message, 0).WithCause(cause)
	}
}

func TestEnsureFromOIDC_CreatesWhenMissing(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	claims := user.Claims{Issuer: "https://idp", Subject: "sub-1", Email: "alice@example.com", Name: "Alice"}
	u, err := s.EnsureFromOIDC(context.Background(), claims)
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", u.Email)
	assert.Equal(t, user.SourceOIDC, u.Source)
}

func TestEnsureFromOIDC_RefreshesExistingName(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	existing, err := user.New("alice@example.com", "Old Name", user.SourceOIDC)
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), existing))

	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	claims := user.Claims{Issuer: "iss", Subject: "sub", Email: "alice@example.com", Name: "New Name"}
	u, err := s.EnsureFromOIDC(context.Background(), claims)
	require.NoError(t, err)
	assert.Equal(t, "New Name", u.Name)
}

func TestEnsureFromOIDC_FallsBackToEmailLocalPartWhenNameEmpty(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	claims := user.Claims{Issuer: "https://idp", Subject: "sub-1", Email: "somebody@altalune.id", Name: ""}
	u, err := s.EnsureFromOIDC(context.Background(), claims)
	require.NoError(t, err)
	assert.Equal(t, "somebody", u.Name, "must synthesize name from email local part when IdP omits name")
}

func TestEnsureFromOIDC_RejectsMissingIssuer(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	_, err := s.EnsureFromOIDC(context.Background(), user.Claims{Subject: "sub", Email: "a@b.co", Name: "n"})
	assert.Error(t, err, "expected error for missing issuer")
}

func TestAcceptTerms_SetsTimestamp(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	existing, err := user.New("a@b.co", "n", user.SourceGenesis)
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), existing))

	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	require.NoError(t, s.AcceptTerms(context.Background(), existing.ID))

	got, err := store.ByID(context.Background(), existing.ID)
	require.NoError(t, err)
	require.NotNil(t, got.TermsAcceptedAt, "expected TermsAcceptedAt to be set")
}

func TestAcceptTerms_Idempotent(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	existing, err := user.New("a@b.co", "n", user.SourceGenesis)
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), existing))

	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	require.NoError(t, s.AcceptTerms(context.Background(), existing.ID))
	first, err := store.ByID(context.Background(), existing.ID)
	require.NoError(t, err)
	require.NotNil(t, first.TermsAcceptedAt)

	require.NoError(t, s.AcceptTerms(context.Background(), existing.ID))
	second, err := store.ByID(context.Background(), existing.ID)
	require.NoError(t, err)
	require.NotNil(t, second.TermsAcceptedAt)
	assert.True(t, first.TermsAcceptedAt.Equal(*second.TermsAcceptedAt), "second AcceptTerms clobbered the first stamp")
}

func TestAcceptTerms_NotFound(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	err := s.AcceptTerms(context.Background(), uuid.New())
	require.Error(t, err)
	assert.True(t, user.IsNotFoundError(err), "want IsNotFoundError, got %T: %v", err, err)
}

func TestRename_UpdatesName(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	existing, err := user.New("a@b.co", "old", user.SourceGenesis)
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), existing))

	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	require.NoError(t, s.Rename(context.Background(), existing.ID, "new"))
	got, err := store.ByID(context.Background(), existing.ID)
	require.NoError(t, err)
	assert.Equal(t, "new", got.Name)
}

func TestRename_InvalidName(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	existing, err := user.New("a@b.co", "old", user.SourceGenesis)
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), existing))

	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	err = s.Rename(context.Background(), existing.ID, "  ")
	require.Error(t, err)
	assert.True(t, user.IsInvalidNameError(err), "want IsInvalidNameError, got %T", err)
}

func TestRename_NotFound(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	err := s.Rename(context.Background(), uuid.New(), "new")
	require.Error(t, err)
	assert.True(t, user.IsNotFoundError(err), "want IsNotFoundError, got %T: %v", err, err)
}

type stubInviteFinder struct {
	has bool
	err error
}

func (s stubInviteFinder) HasPendingForEmail(_ context.Context, _ string) (bool, error) {
	return s.has, s.err
}

func TestCheckOIDCSignupEligibility_ExistingUser(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	existing, err := user.New("alice@example.com", "Alice", user.SourceOIDC)
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), existing))

	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	require.NoError(t, s.CheckOIDCSignupEligibility(context.Background(), "alice@example.com"))
}

func TestCheckOIDCSignupEligibility_PendingInvite(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected(),
		user.WithInviteFinder(stubInviteFinder{has: true}),
	)
	require.NoError(t, s.CheckOIDCSignupEligibility(context.Background(), "bob@example.com"))
}

func TestCheckOIDCSignupEligibility_NotInvited(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected(),
		user.WithInviteFinder(stubInviteFinder{}),
	)
	err := s.CheckOIDCSignupEligibility(context.Background(), "carol@example.com")
	require.Error(t, err)
	assert.True(t, user.IsNotInvitedError(err))
	assert.Equal(t, 0, store.Len(), "no user row must be persisted")
}

func TestCheckOIDCSignupEligibility_NoFinder(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	err := s.CheckOIDCSignupEligibility(context.Background(), "carol@example.com")
	require.Error(t, err)
	assert.True(t, user.IsNotInvitedError(err))
}

func TestAcceptTerms_RoutesUnexpected(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	sentinel := errors.New("boom")
	store.ByIDErr = sentinel
	store.StickyError = true
	var routed bool
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(),
		func(_ context.Context, _ string, cause error, _ ...any) *apperror.AppError {
			routed = true
			return apperror.New(apperror.CodeUnexpectedError, "unexpected", 0).WithCause(cause)
		})
	err := s.AcceptTerms(context.Background(), uuid.New())
	require.Error(t, err)
	assert.True(t, routed, "unexpected func was not invoked for driver failure")
}
