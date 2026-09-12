package credential_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

const (
	testRefreshToken = "1//refresh-token-do-not-log"
	testAccountEmail = "picker@example.com"
	testReturnTo     = "/orgs/acme/projects/default/credentials"
)

var errStoreTouched = &credential.NotFoundError{ID: "the store must not be touched"}

// tokenResponse is what the fake Google token endpoint hands back.
type tokenResponse struct {
	status       int
	refreshToken string
	email        string
	noIDToken    bool
}

func idToken(t *testing.T, email string) string {
	t.Helper()
	enc := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal id_token part: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	header := enc(map[string]string{"alg": "RS256", "kid": "test"})
	claims := enc(map[string]string{"iss": "https://accounts.google.com", "email": email})
	return header + "." + claims + ".not-a-real-signature"
}

func tokenEndpoint(t *testing.T, resp tokenResponse) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if resp.status != 0 && resp.status != http.StatusOK {
			w.WriteHeader(resp.status)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		body := map[string]any{
			"access_token": "access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		}
		if resp.refreshToken != "" {
			body["refresh_token"] = resp.refreshToken
		}
		if !resp.noIDToken {
			body["id_token"] = idToken(t, resp.email)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("encode token response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func oauthConfig(tokenURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     "client-id.apps.googleusercontent.com",
		ClientSecret: "client-secret",
		RedirectURL:  "https://opensheet.test/app/credentials/google/callback",
		Scopes:       []string{"https://www.googleapis.com/auth/spreadsheets.readonly"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL: tokenURL,
		},
	}
}

// A cfg that is not a usable OAuth client yields no connector, which is how a
// deployment with no Google client reaches the workflow.
func connectorFor(cfg *oauth2.Config) *gworkspace.Connector {
	c, err := gworkspace.NewConnector(cfg, nil)
	if err != nil {
		return nil
	}
	return c
}

type connectFixture struct {
	workflow *credential.ConnectWorkflow
	store    *fakes.Credential
	sealer   sealer.Sealer
	secret   []byte
	now      time.Time
	unex     *int
}

func newConnect(t *testing.T, resp tokenResponse, opts ...func(*connectFixture, *oauth2.Config)) *connectFixture {
	t.Helper()
	if resp.email == "" {
		resp.email = testAccountEmail
	}
	if resp.refreshToken == "" && resp.status == 0 {
		resp.refreshToken = testRefreshToken
	}
	f := &connectFixture{
		store:  fakes.NewCredential(),
		sealer: testSealer(t),
		secret: []byte("connect-state-secret-0123456789abcdef"),
		now:    time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC),
		unex:   new(int),
	}
	cfg := oauthConfig(tokenEndpoint(t, resp).URL)
	for _, opt := range opts {
		opt(f, cfg)
	}
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		*f.unex++
		return apperror.New(apperror.CodeUnexpectedError, err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: apperror.CodeUnexpectedError}).WithCause(err)
	}
	f.workflow = credential.NewConnectWorkflow(
		f.store, f.sealer, connectorFor(cfg), f.secret,
		func() time.Time { return f.now },
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		unexpected,
	)
	return f
}

func (f *connectFixture) start(t *testing.T, tc tenant.Context) string {
	t.Helper()
	raw, err := f.workflow.Start(t.Context(), tc.OrgID, tc.ProjectID, tc.UserID, testReturnTo)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatal("Start produced no state")
	}
	return state
}

func scope(t *testing.T) tenant.Context {
	t.Helper()
	return tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
}

// emptySealer reports success while producing no ciphertext.
type emptySealer struct{}

func (emptySealer) Seal([]byte, []byte) ([]byte, error) { return nil, nil }

func (emptySealer) Open([]byte, []byte) ([]byte, error) { return nil, nil }

func seeded(t *testing.T, tc tenant.Context) *credential.Credential {
	t.Helper()
	c, err := credential.New(uuid.Must(uuid.NewV7()), tc.OrgID, tc.ProjectID, tc.UserID,
		testAccountEmail, credential.KindGoogleOAuth, testAccountEmail, []byte("ciphertext"))
	if err != nil {
		t.Fatalf("credential.New: %v", err)
	}
	return c
}

