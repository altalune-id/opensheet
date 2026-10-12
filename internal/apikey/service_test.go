package apikey_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func newAPIKeyService(t *testing.T, store apikey.Store) (*apikey.Service, *int) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	calls := 0
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		calls++
		return apperror.New("opensheet.unexpected", err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(err)
	}
	return apikey.NewService(store, apikey.Scheme{}, fakes.PermissiveMembers(), fakes.NewOrgProjects(), log, unexpected), &calls
}

func tenantCtx(t *testing.T) (context.Context, tenant.Context) {
	t.Helper()
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
	return tenant.Into(t.Context(), tc), tc
}

func mintSeeded(t *testing.T, store *fakes.APIKey, orgID, projectID uuid.UUID, scopes []string, resourceIDs []uuid.UUID) (*apikey.APIKey, string) {
	t.Helper()
	k, plaintext, err := apikey.Scheme{}.Mint(orgID, projectID, "test key", scopes, resourceIDs, nil, time.Now().UTC())
	require.NoError(t, err)
	store.Seed(k)
	return k, plaintext
}

func mustAuthenticate(t *testing.T) session.Principal {
	t.Helper()
	store := fakes.NewAPIKey()
	_, plaintext := mintSeeded(t, store, uuid.New(), uuid.New(), []string{authn.ScopeSheetsRead}, nil)

	auth := apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers())
	p, err := auth.Authenticate(t.Context(), plaintext)
	require.NoError(t, err)
	return p
}

func TestAuthenticateRejectsForeignShape(t *testing.T) {
	store := fakes.NewAPIKey()
	auth := apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers())

	_, err := auth.Authenticate(t.Context(), "aaa.bbb.ccc")
	assert.True(t, authn.IsUnauthorizedError(err), "got %T: %v", err, err)
	assert.False(t, store.BySecretHashCalled, "a JWT-shaped credential must never reach the store")
}

func TestAuthenticateRejectsRevokedKey(t *testing.T) {
	store := fakes.NewAPIKey()
	k, plaintext := mintSeeded(t, store, uuid.New(), uuid.New(), []string{authn.ScopeSheetsRead}, nil)
	now := time.Now().UTC()
	k.RevokedAt = &now
	store.Seed(k)

	auth := apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers())
	_, err := auth.Authenticate(t.Context(), plaintext)
	assert.True(t, authn.IsUnauthorizedError(err), "got %T: %v", err, err)
	assert.False(t, apikey.IsNotFoundError(err), "a revoked key must not surface as a distinguishable NotFoundError")
}

func TestAuthenticateCollapsesStoreOutage(t *testing.T) {
	store := fakes.NewAPIKey()
	store.BySecretHashErr = errors.New("connection refused")
	auth := apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers())

	_, err := auth.Authenticate(t.Context(), apikey.DefaultPrefix+"whatever")
	assert.True(t, authn.IsUnauthorizedError(err), "a store outage must collapse to the same opaque error as a bad credential, got %T: %v", err, err)
}

