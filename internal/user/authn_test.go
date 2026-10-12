package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/user"
)

type stubAuthenticator struct {
	p   session.Principal
	err error
}

func (s stubAuthenticator) Authenticate(_ context.Context, _ string) (session.Principal, error) {
	return s.p, s.err
}

type countingUserStore struct {
	*fakes.User
	byIDPCalls int
}

func (s *countingUserStore) ByIDP(ctx context.Context, issuer, subject string) (*user.User, error) {
	s.byIDPCalls++
	return s.User.ByIDP(ctx, issuer, subject)
}

func TestAuthenticator_Authenticate(t *testing.T) {
	t.Parallel()

	issuer, subject := "https://idp.example", "sub-1"
	terms := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)

	localUser, err := user.New("person@example.com", "Person", user.SourceOIDC)
	require.NoError(t, err)
	localUser.IDPIssuer = issuer
	localUser.IDPSubject = subject
	localUser.IsAdmin = true
	localUser.Locale = "id-ID"
	localUser.AcceptTerms(terms)

	existingUserID := uuid.New()

	tests := []struct {
		name         string
		next         stubAuthenticator
		seed         *user.User
		storeErr     error
		wantErr      bool
		wantUserID   uuid.UUID
		wantIsAdmin  bool
		wantLocale   string
		wantNoLookup bool
	}{
		{
			name: "jwt with matching local user admits and sets UserID",
			next: stubAuthenticator{p: session.Principal{
				Source:     session.SourceToken,
				IDPIssuer:  issuer,
				IDPSubject: subject,
			}},
			seed:        localUser,
			wantUserID:  localUser.ID,
			wantIsAdmin: true,
			wantLocale:  "id-ID",
		},
		{
			name: "jwt with no matching local user fails closed",
			next: stubAuthenticator{p: session.Principal{
				Source:     session.SourceToken,
				IDPIssuer:  issuer,
				IDPSubject: "no-such-subject",
			}},
			seed:    localUser,
			wantErr: true,
		},
		{
			name: "store error fails closed",
			next: stubAuthenticator{p: session.Principal{
				Source:     session.SourceToken,
				IDPIssuer:  issuer,
				IDPSubject: subject,
			}},
			seed:     localUser,
			storeErr: errors.New("db exploded"),
			wantErr:  true,
		},
		{
			name: "api key principal passes through untouched",
			next: stubAuthenticator{p: session.Principal{
				Source:      session.SourceAPIKey,
				ActiveOrgID: uuid.New(),
			}},
			seed:         localUser,
			wantNoLookup: true,
		},
		{
			name: "principal with UserID already set is left alone",
			next: stubAuthenticator{p: session.Principal{
				Source:     session.SourceToken,
				UserID:     existingUserID,
				IDPIssuer:  issuer,
				IDPSubject: subject,
			}},
			seed:         localUser,
			wantUserID:   existingUserID,
			wantNoLookup: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := fakes.NewUser()
			if tt.seed != nil {
				require.NoError(t, fake.Save(t.Context(), tt.seed))
			}
			fake.ByIDPErr = tt.storeErr

			store := &countingUserStore{User: fake}
			dec := user.NewAuthenticator(tt.next, store)

			got, err := dec.Authenticate(t.Context(), "raw-credential")

			if tt.wantErr {
				require.Error(t, err,
					"membership is the only authority for tenancy: a token must never be granted an org its subject does not belong to")
				var unauthorized *authn.UnauthorizedError
				assert.ErrorAs(t, err, &unauthorized, "want opaque *authn.UnauthorizedError, got %v", err)
				assert.Equal(t, session.Principal{}, got)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantUserID, got.UserID)
				if tt.next.p.Source == session.SourceOIDC || tt.next.p.Source == session.SourceToken {
					assert.Equal(t, tt.wantIsAdmin, got.IsAdmin)
				}
				if tt.wantLocale != "" {
					assert.Equal(t, tt.wantLocale, got.Locale)
				}
			}

			if tt.wantNoLookup {
				assert.Zero(t, store.byIDPCalls, "ByIDP must not be called")
			} else {
				assert.Equal(t, 1, store.byIDPCalls, "ByIDP must be called exactly once")
			}
		})
	}
}