func TestConnectWorkflow_Start(t *testing.T) {
	t.Run("builds a consent URL requesting exactly the two scopes offline", func(t *testing.T) {
		f := newConnect(t, tokenResponse{})
		tc := scope(t)

		raw, err := f.workflow.Start(t.Context(), tc.OrgID, tc.ProjectID, tc.UserID, testReturnTo)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if u.Host != "accounts.google.com" || u.Path != "/o/oauth2/v2/auth" {
			t.Errorf("auth endpoint = %s", u)
		}
		q := u.Query()
		if got := q.Get("scope"); got != "https://www.googleapis.com/auth/drive.file email" {
			t.Errorf("scope = %q", got)
		}
		if got := q.Get("access_type"); got != "offline" {
			t.Errorf("access_type = %q, want offline", got)
		}
		if got := q.Get("prompt"); got != "consent" {
			t.Errorf("prompt = %q, want consent", got)
		}
		if got := q.Get("response_type"); got != "code" {
			t.Errorf("response_type = %q", got)
		}
		if got := q.Get("redirect_uri"); got != "https://opensheet.test/app/credentials/google/callback" {
			t.Errorf("redirect_uri = %q", got)
		}
		if strings.Contains(q.Get("scope"), "spreadsheets") {
			t.Error("the sensitive spreadsheets scope leaked in from the oauth config")
		}
		if *f.unex != 0 {
			t.Errorf("unexpected() called %d times", *f.unex)
		}
	})

	t.Run("each call mints a distinct state", func(t *testing.T) {
		f := newConnect(t, tokenResponse{})
		tc := scope(t)
		first := f.start(t, tc)
		second := f.start(t, tc)
		if first == second {
			t.Fatal("two Start calls produced the same state")
		}
	})

	t.Run("a zero org, project or user is unscoped", func(t *testing.T) {
		f := newConnect(t, tokenResponse{})
		for _, tc := range []tenant.Context{
			{ProjectID: uuid.New(), UserID: uuid.New()},
			{OrgID: uuid.New(), UserID: uuid.New()},
			{OrgID: uuid.New(), ProjectID: uuid.New()},
		} {
			_, err := f.workflow.Start(t.Context(), tc.OrgID, tc.ProjectID, tc.UserID, "")
			if !tenant.IsUnscopedError(err) {
				t.Fatalf("error = %T %v, want tenant.UnscopedError", err, err)
			}
		}
	})

	t.Run("an off-site returnTo is refused", func(t *testing.T) {
		f := newConnect(t, tokenResponse{})
		tc := scope(t)
		_, err := f.workflow.Start(t.Context(), tc.OrgID, tc.ProjectID, tc.UserID, "https://evil.example.com/")
		if !credential.IsStateInvalidError(err) {
			t.Fatalf("error = %T %v, want *StateInvalidError", err, err)
		}
	})

	t.Run("an unconfigured deployment refuses to start", func(t *testing.T) {
		f := newConnect(t, tokenResponse{}, func(_ *connectFixture, cfg *oauth2.Config) {
			cfg.ClientID = ""
		})
		tc := scope(t)
		_, err := f.workflow.Start(t.Context(), tc.OrgID, tc.ProjectID, tc.UserID, "")
		if !credential.IsNotConfiguredError(err) {
			t.Fatalf("error = %T %v, want *NotConfiguredError", err, err)
		}
	})

	t.Run("a nil clock falls back to the wall clock", func(t *testing.T) {
		store := fakes.NewCredential()
		w := credential.NewConnectWorkflow(
			store, testSealer(t), connectorFor(oauthConfig(tokenEndpoint(t, tokenResponse{}).URL)),
			[]byte("connect-state-secret-0123456789abcdef"), nil,
			slog.New(slog.NewTextHandler(io.Discard, nil)),
			func(context.Context, string, error, ...any) *apperror.AppError {
				t.Error("unexpected() must not be called")
				return nil
			},
		)
		tc := scope(t)
		raw, err := w.Start(t.Context(), tc.OrgID, tc.ProjectID, tc.UserID, testReturnTo)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if _, err := w.ReturnTo(u.Query().Get("state")); err != nil {
			t.Fatalf("ReturnTo: %v", err)
		}
	})

	t.Run("a deployment with no state secret refuses to start", func(t *testing.T) {
		f := newConnect(t, tokenResponse{}, func(fx *connectFixture, _ *oauth2.Config) {
			fx.secret = nil
		})
		tc := scope(t)
		_, err := f.workflow.Start(t.Context(), tc.OrgID, tc.ProjectID, tc.UserID, "")
		if !credential.IsNotConfiguredError(err) {
			t.Fatalf("error = %T %v, want *NotConfiguredError", err, err)
		}
	})
}

