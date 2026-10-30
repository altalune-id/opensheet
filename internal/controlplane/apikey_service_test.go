package controlplane_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	apikeyv1 "altalune.id/opensheet/gen/go/apikey/v1"
	apikeyv1connect "altalune.id/opensheet/gen/go/apikey/v1/apikeyv1connect"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/controlplane"
	"altalune.id/opensheet/internal/platform"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/testutil/fakes"
)

type apikeyFixture struct {
	t       *testing.T
	baseURL string
	store   *fakes.APIKey
	projs   *fakes.Project
	project *project.Project
	orgID   uuid.UUID
}

func newAPIKeyFixture(t *testing.T) *apikeyFixture {
	t.Helper()
	return newAPIKeyFixtureAs(t, true)
}

func newAPIKeyFixtureAs(t *testing.T, manager bool) *apikeyFixture {
	t.Helper()
	return newAPIKeyFixtureFor(t, manager, session.SourceOIDC)
}

func newAPIKeyFixtureFor(t *testing.T, manager bool, src session.Source) *apikeyFixture {
	t.Helper()
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := apperror.NewReporter(log, false)

	orgID := uuid.New()
	principal := session.Principal{UserID: uuid.New(), Email: "a@b", ActiveOrgID: orgID, Source: src}
	managers := fakes.NewMembers()
	if manager {
		managers.SeatManager(orgID, principal.UserID)
	}

	projs := fakes.NewProject()
	keyStore := fakes.NewAPIKey()

	projectSvc := project.NewService(projs, log, reporter.Unexpected)
	keySvc := apikey.NewService(keyStore, apikey.Scheme{}, managers, fakes.NewOrgProjects(), log, reporter.Unexpected)

	kernel := &platform.Kernel{
		Log:      log,
		Reporter: reporter,
		Verifier: stubVerifier{principal: fullTokenGrant(principal)},
	}

	srv := controlplane.New(nil, kernel, controlplane.Deps{Projects: projectSvc})
	srv.Authn = authn.Chain{apikey.NewAuthenticator(keyStore, nil, apikey.Scheme{}, fakes.NewMembers()), tokens.NewAuthenticator(kernel.Verifier, authn.Scheme{Prefix: apikey.DefaultPrefix})}
	srv.APIKeys = keySvc
	resource, err := tokens.NewResource("https://app.example.com/api")
	require.NoError(t, err)
	srv.Resource = &resource

	ts := httptest.NewServer(srv.Handler(""))
	t.Cleanup(ts.Close)

	proj, err := project.New(orgID, "p1", "Project 1")
	require.NoError(t, err)
	require.NoError(t, projs.Save(ctx, proj))

	return &apikeyFixture{t: t, baseURL: ts.URL, store: keyStore, projs: projs, project: proj, orgID: orgID}
}

func (f *apikeyFixture) client() apikeyv1connect.APIKeyServiceClient {
	return apikeyv1connect.NewAPIKeyServiceClient(http.DefaultClient, f.baseURL+"/api")
}

func TestAPIKey_Create_MintsAndReturnsPlaintextOnce(t *testing.T) {
	f := newAPIKeyFixture(t)

	req := connect.NewRequest(&apikeyv1.CreateRequest{
		ExpiresAt: timestamppb.New(time.Now().Add(24 * time.Hour)),
		ProjectId: f.project.ID.String(),
		Name:      "ci-deploy",
		Scopes:    []string{authn.ScopeAPIKeysRead},
	})
	withBearer(req.Header())

	resp, err := f.client().Create(t.Context(), req)
	require.NoError(t, err)

	require.NotEmpty(t, resp.Msg.GetPlaintext())
	require.True(t, strings.HasPrefix(resp.Msg.GetPlaintext(), apikey.DefaultPrefix))
	require.Equal(t, "ci-deploy", resp.Msg.GetKey().GetName())
	require.Equal(t, f.project.ID.String(), resp.Msg.GetKey().GetProjectId())
	require.Equal(t, []string{authn.ScopeAPIKeysRead}, resp.Msg.GetKey().GetScopes())
	require.NotEmpty(t, resp.Msg.GetKey().GetId())
}