func TestAuthenticator_Authenticate_UpstreamErrorPassesThrough(t *testing.T) {
	t.Parallel()

	upstreamErr := &authn.UnauthorizedError{}
	store := &countingUserStore{User: fakes.NewUser()}
	dec := user.NewAuthenticator(stubAuthenticator{err: upstreamErr}, store)

	_, err := dec.Authenticate(t.Context(), "raw-credential")

	require.Error(t, err)
	assert.Zero(t, store.byIDPCalls, "ByIDP must not be called when the wrapped authenticator itself failed")
}

type tenantFixture struct {
	orgs     *fakeOrgs
	projects *fakeProjects
	orgA     uuid.UUID
	orgB     uuid.UUID
	projectA uuid.UUID
	foreign  uuid.UUID
}

func newTenantFixture(t *testing.T) *tenantFixture {
	t.Helper()

	f := &tenantFixture{
		orgs:     newFakeOrgs(),
		projects: newFakeProjects(),
		orgA:     uuid.New(),
		orgB:     uuid.New(),
		projectA: uuid.New(),
		foreign:  uuid.New(),
	}
	for _, id := range []uuid.UUID{f.orgA, f.orgB, f.foreign} {
		require.NoError(t, f.orgs.Save(t.Context(), &user.OrgRef{ID: id, Slug: id.String(), Name: id.String()}))
	}
	require.NoError(t, f.projects.Save(t.Context(), &user.ProjectRef{ID: f.projectA, OrgID: f.orgA, Slug: "p", Name: "P"}))
	return f
}

func (f *tenantFixture) join(t *testing.T, userID, orgID uuid.UUID) {
	t.Helper()
	require.NoError(t, f.orgs.SaveMembership(t.Context(), &user.MembershipRef{OrgID: orgID, UserID: userID, Role: "owner"}))
}

func seededLocalUser(t *testing.T, issuer, subject string) (*fakes.User, *user.User) {
	t.Helper()

	u, err := user.New("machine@example.com", "Machine", user.SourceOIDC)
	require.NoError(t, err)
	u.IDPIssuer = issuer
	u.IDPSubject = subject
	u.Locale = "en-US"

	store := fakes.NewUser()
	require.NoError(t, store.Save(t.Context(), u))
	return store, u
}