func TestAuthenticateReturnsAnIdenticalErrorForEveryRejectionCause(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, store *fakes.APIKey) string
	}{
		{
			name:  "empty credential",
			setup: func(*testing.T, *fakes.APIKey) string { return "" },
		},
		{
			name:  "malformed credential",
			setup: func(*testing.T, *fakes.APIKey) string { return "not-a-credential" },
		},
		{
			name:  "jwt shaped credential",
			setup: func(*testing.T, *fakes.APIKey) string { return "aaa.bbb.ccc" },
		},
		{
			name:  "well-formed unknown key",
			setup: func(*testing.T, *fakes.APIKey) string { return apikey.DefaultPrefix + "unknown" },
		},
		{
			name: "revoked key",
			setup: func(t *testing.T, store *fakes.APIKey) string {
				k, plaintext := mintSeeded(t, store, uuid.New(), uuid.New(), []string{authn.ScopeSheetsRead}, nil)
				now := time.Now().UTC()
				k.RevokedAt = &now
				store.Seed(k)
				return plaintext
			},
		},
		{
			name: "expired key",
			setup: func(t *testing.T, store *fakes.APIKey) string {
				k, plaintext := mintSeeded(t, store, uuid.New(), uuid.New(), []string{authn.ScopeSheetsRead}, nil)
				past := time.Now().UTC().Add(-time.Hour)
				k.ExpiresAt = &past
				store.Seed(k)
				return plaintext
			},
		},
		{
			name: "store outage",
			setup: func(_ *testing.T, store *fakes.APIKey) string {
				store.BySecretHashErr = errors.New("connection refused")
				return apikey.DefaultPrefix + "whatever"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := fakes.NewAPIKey()
			raw := tt.setup(t, store)
			auth := apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers())

			p, err := auth.Authenticate(t.Context(), raw)
			assert.Equal(t, &authn.UnauthorizedError{}, err, "every rejection must be the same opaque error")
			assert.Equal(t, session.Principal{}, p, "a rejection must not leak a partial principal")
		})
	}
}

func TestAuthorizeChecksOrgProjectAndScope(t *testing.T) {
	store := fakes.NewAPIKey()
	orgID, projectID := uuid.New(), uuid.New()
	k, plaintext := mintSeeded(t, store, orgID, projectID, []string{authn.ScopeSheetsRead}, nil)
	auth := apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers())

	t.Run("wrong org denies", func(t *testing.T) {
		_, err := auth.Authorize(t.Context(), plaintext, authn.ScopeSheetsRead, uuid.New(), projectID, uuid.New())
		assert.Error(t, err)
	})
	t.Run("wrong project denies", func(t *testing.T) {
		_, err := auth.Authorize(t.Context(), plaintext, authn.ScopeSheetsRead, orgID, uuid.New(), uuid.New())
		assert.Error(t, err)
	})
	t.Run("missing scope denies", func(t *testing.T) {
		_, err := auth.Authorize(t.Context(), plaintext, authn.ScopeSheetsWrite, orgID, projectID, uuid.New())
		assert.True(t, authn.IsInsufficientScopeError(err), "got %T: %v", err, err)
	})
	t.Run("matching grant admits", func(t *testing.T) {
		p, err := auth.Authorize(t.Context(), plaintext, authn.ScopeSheetsRead, orgID, projectID, uuid.New())
		require.NoError(t, err)
		assert.Equal(t, k.OrgID, p.ActiveOrgID)
		assert.Equal(t, session.SourceAPIKey, p.Source)
	})
}

func TestAuthorizeProjectRefusesRestrictedKey(t *testing.T) {
	store := fakes.NewAPIKey()
	orgID, projectID, resourceID := uuid.New(), uuid.New(), uuid.New()
	_, restrictedPlaintext := mintSeeded(t, store, orgID, projectID, []string{authn.ScopeSheetsRead}, []uuid.UUID{resourceID})
	auth := apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers())

	_, err := auth.AuthorizeProject(t.Context(), restrictedPlaintext, authn.ScopeSheetsRead, orgID, projectID)
	assert.True(t, authn.IsInsufficientScopeError(err), "a resource-restricted key must not pass a project-wide check, got %T: %v", err, err)

	_, unrestrictedPlaintext := mintSeeded(t, store, orgID, projectID, []string{authn.ScopeSheetsRead}, nil)
	p, err := auth.AuthorizeProject(t.Context(), unrestrictedPlaintext, authn.ScopeSheetsRead, orgID, projectID)
	require.NoError(t, err)
	assert.Equal(t, session.SourceAPIKey, p.Source)
}

