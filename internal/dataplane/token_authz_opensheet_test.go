package dataplane

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/testutil/fakes"
)

const (
	testToken  = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1In0.c2ln"
	testKey    = "osk_secret"
	missingRow = "/api/v1/orgs/acme/projects/default/sheets/absent"
)

type fakeIdentity struct {
	principals map[string]session.Principal
	err        error
	calls      int
}

func (f *fakeIdentity) Authenticate(_ context.Context, raw string) (session.Principal, error) {
	f.calls++
	if f.err != nil {
		return session.Principal{}, f.err
	}
	p, ok := f.principals[raw]
	if !ok {
		return session.Principal{}, &authn.UnauthorizedError{}
	}
	return p, nil
}

type tokenRig struct {
	*rig
	keys     *fakeIdentity
	identity *fakeIdentity
	roles    *fakes.Roles
	userID   uuid.UUID
	creator  uuid.UUID
}

func newTokenRig(scopes ...string) *tokenRig {
	g := &tokenRig{
		rig:     newRig(),
		roles:   fakes.NewRoles(),
		userID:  uuid.Must(uuid.NewV7()),
		creator: uuid.Must(uuid.NewV7()),
	}
	g.identity = &fakeIdentity{principals: map[string]session.Principal{
		testToken: {Source: session.SourceToken, UserID: g.userID, Scopes: scopes},
	}}
	g.keys = &fakeIdentity{principals: map[string]session.Principal{
		testKey: {
			Source: session.SourceAPIKey, KeyID: uuid.Must(uuid.NewV7()), KeyOwnerID: g.creator, Scopes: scopes,
			ActiveOrgID: g.orgs.ref.ID, ProjectIDs: []uuid.UUID{g.projects.ref.ID},
		},
	}}
	g.seat()
	g.roles.Set(g.orgs.ref.ID, g.creator, "admin")
	g.authz = nil
	return g
}

func (g *tokenRig) seat() { g.roles.Set(g.orgs.ref.ID, g.userID, "member") }

func (g *tokenRig) unseat() { g.roles.Set(g.orgs.ref.ID, g.userID, "") }

func (g *tokenRig) authorizer() Authorizer {
	var people identity
	if g.identity != nil {
		people = g.identity
	}
	return NewCredentialAuthorizer(g.keys, people, authz.NewResolver(g.roles), authn.Scheme{Prefix: "osk_", TokenPrefix: "othra_pt_"})
}

func (g *tokenRig) handler() http.Handler {
	h := g.rig
	return NewSheetHandler(SheetHandlerParams{
		Orgs: h.orgs, Projects: h.projects, Sheets: h.sheets, Reader: h.reader, Inspector: h.inspector,
		Purger: h.purger, Writer: h.writer, Tabs: h.tabs, Authz: g.authorizer(), Caps: h.caps,
		Log: discardLog(),
	})
}

func (g *tokenRig) call(t *testing.T, method, path string, body []byte, cred string) (int, string) {
	t.Helper()
	header := http.Header{}
	if cred != "" {
		header.Set("Authorization", "Bearer "+cred)
	}
	return g.callWith(t, method, path, body, header)
}

func (g *tokenRig) callWith(t *testing.T, method, path string, body []byte, header http.Header) (int, string) {
	t.Helper()
	rec := serve(t, g.handler(), method, path, body, header)
	return rec.Code, rec.Body.String()
}

func (g *tokenRig) missingSlugBody(t *testing.T) string {
	t.Helper()
	keep := g.sheets.err
	g.sheets.err = apperror.New(apperror.CodeSheetNotFound, "Sheet not found", codes.NotFound)
	defer func() { g.sheets.err = keep }()
	code, body := g.call(t, http.MethodGet, missingRow, nil, testToken)
	if code != http.StatusNotFound {
		t.Fatalf("missing slug status = %d, want 404; body=%s", code, body)
	}
	return body
}