// SECURITY: the plaintext secret comes back from Create only, so List must never carry it.
func TestAPIKey_List_NeverCarriesPlaintext(t *testing.T) {
	f := newAPIKeyFixture(t)

	createReq := connect.NewRequest(&apikeyv1.CreateRequest{
		ExpiresAt: timestamppb.New(time.Now().Add(24 * time.Hour)),
		ProjectId: f.project.ID.String(),
		Name:      "ci-deploy",
		Scopes:    []string{authn.ScopeAPIKeysRead},
	})
	withBearer(createReq.Header())
	created, err := f.client().Create(t.Context(), createReq)
	require.NoError(t, err)
	plaintext := created.Msg.GetPlaintext()
	require.NotEmpty(t, plaintext)

	listReq := connect.NewRequest(&apikeyv1.ListRequest{ProjectId: f.project.ID.String()})
	withBearer(listReq.Header())
	listed, err := f.client().List(t.Context(), listReq)
	require.NoError(t, err)
	require.Len(t, listed.Msg.GetKeys(), 1)
	require.Equal(t, created.Msg.GetKey().GetId(), listed.Msg.GetKeys()[0].GetId())

	raw := rawJSONList(t, f.baseURL, f.project.ID.String())
	require.NotContains(t, raw, plaintext, "the raw List response body must never contain the plaintext secret")
}

func TestAPIKey_Create_UnknownScope_ReturnsInvalidArgument(t *testing.T) {
	f := newAPIKeyFixture(t)

	req := connect.NewRequest(&apikeyv1.CreateRequest{
		ExpiresAt: timestamppb.New(time.Now().Add(24 * time.Hour)),
		ProjectId: f.project.ID.String(),
		Name:      "bad-scope",
		Scopes:    []string{"not:a:real:scope"},
	})
	withBearer(req.Header())

	_, err := f.client().Create(t.Context(), req)
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connectCode(err))
}

func TestAPIKey_Create_EmptyName_ReturnsInvalidArgument(t *testing.T) {
	f := newAPIKeyFixture(t)

	req := connect.NewRequest(&apikeyv1.CreateRequest{
		ExpiresAt: timestamppb.New(time.Now().Add(24 * time.Hour)),
		ProjectId: f.project.ID.String(),
		Name:      "   ",
		Scopes:    []string{authn.ScopeAPIKeysRead},
	})
	withBearer(req.Header())

	_, err := f.client().Create(t.Context(), req)
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connectCode(err))
}

func TestAPIKey_Revoke_MarksKeyRevoked(t *testing.T) {
	f := newAPIKeyFixture(t)

	createReq := connect.NewRequest(&apikeyv1.CreateRequest{
		ExpiresAt: timestamppb.New(time.Now().Add(24 * time.Hour)),
		ProjectId: f.project.ID.String(),
		Name:      "temp",
		Scopes:    []string{authn.ScopeAPIKeysRead},
	})
	withBearer(createReq.Header())
	created, err := f.client().Create(t.Context(), createReq)
	require.NoError(t, err)

	revokeReq := connect.NewRequest(&apikeyv1.RevokeRequest{
		ProjectId: f.project.ID.String(),
		Id:        created.Msg.GetKey().GetId(),
	})
	withBearer(revokeReq.Header())
	_, err = f.client().Revoke(t.Context(), revokeReq)
	require.NoError(t, err)

	listReq := connect.NewRequest(&apikeyv1.ListRequest{ProjectId: f.project.ID.String()})
	withBearer(listReq.Header())
	listed, err := f.client().List(t.Context(), listReq)
	require.NoError(t, err)
	require.Len(t, listed.Msg.GetKeys(), 1)
	require.NotNil(t, listed.Msg.GetKeys()[0].GetRevokedAt())
}