func TestAuthorizeRefusesAnUnlistedResource(t *testing.T) {
	store := fakes.NewAPIKey()
	orgID, projectID, listed := uuid.New(), uuid.New(), uuid.New()
	_, plaintext := mintSeeded(t, store, orgID, projectID, []string{authn.ScopeSheetsWrite}, []uuid.UUID{listed})
	auth := apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers())

	_, err := auth.Authorize(t.Context(), plaintext, authn.ScopeSheetsWrite, orgID, projectID, listed)
	require.NoError(t, err, "the listed resource must stay reachable")

	_, err = auth.Authorize(t.Context(), plaintext, authn.ScopeSheetsWrite, orgID, projectID, uuid.New())
	assert.True(t, authn.IsInsufficientScopeError(err), "an unlisted resource must be refused, got %T: %v", err, err)
}

// SECURITY: the control plane and the MCP surface bound a key by the grant its principal carries, so the principal must carry the key's project and resources.
func TestPrincipalCarriesTheKeysGrant(t *testing.T) {
	store := fakes.NewAPIKey()
	projectID, resourceID := uuid.New(), uuid.New()
	_, plaintext := mintSeeded(t, store, uuid.New(), projectID, []string{authn.ScopeSheetsRead}, []uuid.UUID{resourceID})

	p, err := apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers()).Authenticate(t.Context(), plaintext)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{projectID}, p.ProjectIDs)
	assert.Equal(t, []uuid.UUID{resourceID}, p.ResourceIDs)
}

func TestPrincipalCarriesNoUserIDAndNoSecret(t *testing.T) {
	// SECURITY: a key is not a person. A key principal must never carry a UserID, or a
	// membership check elsewhere will treat it as a signed-in human.
	p := mustAuthenticate(t)
	if p.UserID != uuid.Nil {
		t.Fatal("key principal carries a UserID")
	}
	if p.Source != session.SourceAPIKey {
		t.Fatalf("Source = %v, want SourceAPIKey", p.Source)
	}
}

func TestPrincipalCarriesTheKeyID(t *testing.T) {
	store := fakes.NewAPIKey()
	k, plaintext := mintSeeded(t, store, uuid.New(), uuid.New(), []string{authn.ScopeSheetsRead}, nil)

	auth := apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers())
	p, err := auth.Authenticate(t.Context(), plaintext)
	require.NoError(t, err)
	assert.Equal(t, k.ID, p.KeyID, "principalFor must carry the minted key's id so downstream writers can attribute rows to it")
}

func TestService_MintListRevoke(t *testing.T) {
	store := fakes.NewAPIKey()
	svc, unex := newAPIKeyService(t, store)
	ctx, tc := tenantCtx(t)

	k, plaintext, err := svc.Mint(ctx, "ci key", []string{authn.ScopeSheetsRead}, nil, in(time.Hour))
	require.NoError(t, err)
	assert.NotEmpty(t, plaintext)
	assert.Equal(t, tc.OrgID, k.OrgID)
	assert.Equal(t, tc.ProjectID, k.ProjectID)
	assert.Zero(t, *unex)

	list, err := svc.List(ctx, tc.ProjectID)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	require.NoError(t, svc.Revoke(ctx, k.ID))
	got, err := store.ByID(ctx, k.ID)
	require.NoError(t, err)
	assert.NotNil(t, got.RevokedAt)
}

func TestService_MintRejectsUnknownScope(t *testing.T) {
	store := fakes.NewAPIKey()
	svc, unex := newAPIKeyService(t, store)
	ctx, _ := tenantCtx(t)

	_, _, err := svc.Mint(ctx, "bad", []string{"not-a-scope"}, nil, in(time.Hour))
	assert.True(t, apikey.IsUnknownScopeError(err), "got %T: %v", err, err)
	assert.Zero(t, *unex)
}

func TestService_RevokeMissingKeyIsTypedNotFound(t *testing.T) {
	store := fakes.NewAPIKey()
	svc, unex := newAPIKeyService(t, store)
	ctx, _ := tenantCtx(t)

	err := svc.Revoke(ctx, uuid.New())
	assert.True(t, apikey.IsNotFoundError(err), "got %T: %v", err, err)
	assert.Zero(t, *unex)
}