func TestCredentialAuthorizer_KeysAndTokensStayApart(t *testing.T) {
	g := newTokenRig(authn.ScopeSheetsRead)

	if code, body := g.call(t, http.MethodGet, rowsPath, nil, testKey); code != http.StatusOK {
		t.Fatalf("key read status = %d, want 200; body=%s", code, body)
	}
	if g.keys.calls != 1 || g.identity.calls != 0 {
		t.Fatalf("key reached keys %d times and tokens %d times, want 1 and 0", g.keys.calls, g.identity.calls)
	}
	if code, body := g.call(t, http.MethodGet, rowsPath, nil, testToken); code != http.StatusOK {
		t.Fatalf("token read status = %d, want 200; body=%s", code, body)
	}
	if g.keys.calls != 1 {
		t.Errorf("a token reached the key identity: %d calls, want 1", g.keys.calls)
	}

	g.identity = nil
	jwtCode, jwtBody := g.call(t, http.MethodGet, rowsPath, nil, testToken)
	g.keys.err = &authn.UnauthorizedError{}
	keyCode, keyBody := g.call(t, http.MethodGet, rowsPath, nil, testKey)
	if keyCode != http.StatusNotFound || jwtCode != http.StatusNotFound || jwtBody != keyBody {
		t.Errorf("refusals: key %d %s, token %d %s; want one identical 404", keyCode, keyBody, jwtCode, jwtBody)
	}
}

func TestTokenAuthz_MemberWithScopeReads(t *testing.T) {
	g := newTokenRig(authn.ScopeSheetsRead, authn.ScopeSpreadsheetsRead)

	if code, body := g.call(t, http.MethodGet, rowsPath, nil, testToken); code != http.StatusOK {
		t.Fatalf("rows status = %d, want 200; body=%s", code, body)
	}
	if code, body := g.call(t, http.MethodGet, g.tabsPath(), nil, testToken); code != http.StatusOK {
		t.Fatalf("tabs status = %d, want 200; body=%s", code, body)
	}
	if g.keys.calls != 0 {
		t.Error("a token reached the key identity")
	}
}

func TestTokenAuthz_WritesNeedSheetsWrite(t *testing.T) {
	body := []byte(`{"name":"ada"}`)
	reader := newTokenRig(authn.ScopeSheetsRead)
	writer := newTokenRig(authn.ScopeSheetsRead, authn.ScopeSheetsWrite)
	for _, g := range []*tokenRig{reader, writer} {
		g.sheets.ref.Writable = true
	}

	code, got := reader.call(t, http.MethodPost, rowsPath+"/rows", body, testToken)
	if code != http.StatusNotFound || got != reader.missingSlugBody(t) {
		t.Errorf("read-only token write: %d %s, want the missing-slug 404", code, got)
	}
	if code, got := writer.call(t, http.MethodPost, rowsPath+"/rows", body, testToken); code != http.StatusCreated {
		t.Errorf("write token: status %d, want 201; body=%s", code, got)
	}
}

func TestDataPlane_TokenOfNonMemberIs404(t *testing.T) {
	g := newTokenRig(authn.ScopeSheetsRead, authn.ScopeSpreadsheetsRead)
	g.unseat()
	want := g.missingSlugBody(t)

	for _, path := range []string{rowsPath, g.tabsPath()} {
		code, body := g.call(t, http.MethodGet, path, nil, testToken)
		if code != http.StatusNotFound || body != want {
			t.Errorf("%s: %d %s, want the missing-slug 404 %s", path, code, body, want)
		}
	}
}

func TestTokenAuthz_MissingScopeIs404(t *testing.T) {
	g := newTokenRig(authn.ScopeAPIKeysRead)
	want := g.missingSlugBody(t)

	for _, path := range []string{rowsPath, g.tabsPath()} {
		code, body := g.call(t, http.MethodGet, path, nil, testToken)
		if code != http.StatusNotFound || body != want {
			t.Errorf("%s: %d %s, want the missing-slug 404", path, code, body)
		}
	}
}

