package dataplane

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func dataPlaneScopes() []string {
	return []string{
		authn.ScopeSheetsRead, authn.ScopeSheetsWrite, authn.ScopeSheetsAdmin, authn.ScopeCachePurge,
		authn.ScopeSpreadsheetsRead, authn.ScopeSpreadsheetsWrite,
	}
}

func (g *tokenRig) keyAs(mutate func(p *session.Principal)) {
	p := g.keys.principals[testKey]
	mutate(&p)
	g.keys.principals[testKey] = p
}

func (g *tokenRig) authorizeKey(t *testing.T, scope string) (context.Context, error) {
	t.Helper()
	ctx, _, err := g.authorizer().Authorize(t.Context(), testKey, scope, g.orgs.ref.ID, g.projects.ref.ID, g.sheets.ref.ID)
	return ctx, err
}

func TestResolver_KeyHeldFollowsCreatorRole(t *testing.T) {
	g := newTokenRig(dataPlaneScopes()...)

	ctx, err := g.authorizeKey(t, authn.ScopeSheetsRead)
	if err != nil {
		t.Fatalf("admin's key: %v", err)
	}
	if err := authz.Can(ctx, authz.SheetsEnableWrites); err != nil {
		t.Fatalf("admin's key with sheets:admin must hold sheets:enable_writes: %v", err)
	}

	g.roles.Set(g.orgs.ref.ID, g.creator, "member")
	ctx, err = g.authorizeKey(t, authn.ScopeSheetsRead)
	if err != nil {
		t.Fatalf("demoted creator's key: %v", err)
	}
	if err := authz.Can(ctx, authz.SheetsEnableWrites); !authz.IsDeniedError(err) {
		t.Errorf("demoted creator's key must lose sheets:enable_writes, got %v", err)
	}
}

func TestDataPlane_KeyOfRemovedCreatorIs404(t *testing.T) {
	g := newTokenRig(authn.ScopeSheetsRead, authn.ScopeSpreadsheetsRead)
	want := g.missingSlugBody(t)
	g.roles.Set(g.orgs.ref.ID, g.creator, "")

	for _, path := range []string{rowsPath, g.tabsPath()} {
		code, body := g.call(t, http.MethodGet, path, nil, testKey)
		if code != http.StatusNotFound || body != want {
			t.Errorf("%s: %d %s, want the missing-slug 404", path, code, body)
		}
	}
}

func TestDataPlane_KeyScopeWithoutRolePermissionRefused(t *testing.T) {
	body := []byte(`{"name":"ada"}`)
	g := newTokenRig(authn.ScopeSheetsRead, authn.ScopeSheetsWrite)
	g.sheets.ref.Writable = true
	want := g.missingSlugBody(t)

	if code, got := g.call(t, http.MethodPost, rowsPath+"/rows", body, testKey); code != http.StatusCreated {
		t.Fatalf("admin's key write: %d %s, want 201", code, got)
	}
	g.roles.Set(g.orgs.ref.ID, g.creator, "viewer")
	if code, got := g.call(t, http.MethodPost, rowsPath+"/rows", body, testKey); code != http.StatusNotFound || got != want {
		t.Errorf("key whose creator's role lacks sheets:write: %d %s, want the missing-slug 404", code, got)
	}
}

func TestResolver_KeyWithNoCreatorKeepsTodaysAccess(t *testing.T) {
	for name, mutate := range map[string]func(p *session.Principal){
		"project key": func(p *session.Principal) { p.KeyOwnerID = uuid.Nil },
		"org key":     func(p *session.Principal) { p.KeyOwnerID, p.ProjectIDs, p.AllProjects = uuid.Nil, nil, true },
	} {
		t.Run(name, func(t *testing.T) {
			g := newTokenRig(dataPlaneScopes()...)
			g.sheets.ref.Writable = true
			g.keyAs(mutate)
			g.roles.Set(g.orgs.ref.ID, g.creator, "")

			for _, tc := range []struct {
				method, path string
				body         []byte
				want         int
			}{
				{http.MethodGet, rowsPath, nil, http.StatusOK},
				{http.MethodPost, rowsPath + "/rows", []byte(`{"name":"ada"}`), http.StatusCreated},
				{http.MethodDelete, rowsPath + "/cache", nil, http.StatusNoContent},
				{http.MethodGet, g.tabsPath(), nil, http.StatusOK},
				{http.MethodPost, g.tabsPath(), []byte(`{"title":"Q2"}`), http.StatusCreated},
			} {
				if code, body := g.call(t, tc.method, tc.path, tc.body, testKey); code != tc.want {
					t.Errorf("%s %s: %d %s, want %d", tc.method, tc.path, code, body, tc.want)
				}
			}
			ctx, err := g.authorizeKey(t, authn.ScopeSheetsRead)
			if err != nil {
				t.Fatalf("authorize: %v", err)
			}
			if err := authz.Can(ctx, authz.SheetsEnableWrites); err != nil {
				t.Errorf("a key with no creator and sheets:admin must hold sheets:enable_writes: %v", err)
			}
		})
	}
}