// TestAPIKey_Revoke_KeyFromAnotherProjectInSameOrg_ReturnsNotFound proves apikey.Service.Revoke's ownership check reaches this RPC surface.
func TestAPIKey_Revoke_KeyFromAnotherProjectInSameOrg_ReturnsNotFound(t *testing.T) {
	f := newAPIKeyFixture(t)
	ctx := context.Background()

	proj2, err := project.New(f.orgID, "p2", "Project 2")
	require.NoError(t, err)
	require.NoError(t, f.projs.Save(ctx, proj2))

	createReq := connect.NewRequest(&apikeyv1.CreateRequest{
		ExpiresAt: timestamppb.New(time.Now().Add(24 * time.Hour)),
		ProjectId: f.project.ID.String(),
		Name:      "p1-key",
		Scopes:    []string{authn.ScopeAPIKeysRead},
	})
	withBearer(createReq.Header())
	created, err := f.client().Create(t.Context(), createReq)
	require.NoError(t, err)

	revokeReq := connect.NewRequest(&apikeyv1.RevokeRequest{
		ProjectId: proj2.ID.String(),
		Id:        created.Msg.GetKey().GetId(),
	})
	withBearer(revokeReq.Header())
	_, err = f.client().Revoke(t.Context(), revokeReq)
	require.Error(t, err)
	require.Equal(t, connect.CodeNotFound, connectCode(err))
}

// TestAPIKey_ScopeEnforcement proves ScopeTable's entries gate these procedures for a real API-key credential.
func TestAPIKey_ScopeEnforcement(t *testing.T) {
	f := newAPIKeyFixture(t)
	now := time.Now().UTC()

	readKey, readPlain, err := apikey.Scheme{}.Mint(f.orgID, f.project.ID, "reader", []string{authn.ScopeAPIKeysRead}, nil, nil, now)
	require.NoError(t, err)
	f.store.Seed(readKey)

	listReq := connect.NewRequest(&apikeyv1.ListRequest{ProjectId: f.project.ID.String()})
	listReq.Header().Set("Authorization", "Bearer "+readPlain)
	_, err = f.client().List(t.Context(), listReq)
	require.NoError(t, err, "a read-scoped key must be admitted to List")

	createReq := connect.NewRequest(&apikeyv1.CreateRequest{
		ExpiresAt: timestamppb.New(time.Now().Add(24 * time.Hour)),
		ProjectId: f.project.ID.String(),
		Name:      "should-not-be-created",
		Scopes:    []string{authn.ScopeAPIKeysRead},
	})
	createReq.Header().Set("Authorization", "Bearer "+readPlain)
	_, err = f.client().Create(t.Context(), createReq)
	require.Error(t, err, "a read-scoped key must be denied Create")
	require.Equal(t, connect.CodePermissionDenied, connectCode(err))
}

func rawJSONList(t *testing.T, baseURL, projectID string) string {
	t.Helper()
	body := strings.NewReader(`{"projectId":"` + projectID + `"}`)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+"/api/apikey.v1.APIKeyService/List", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	withBearer(req.Header)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	return string(raw)
}

