package user_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/testutil/fakes"
	"altalune.id/template/internal/user"
)

func TestService_Create_HappyPath(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	got, err := s.Create(context.Background(), user.CreateRequest{Email: "alice@example.com", Name: "Alice", Source: user.SourceLocal, Password: "secret12"})
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", got.Email)
	assert.NotEmpty(t, got.PasswordHash)
}

func TestService_Create_InvalidEmail(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	_, err := s.Create(context.Background(), user.CreateRequest{Email: "not-email", Name: "N", Source: user.SourceLocal})
	require.Error(t, err)
	assert.True(t, user.IsInvalidEmailError(err))
}

func TestService_Create_DuplicateEmail(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	_, err := s.Create(context.Background(), user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceLocal})
	require.NoError(t, err)
	_, err = s.Create(context.Background(), user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceLocal})
	require.Error(t, err)
	assert.True(t, user.IsAlreadyExistsError(err), "expected AlreadyExistsError, got %T", err)
}

func TestService_Create_SaveErrorRoutesUnexpected(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	store.SaveErr = errors.New("boom")
	store.StickyError = true
	routed := false
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(),
		func(_ context.Context, _ string, cause error, _ ...any) *apperror.AppError {
			routed = true
			return apperror.New(apperror.CodeUnexpectedError, "unexpected", 0).WithCause(cause)
		})
	_, err := s.Create(context.Background(), user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceLocal})
	require.Error(t, err)
	assert.True(t, routed)
}

func TestService_Promote_Grants(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	u, err := user.New("a@b.co", "A", user.SourceLocal)
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), u))

	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	require.NoError(t, s.Promote(context.Background(), u.ID))
	got, err := store.ByID(context.Background(), u.ID)
	require.NoError(t, err)
	assert.True(t, got.IsAdmin)
}

func TestService_Promote_Idempotent(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	u, err := user.New("a@b.co", "A", user.SourceLocal)
	require.NoError(t, err)
	u.IsAdmin = true
	require.NoError(t, store.Save(context.Background(), u))

	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	require.NoError(t, s.Promote(context.Background(), u.ID))
}

func TestService_Promote_NotFound(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	err := s.Promote(context.Background(), uuid.New())
	require.Error(t, err)
	assert.True(t, user.IsNotFoundError(err))
}

func TestService_HasLocalUsers(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())

	has, err := s.HasLocalUsers(context.Background())
	require.NoError(t, err)
	assert.False(t, has)

	_, err = s.Create(context.Background(), user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceLocal, Password: "abcdefgh"})
	require.NoError(t, err)

	has, err = s.HasLocalUsers(context.Background())
	require.NoError(t, err)
	assert.True(t, has)
}

func TestService_UpdateLocale_HappyPath(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	u, err := user.New("a@b.co", "A", user.SourceLocal)
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), u))

	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	require.NoError(t, s.UpdateLocale(context.Background(), u.ID, "id-ID"))
	got, err := store.ByID(context.Background(), u.ID)
	require.NoError(t, err)
	assert.Equal(t, "id-ID", got.Locale)
}

func TestService_UpdateLocale_NotFound(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	err := s.UpdateLocale(context.Background(), uuid.New(), "id-ID")
	require.Error(t, err)
	assert.True(t, user.IsNotFoundError(err))
}

func TestService_ByID_HappyPath(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	u, err := user.New("a@b.co", "A", user.SourceLocal)
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), u))

	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	got, err := s.ByID(context.Background(), u.ID)
	require.NoError(t, err)
	assert.Equal(t, u.Email, got.Email)
}

func TestService_ByID_NotFound(t *testing.T) {
	t.Parallel()
	store := fakes.NewUser()
	s := user.NewService(store, user.GenesisConfig{}, newTestLogger(), noopUnexpected())
	_, err := s.ByID(context.Background(), uuid.New())
	require.Error(t, err)
	assert.True(t, user.IsNotFoundError(err))
}