func TestDataPlane_KeyOfAnotherOrgIs404EvenWhenItsCreatorIsAMember(t *testing.T) {
	g := newTokenRig(authn.ScopeSheetsRead)
	want := g.missingSlugBody(t)
	g.keyAs(func(p *session.Principal) { p.ActiveOrgID, p.AllProjects = uuid.Must(uuid.NewV7()), true })

	if code, body := g.call(t, http.MethodGet, rowsPath, nil, testKey); code != http.StatusNotFound || body != want {
		t.Errorf("foreign org key: %d %s, want the missing-slug 404", code, body)
	}
}

func TestCredentialAuthorizer_ReturnsTheBoundContext(t *testing.T) {
	g := newTokenRig(authn.ScopeSheetsRead)
	for name, cred := range map[string]string{"key": testKey, "token": testToken} {
		ctx, p, err := g.authorizer().Authorize(t.Context(), cred, authn.ScopeSheetsRead, g.orgs.ref.ID, g.projects.ref.ID, g.sheets.ref.ID)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := session.PrincipalFrom(ctx); got.HeldOrgID != g.orgs.ref.ID || p.HeldOrgID != g.orgs.ref.ID {
			t.Errorf("%s: context bound to %s, principal to %s; want %s", name, got.HeldOrgID, p.HeldOrgID, g.orgs.ref.ID)
		}
	}
}

func TestDataPlane_KeyGrantStillNarrows(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(p *session.Principal)
		path   func(g *tokenRig) string
	}{
		"other project":      {func(p *session.Principal) { p.ProjectIDs = []uuid.UUID{uuid.Must(uuid.NewV7())} }, func(*tokenRig) string { return rowsPath }},
		"unlisted sheet":     {func(p *session.Principal) { p.ResourceIDs = []uuid.UUID{uuid.Must(uuid.NewV7())} }, func(*tokenRig) string { return rowsPath }},
		"restricted on tabs": {nil, func(g *tokenRig) string { return g.tabsPath() }},
		"scope not on key":   {func(p *session.Principal) { p.Scopes = []string{authn.ScopeSpreadsheetsRead} }, func(*tokenRig) string { return rowsPath }},
	} {
		t.Run(name, func(t *testing.T) {
			g := newTokenRig(authn.ScopeSheetsRead, authn.ScopeSpreadsheetsRead)
			want := g.missingSlugBody(t)
			if tc.mutate == nil {
				tc.mutate = func(p *session.Principal) { p.ResourceIDs = []uuid.UUID{g.sheets.ref.ID} }
			}
			g.keyAs(tc.mutate)
			if code, body := g.call(t, http.MethodGet, tc.path(g), nil, testKey); code != http.StatusNotFound || body != want {
				t.Errorf("%d %s, want the missing-slug 404", code, body)
			}
		})
	}
}

func TestDataPlane_PersonalTokenOfDemotedOwnerLosesOrgScope(t *testing.T) {
	g := newTokenRig(authn.ScopeSheetsRead)
	want := g.missingSlugBody(t)
	orgID, owner := g.orgs.ref.ID, g.creator
	members, projects, store := fakes.NewMembers(), fakes.NewOrgProjects(), fakes.NewAPIKey()
	members.SeatManager(orgID, owner)
	projects.Add(orgID, g.projects.ref.ID)
	svc := apikey.NewService(store, apikey.Scheme{}, members, projects, discardLog(), nil)
	ctx := fakes.BindRole(t.Context(), tenant.Context{OrgID: orgID, UserID: owner}, "owner")
	expiry := time.Now().Add(time.Hour)
	_, plaintext, err := svc.MintPersonal(ctx, "laptop", []string{authn.ScopeMembersRead, authn.ScopeSheetsRead}, apikey.ProjectGrant{All: true}, &expiry)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	a := NewCredentialAuthorizer(apikey.NewAuthenticator(store, nil, apikey.Scheme{}, members), nil, authz.NewResolver(g.roles), apikey.Scheme{}.Authn())
	h := g.rig
	read := func() (int, string) {
		rec := serve(t, NewSheetHandler(SheetHandlerParams{
			Orgs: h.orgs, Projects: h.projects, Sheets: h.sheets, Reader: h.reader, Inspector: h.inspector,
			Purger: h.purger, Writer: h.writer, Tabs: h.tabs, Authz: a, Caps: h.caps, Log: discardLog(),
		}), http.MethodGet, rowsPath, nil, http.Header{"Authorization": {"Bearer " + plaintext}})
		return rec.Code, rec.Body.String()
	}

	if code, body := read(); code != http.StatusOK {
		t.Fatalf("owner's token: %d %s, want 200", code, body)
	}
	members.SeatMember(orgID, owner)
	g.roles.Set(orgID, owner, "member")
	if code, body := read(); code != http.StatusNotFound || body != want {
		t.Errorf("demoted owner's org-level token: %d %s, want the missing-slug 404", code, body)
	}
}

func TestPublicSheetRead_NoPrincipal(t *testing.T) {
	g := newTokenRig(authn.ScopeSheetsRead)
	g.sheets.ref.Visibility = sheet.VisibilityPublic

	code, body := g.call(t, http.MethodGet, rowsPath, nil, "")
	if code != http.StatusOK {
		t.Fatalf("public read with no credential: %d %s, want 200", code, body)
	}
	if g.keys.calls != 0 || g.identity.calls != 0 {
		t.Errorf("public read resolved a credential: keys %d, tokens %d", g.keys.calls, g.identity.calls)
	}
}