// SECURITY: only an owner or admin person mints a key; a member and every key — even one holding apikeys:write — are refused.
func TestAPIKey_Create_RequiresAnOwnerOrAdminPerson(t *testing.T) {
	create := func(f *apikeyFixture, auth func(http.Header)) error {
		req := connect.NewRequest(&apikeyv1.CreateRequest{
			ExpiresAt: timestamppb.New(time.Now().Add(24 * time.Hour)),
			ProjectId: f.project.ID.String(),
			Name:      "k",
			Scopes:    []string{controlplane.ScopePostsRead},
		})
		auth(req.Header())
		_, err := f.client().Create(t.Context(), req)
		return err
	}

	t.Run("a member is refused", func(t *testing.T) {
		f := newAPIKeyFixtureAs(t, false)
		err := create(f, withBearer)
		require.Equal(t, connect.CodePermissionDenied, connectCode(err), "err=%v", err)
		require.Empty(t, f.store.All(), "a refused mint must persist nothing")
	})

	t.Run("a key holding apikeys:write is refused", func(t *testing.T) {
		f := newAPIKeyFixture(t)
		k, plaintext, err := apikey.Scheme{}.Mint(f.orgID, f.project.ID, "writer", []string{authn.ScopeAPIKeysWrite}, nil, nil, time.Now().UTC())
		require.NoError(t, err)
		f.store.Seed(k)
		err = create(f, func(h http.Header) { h.Set("Authorization", "Bearer "+plaintext) })
		require.Equal(t, connect.CodePermissionDenied, connectCode(err), "err=%v", err)
		require.Len(t, f.store.All(), 1, "the key must not have minted another")
	})

	t.Run("an owner or admin mints", func(t *testing.T) {
		f := newAPIKeyFixture(t)
		require.NoError(t, create(f, withBearer))
	})
}

// SECURITY: key management stays console-only, so a bearer token is refused without an insufficient_scope hint naming a scope nobody can be granted.
func TestAPIKey_CreateAndRevoke_RefuseABearerToken(t *testing.T) {
	f := newAPIKeyFixtureFor(t, true, session.SourceToken)
	create := connect.NewRequest(&apikeyv1.CreateRequest{
		ExpiresAt: timestamppb.New(time.Now().Add(24 * time.Hour)),
		ProjectId: f.project.ID.String(),
		Name:      "k",
		Scopes:    []string{authn.ScopeSheetsRead},
	})
	withBearer(create.Header())
	_, createErr := f.client().Create(t.Context(), create)

	revoke := connect.NewRequest(&apikeyv1.RevokeRequest{ProjectId: f.project.ID.String(), Id: uuid.NewString()})
	withBearer(revoke.Header())
	_, revokeErr := f.client().Revoke(t.Context(), revoke)

	for name, err := range map[string]error{"Create": createErr, "Revoke": revokeErr} {
		require.Equal(t, connect.CodePermissionDenied, connectCode(err), "%s: err=%v", name, err)
		var cerr *connect.Error
		require.ErrorAs(t, err, &cerr)
		require.Empty(t, cerr.Meta().Get("WWW-Authenticate"), "%s must not ask for a scope", name)
		require.NotContains(t, err.Error(), authn.ScopeAPIKeysWrite, name)
	}
	require.Empty(t, f.store.All(), "a refused mint must persist nothing")
}

func TestAPIKey_Create_RetiredScope_ReturnsInvalidArgument(t *testing.T) {
	f := newAPIKeyFixture(t)
	req := connect.NewRequest(&apikeyv1.CreateRequest{
		ExpiresAt: timestamppb.New(time.Now().Add(24 * time.Hour)),
		ProjectId: f.project.ID.String(),
		Name:      "retired",
		Scopes:    []string{authn.ScopeAPIKeysWrite},
	})
	withBearer(req.Header())
	_, err := f.client().Create(t.Context(), req)
	require.Equal(t, connect.CodeInvalidArgument, connectCode(err), "err=%v", err)
}

func TestAPIKey_Create_WithoutExpiry_ReturnsInvalidArgument(t *testing.T) {
	f := newAPIKeyFixture(t)
	req := connect.NewRequest(&apikeyv1.CreateRequest{
		ProjectId: f.project.ID.String(),
		Name:      "forever",
		Scopes:    []string{controlplane.ScopePostsRead},
	})
	withBearer(req.Header())
	_, err := f.client().Create(t.Context(), req)
	require.Equal(t, connect.CodeInvalidArgument, connectCode(err), "err=%v", err)
	require.Empty(t, f.store.All(), "a key without an expiry must not be stored")
}