func TestConnectWorkflow_Complete(t *testing.T) {
	// SECURITY: the refresh token must reach the store as ciphertext and nowhere else.
	t.Run("seals the refresh token against the credential row", func(t *testing.T) {
		f := newConnect(t, tokenResponse{})
		tc := scope(t)
		state := f.start(t, tc)

		c, err := f.workflow.Complete(t.Context(), "auth-code", state)
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if c.OrgID != tc.OrgID || c.ProjectID != tc.ProjectID || c.AuthorizedByUserID != tc.UserID {
			t.Errorf("state did not carry the scope: %+v", c)
		}
		if c.Kind != credential.KindGoogleOAuth {
			t.Errorf("Kind = %q", c.Kind)
		}
		if c.Status != credential.StatusActive {
			t.Errorf("Status = %q", c.Status)
		}
		if c.GoogleAccountEmail != testAccountEmail || c.Name != testAccountEmail {
			t.Errorf("account = %q, name = %q", c.GoogleAccountEmail, c.Name)
		}
		if strings.Contains(string(c.Sealed), testRefreshToken) {
			t.Fatal("Sealed carries the refresh token in the clear")
		}
		opened, err := f.sealer.Open(c.Sealed, credential.SealAAD(c.OrgID, c.ProjectID, c.ID))
		if err != nil {
			t.Fatalf("the ciphertext must open under the row's own AAD: %v", err)
		}
		if string(opened) != testRefreshToken {
			t.Error("round trip lost the refresh token")
		}
		stored, err := f.store.ByID(t.Context(), c.ID)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if string(stored.Sealed) != string(c.Sealed) {
			t.Error("the store did not receive the sealed credential")
		}
		if *f.unex != 0 {
			t.Errorf("unexpected() called %d times", *f.unex)
		}
	})

	t.Run("recovers the return path from the same state", func(t *testing.T) {
		f := newConnect(t, tokenResponse{})
		state := f.start(t, scope(t))
		got, err := f.workflow.ReturnTo(state)
		if err != nil {
			t.Fatalf("ReturnTo: %v", err)
		}
		if got != testReturnTo {
			t.Errorf("ReturnTo = %q, want %q", got, testReturnTo)
		}
	})

	t.Run("ReturnTo rejects a state it did not sign", func(t *testing.T) {
		f := newConnect(t, tokenResponse{})
		if _, err := f.workflow.ReturnTo("forged|state"); !credential.IsStateInvalidError(err) {
			t.Fatalf("error = %T %v, want *StateInvalidError", err, err)
		}
	})

	t.Run("ReturnTo refuses on an unconfigured deployment", func(t *testing.T) {
		f := newConnect(t, tokenResponse{})
		state := f.start(t, scope(t))
		unconfigured := newConnect(t, tokenResponse{}, func(_ *connectFixture, cfg *oauth2.Config) {
			cfg.ClientID = ""
		})
		if _, err := unconfigured.workflow.ReturnTo(state); !credential.IsNotConfiguredError(err) {
			t.Fatalf("error = %T %v, want *NotConfiguredError", err, err)
		}
	})

	t.Run("reconnecting the same account rotates the existing credential", func(t *testing.T) {
		f := newConnect(t, tokenResponse{})
		tc := scope(t)

		first, err := f.workflow.Complete(t.Context(), "code-1", f.start(t, tc))
		if err != nil {
			t.Fatalf("first Complete: %v", err)
		}
		if _, err := f.workflow.Complete(t.Context(), "code-2", f.start(t, tc)); err != nil {
			t.Fatalf("second Complete: %v", err)
		}

		all, err := f.store.List(t.Context(), tc.OrgID, tc.ProjectID)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(all) != 1 {
			t.Fatalf("stored %d credentials, want 1", len(all))
		}
		if all[0].ID != first.ID {
			t.Errorf("rotation minted a new id: %s != %s", all[0].ID, first.ID)
		}
		opened, err := f.sealer.Open(all[0].Sealed, credential.SealAAD(tc.OrgID, tc.ProjectID, first.ID))
		if err != nil {
			t.Fatalf("the rotated ciphertext must open under the original id: %v", err)
		}
		if string(opened) != testRefreshToken {
			t.Error("rotation lost the refresh token")
		}
	})

	t.Run("reconnecting reactivates a credential that needed reauth", func(t *testing.T) {
		f := newConnect(t, tokenResponse{})
		tc := scope(t)
		first, err := f.workflow.Complete(t.Context(), "code-1", f.start(t, tc))
		if err != nil {
			t.Fatalf("first Complete: %v", err)
		}
		first.MarkReauthNeeded()
		f.store.Seed(first)

		again, err := f.workflow.Complete(t.Context(), "code-2", f.start(t, tc))
		if err != nil {
			t.Fatalf("second Complete: %v", err)
		}
		if again.Status != credential.StatusActive {
			t.Errorf("Status = %q, want active", again.Status)
		}
	})

	t.Run("a different account in the same project gets its own credential", func(t *testing.T) {
		f := newConnect(t, tokenResponse{})
		tc := scope(t)
		if _, err := f.workflow.Complete(t.Context(), "code-1", f.start(t, tc)); err != nil {
			t.Fatalf("first Complete: %v", err)
		}

		other := newConnect(t, tokenResponse{email: "second@example.com"}, func(fx *connectFixture, _ *oauth2.Config) {
			fx.store = f.store
			fx.sealer = f.sealer
		})
		if _, err := other.workflow.Complete(t.Context(), "code-2", other.start(t, tc)); err != nil {
			t.Fatalf("second Complete: %v", err)
		}
		all, err := f.store.List(t.Context(), tc.OrgID, tc.ProjectID)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(all) != 2 {
			t.Fatalf("stored %d credentials, want 2", len(all))
		}
	})

	t.Run("a token response with no id_token still connects", func(t *testing.T) {
		f := newConnect(t, tokenResponse{noIDToken: true})
		tc := scope(t)

		c, err := f.workflow.Complete(t.Context(), "auth-code", f.start(t, tc))
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if c.GoogleAccountEmail != "" {
			t.Errorf("GoogleAccountEmail = %q, want empty", c.GoogleAccountEmail)
		}
		if c.Name == "" {
			t.Error("a nameless credential violates the aggregate invariant")
		}
	})

	t.Run("a list failure routes through unexpected", func(t *testing.T) {
		f := newConnect(t, tokenResponse{})
		tc := scope(t)
		state := f.start(t, tc)
		f.store.ListFn = func(context.Context, uuid.UUID, uuid.UUID) ([]*credential.Credential, error) {
			return nil, errStoreTouched
		}

		if _, err := f.workflow.Complete(t.Context(), "auth-code", state); err == nil {
			t.Fatal("Complete swallowed a list failure")
		}
		if *f.unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *f.unex)
		}
	})

	t.Run("a token response with no refresh token is refused", func(t *testing.T) {
		f := newConnect(t, tokenResponse{refreshToken: " ", status: http.StatusOK})
		tc := scope(t)

		_, err := f.workflow.Complete(t.Context(), "auth-code", f.start(t, tc))
		if !credential.IsNoRefreshTokenError(err) {
			t.Fatalf("error = %T %v, want *NoRefreshTokenError", err, err)
		}
		all, listErr := f.store.List(t.Context(), tc.OrgID, tc.ProjectID)
		if listErr != nil {
			t.Fatalf("List: %v", listErr)
		}
		if len(all) != 0 {
			t.Error("a grant with no refresh token must not be stored")
		}
	})

	t.Run("google rejecting the code routes through unexpected", func(t *testing.T) {
		f := newConnect(t, tokenResponse{status: http.StatusBadRequest})
		tc := scope(t)

		_, err := f.workflow.Complete(t.Context(), "stale-code", f.start(t, tc))
		if err == nil {
			t.Fatal("Complete accepted a rejected code")
		}
		if *f.unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *f.unex)
		}
		if strings.Contains(err.Error(), "client-secret") {
			t.Error("the client secret leaked into the error")
		}
	})

	t.Run("a sealer with no key refuses before anything is stored", func(t *testing.T) {
		f := newConnect(t, tokenResponse{}, func(fx *connectFixture, _ *oauth2.Config) {
			fx.sealer = sealer.Disabled()
		})
		tc := scope(t)

		_, err := f.workflow.Complete(t.Context(), "auth-code", f.start(t, tc))
		if !sealer.IsUnavailableError(err) {
			t.Fatalf("error = %T %v, want sealer.UnavailableError", err, err)
		}
		if sealer.IsOpenFailedError(err) {
			t.Error("a missing key must not read as a tampered row")
		}
	})

	t.Run("a sealer that yields no ciphertext is refused", func(t *testing.T) {
		for name, seed := range map[string]bool{"a fresh credential": false, "a rotation": true} {
			t.Run(name, func(t *testing.T) {
				f := newConnect(t, tokenResponse{}, func(fx *connectFixture, _ *oauth2.Config) {
					fx.sealer = emptySealer{}
				})
				tc := scope(t)
				if seed {
					f.store.Seed(seeded(t, tc))
				}

				_, err := f.workflow.Complete(t.Context(), "auth-code", f.start(t, tc))
				if !credential.IsNotSealedError(err) {
					t.Fatalf("error = %T %v, want *NotSealedError", err, err)
				}
			})
		}
	})

	t.Run("a store failure routes through unexpected", func(t *testing.T) {
		f := newConnect(t, tokenResponse{})
		tc := scope(t)
		state := f.start(t, tc)
		f.store.SaveFn = func(context.Context, *credential.Credential) error { return errStoreTouched }

		if _, err := f.workflow.Complete(t.Context(), "auth-code", state); err == nil {
			t.Fatal("Complete swallowed a store failure")
		}
		if *f.unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *f.unex)
		}
	})

	t.Run("a name collision surfaces as AlreadyExistsError", func(t *testing.T) {
		f := newConnect(t, tokenResponse{})
		tc := scope(t)
		state := f.start(t, tc)
		f.store.SaveFn = func(_ context.Context, c *credential.Credential) error {
			return &credential.AlreadyExistsError{Name: c.Name}
		}

		_, err := f.workflow.Complete(t.Context(), "auth-code", state)
		if !credential.IsAlreadyExistsError(err) {
			t.Fatalf("error = %T %v, want *AlreadyExistsError", err, err)
		}
		if *f.unex != 0 {
			t.Error("a name collision must not route through unexpected")
		}
	})
}