func TestTokenAuthz_UnknownPersonIs404(t *testing.T) {
	g := newTokenRig(authn.ScopeSheetsRead)
	want := g.missingSlugBody(t)
	g.identity.principals[testToken] = session.Principal{Source: session.SourceToken, Scopes: []string{authn.ScopeSheetsRead}}

	code, body := g.call(t, http.MethodGet, rowsPath, nil, testToken)
	if code != http.StatusNotFound || body != want {
		t.Errorf("a token with no local person: %d %s, want the missing-slug 404", code, body)
	}
}

func TestTokenAuthz_IssuerOutageIs503(t *testing.T) {
	outage := &authn.UnavailableError{Cause: errors.New("unreachable")}
	tests := []struct {
		name  string
		cred  string
		setup func(g *tokenRig)
	}{
		{"issuer outage", testToken, func(g *tokenRig) { g.identity.err = outage }},
		{"membership store failure", testToken, func(g *tokenRig) { g.roles.Err = errors.New("db down") }},
		{"key creator store failure", testKey, func(g *tokenRig) { g.roles.Err = errors.New("db down") }},
		{"api key store failure", testKey, func(g *tokenRig) { g.keys.err = outage }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newTokenRig(authn.ScopeSheetsRead, authn.ScopeSpreadsheetsRead)
			tt.setup(g)
			for _, path := range []string{rowsPath, g.tabsPath()} {
				code, body := g.call(t, http.MethodGet, path, nil, tt.cred)
				if code != http.StatusServiceUnavailable {
					t.Errorf("%s: status %d, want 503; body=%s", path, code, body)
				}
				want := `{"error":{"code":"` + apperror.CodeTokenUnavailable + `","message":"` + authn.UnavailableMessage + `"}}`
				if body != want {
					t.Errorf("%s: body %s, want %s", path, body, want)
				}
			}
		})
	}
}

func discardLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

func serve(t *testing.T, h http.Handler, method, path string, body []byte, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader = http.NoBody
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, reader)
	req.Header = header
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestTokenAuthz_PersonalTokenIs404AndNeverVerified(t *testing.T) {
	g := newTokenRig(authn.ScopeSheetsRead)
	want := g.missingSlugBody(t)
	before := g.identity.calls

	code, body := g.call(t, http.MethodGet, rowsPath, nil, "othra_pt_0123456789abcdefghijklmnopqrstuvwxyzABCD")
	if code != http.StatusNotFound || body != want {
		t.Errorf("personal token: %d %s, want the missing-slug 404", code, body)
	}
	if g.identity.calls != before {
		t.Error("a personal token reached the verifier")
	}
}

func TestTokenAuthz_TokenInXAPIKeyIsRefused(t *testing.T) {
	g := newTokenRig(authn.ScopeSheetsRead)
	want := g.missingSlugBody(t)
	before := g.identity.calls

	code, body := g.callWith(t, http.MethodGet, rowsPath, nil, http.Header{"X-Api-Key": {testToken}})
	if code != http.StatusNotFound || body != want {
		t.Errorf("token in X-API-Key: %d %s, want the missing-slug 404", code, body)
	}
	if g.identity.calls != before {
		t.Error("a token sent in X-API-Key reached the verifier")
	}
}

func TestTokenAuthz_TabWriteNeedsSpreadsheetsWrite(t *testing.T) {
	body := []byte(`{"title":"Q2"}`)
	reader := newTokenRig(authn.ScopeSpreadsheetsRead)
	writer := newTokenRig(authn.ScopeSpreadsheetsRead, authn.ScopeSpreadsheetsWrite)

	if code, got := reader.call(t, http.MethodPost, reader.tabsPath(), body, testToken); code != http.StatusNotFound {
		t.Errorf("read-only token adding a tab: %d %s, want 404", code, got)
	}
	if code, got := writer.call(t, http.MethodPost, writer.tabsPath(), body, testToken); code != http.StatusCreated {
		t.Errorf("write token adding a tab: %d %s, want 201", code, got)
	}
}