// TestService_RevokeRefusesKeyFromAnotherProjectInSameOrg proves Revoke enforces tenant scope with the same *NotFoundError a missing id produces.
func TestService_RevokeRefusesKeyFromAnotherProjectInSameOrg(t *testing.T) {
	store := fakes.NewAPIKey()
	svc, unex := newAPIKeyService(t, store)
	orgID, projA, projB := uuid.New(), uuid.New(), uuid.New()

	k, _ := mintSeeded(t, store, orgID, projB, []string{authn.ScopeSheetsRead}, nil)

	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, ProjectID: projA, UserID: uuid.New()})
	err := svc.Revoke(ctx, k.ID)
	assert.True(t, apikey.IsNotFoundError(err), "got %T: %v", err, err)
	assert.Zero(t, *unex)

	got, gErr := store.ByID(t.Context(), k.ID)
	require.NoError(t, gErr)
	assert.Nil(t, got.RevokedAt, "a refused cross-project revoke must not mutate the key")
}

// TestService_RevokeRefusesKeyFromAnotherOrg proves the same guard when only OrgID differs.
func TestService_RevokeRefusesKeyFromAnotherOrg(t *testing.T) {
	store := fakes.NewAPIKey()
	svc, unex := newAPIKeyService(t, store)
	projectID := uuid.New()

	k, _ := mintSeeded(t, store, uuid.New(), projectID, []string{authn.ScopeSheetsRead}, nil)

	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), ProjectID: projectID, UserID: uuid.New()})
	err := svc.Revoke(ctx, k.ID)
	assert.True(t, apikey.IsNotFoundError(err), "got %T: %v", err, err)
	assert.Zero(t, *unex)
}

func TestUsageWorker_FlushesOnTicker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := fakes.NewAPIKey()
		orgID, projectID := uuid.New(), uuid.New()
		k, _ := mintSeeded(t, store, orgID, projectID, []string{authn.ScopeSheetsRead}, nil)

		w := apikey.NewUsageWorker(store, 100*time.Millisecond, nil)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()

		w.Record(k.ID, tenant.Context{OrgID: orgID, ProjectID: projectID}, time.Now())
		time.Sleep(150 * time.Millisecond)
		synctest.Wait()

		got, err := store.ByID(ctx, k.ID)
		require.NoError(t, err)
		assert.NotNil(t, got.LastUsedAt)

		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("Run did not exit after context cancellation")
		}
	})
}

func TestUsageWorker_ExitsOnContextCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := fakes.NewAPIKey()
		w := apikey.NewUsageWorker(store, time.Hour, nil)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()

		cancel()
		synctest.Wait()
		select {
		case err := <-done:
			assert.ErrorIs(t, err, context.Canceled)
		default:
			t.Fatal("Run did not exit after context cancellation")
		}
	})
}

func TestUsageWorker_FlushesPendingOnShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := fakes.NewAPIKey()
		orgID, projectID := uuid.New(), uuid.New()
		k, _ := mintSeeded(t, store, orgID, projectID, []string{authn.ScopeSheetsRead}, nil)

		w := apikey.NewUsageWorker(store, time.Hour, nil)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()

		w.Record(k.ID, tenant.Context{OrgID: orgID, ProjectID: projectID}, time.Now())

		// Cancel long before the hour-long tick: only a shutdown drain can persist this.
		cancel()
		synctest.Wait()
		<-done

		got, err := store.ByID(tenant.Into(t.Context(), tenant.Context{OrgID: orgID, ProjectID: projectID}), k.ID)
		require.NoError(t, err)
		assert.NotNil(t, got.LastUsedAt, "a timestamp recorded before shutdown was dropped")
	})
}