func TestConnectWorkflow_CompleteRejectsBadState(t *testing.T) {
	tc := scope(t)

	tests := []struct {
		name  string
		code  string
		state func(t *testing.T, f *connectFixture) string
	}{
		{
			name: "tampered state",
			code: "auth-code",
			state: func(t *testing.T, f *connectFixture) string {
				s := f.start(t, tc)
				return "A" + s[1:]
			},
		},
		{
			name: "state signed with another secret",
			code: "auth-code",
			state: func(t *testing.T, f *connectFixture) string {
				other := newConnect(t, tokenResponse{}, func(fx *connectFixture, _ *oauth2.Config) {
					fx.secret = []byte("a-different-state-secret-abcdefghij")
				})
				return other.start(t, tc)
			},
		},
		{
			name: "state older than StateMaxAge",
			code: "auth-code",
			state: func(t *testing.T, f *connectFixture) string {
				s := f.start(t, tc)
				f.now = f.now.Add(credential.StateMaxAge + time.Minute)
				return s
			},
		},
		{
			name:  "empty state",
			code:  "auth-code",
			state: func(*testing.T, *connectFixture) string { return "" },
		},
		{
			name:  "missing code",
			code:  "  ",
			state: func(t *testing.T, f *connectFixture) string { return f.start(t, tc) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newConnect(t, tokenResponse{})
			state := tt.state(t, f)
			f.store.SaveFn = func(context.Context, *credential.Credential) error { return errStoreTouched }
			f.store.ListFn = func(context.Context, uuid.UUID, uuid.UUID) ([]*credential.Credential, error) {
				return nil, errStoreTouched
			}

			_, err := f.workflow.Complete(t.Context(), tt.code, state)
			if !credential.IsStateInvalidError(err) {
				t.Fatalf("error = %T %v, want *StateInvalidError", err, err)
			}
			if *f.unex != 0 {
				t.Error("a rejected state must not route through unexpected")
			}
		})
	}

	t.Run("an unconfigured deployment refuses to complete", func(t *testing.T) {
		f := newConnect(t, tokenResponse{})
		state := f.start(t, tc)
		unconfigured := newConnect(t, tokenResponse{}, func(_ *connectFixture, cfg *oauth2.Config) {
			cfg.ClientSecret = ""
		})
		if _, err := unconfigured.workflow.Complete(t.Context(), "auth-code", state); !credential.IsNotConfiguredError(err) {
			t.Fatalf("error = %T %v, want *NotConfiguredError", err, err)
		}
	})
}