func TestAuthenticator_ResolvesTenantFromMembership(t *testing.T) {
	t.Parallel()

	const issuer, subject = "https://idp.example", "sub-tenant"

	tests := []struct {
		name string
		// memberships the local user holds, by fixture field name: "a", "b".
		member      []string
		claim       func(f *tenantFixture) uuid.UUID
		listErr     error
		noResolver  bool
		wantErr     bool
		wantClaimed bool
		wantOrg     func(f *tenantFixture) uuid.UUID
		wantProject func(f *tenantFixture) uuid.UUID
	}{
		{
			name:        "single membership and no claim resolves that org and its project",
			member:      []string{"a"},
			wantOrg:     func(f *tenantFixture) uuid.UUID { return f.orgA },
			wantProject: func(f *tenantFixture) uuid.UUID { return f.projectA },
		},
		{
			name:        "claim the user is a member of is honored",
			member:      []string{"a", "b"},
			claim:       func(f *tenantFixture) uuid.UUID { return f.orgB },
			wantOrg:     func(f *tenantFixture) uuid.UUID { return f.orgB },
			wantProject: func(f *tenantFixture) uuid.UUID { return uuid.Nil },
		},
		{
			name:        "claim naming an org the user is not a member of is refused",
			member:      []string{"a"},
			claim:       func(f *tenantFixture) uuid.UUID { return f.foreign },
			wantErr:     true,
			wantClaimed: true,
		},
		{
			name:    "claim naming an org nobody is a member of is refused",
			member:  nil,
			claim:   func(f *tenantFixture) uuid.UUID { return f.foreign },
			wantErr: true, wantClaimed: true,
		},
		{
			name:        "several memberships and no claim stays untenanted",
			member:      []string{"a", "b"},
			wantOrg:     func(f *tenantFixture) uuid.UUID { return uuid.Nil },
			wantProject: func(f *tenantFixture) uuid.UUID { return uuid.Nil },
		},
		{
			name:        "no membership stays untenanted",
			wantOrg:     func(f *tenantFixture) uuid.UUID { return uuid.Nil },
			wantProject: func(f *tenantFixture) uuid.UUID { return uuid.Nil },
		},
		{
			name:    "membership lookup failure fails closed",
			member:  []string{"a"},
			listErr: errors.New("db exploded"),
			wantErr: true,
		},
		{
			name:        "without the resolver a claim grants nothing",
			member:      []string{"a"},
			claim:       func(f *tenantFixture) uuid.UUID { return f.orgA },
			noResolver:  true,
			wantOrg:     func(f *tenantFixture) uuid.UUID { return uuid.Nil },
			wantProject: func(f *tenantFixture) uuid.UUID { return uuid.Nil },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, local := seededLocalUser(t, issuer, subject)
			f := newTenantFixture(t)
			for _, m := range tt.member {
				switch m {
				case "a":
					f.join(t, local.ID, f.orgA)
				case "b":
					f.join(t, local.ID, f.orgB)
				}
			}
			f.orgs.ListForUserErr = tt.listErr

			p := session.Principal{Source: session.SourceToken, IDPIssuer: issuer, IDPSubject: subject}
			if tt.claim != nil {
				p.ClaimedOrgID = tt.claim(f)
			}

			opts := []user.AuthenticatorOption{user.WithTenantResolution(f.orgs, f.projects)}
			if tt.noResolver {
				opts = nil
			}
			a := user.NewAuthenticator(stubAuthenticator{p: p}, store, opts...)

			got, err := a.Authenticate(t.Context(), "raw-credential")

			if tt.wantErr {
				require.Error(t, err,
					"membership is the only authority for tenancy: a token must never be granted an org its subject does not belong to")
				assert.True(t, authn.IsUnauthorizedError(err),
					"every tenant-resolution failure must still read as unauthorized, got %T: %v", err, err)
				assert.Equal(t, session.Principal{}, got, "a refused credential must carry no org at all")
				if tt.wantClaimed {
					assert.True(t, user.IsOrgClaimNotMemberError(err),
						"want *user.OrgClaimNotMemberError, got %T: %v", err, err)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantOrg(f), got.ActiveOrgID,
				`wrong ActiveOrgID: a verified JWT must carry the org its user is a member of, or every tenant-scoped call dies with "tenant: context names no org"`)
			assert.Equal(t, tt.wantProject(f), got.ActiveProjectID, "wrong ActiveProjectID for the resolved org")
		})
	}
}

// SECURITY: a token must never be able to assert admin — the local record decides, as it always has.
func TestAuthenticator_IsAdminComesFromTheLocalRecordNotTheToken(t *testing.T) {
	t.Parallel()

	const issuer, subject = "https://idp.example", "sub-admin"

	store, local := seededLocalUser(t, issuer, subject)
	require.False(t, local.IsAdmin, "fixture user must be a non-admin")

	f := newTenantFixture(t)
	f.join(t, local.ID, f.orgA)

	a := user.NewAuthenticator(
		stubAuthenticator{p: session.Principal{
			Source:       session.SourceToken,
			IDPIssuer:    issuer,
			IDPSubject:   subject,
			IsAdmin:      true,
			ClaimedOrgID: f.orgA,
		}},
		store,
		user.WithTenantResolution(f.orgs, f.projects),
	)

	got, err := a.Authenticate(t.Context(), "raw-credential")

	require.NoError(t, err)
	assert.False(t, got.IsAdmin, "a token claiming admin must not make the principal an admin")
	assert.Equal(t, f.orgA, got.ActiveOrgID, "the honored membership must still resolve")
}
