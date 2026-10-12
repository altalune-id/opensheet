package ingest_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/ingest"
)

type fakeVerifier struct {
	err  error
	seen []byte
}

func (v *fakeVerifier) Verify(_ *http.Request, body []byte) error {
	v.seen = body
	return v.err
}

type spyHandler struct {
	reached bool
	body    []byte
}

func (s *spyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.reached = true
	s.body, _ = io.ReadAll(r.Body)
	w.WriteHeader(http.StatusAccepted)
}

func post(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec
}

func requireEnvelope(t *testing.T, rec *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	require.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json"),
		"R6 fixes the error shape per surface: Content-Type = %q, want application/json",
		rec.Header().Get("Content-Type"))
	require.NotContains(t, rec.Body.String(), "<html",
		"R6: an HTML error page reached a machine surface")
	var got struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got),
		"R6: the error body is not the declared envelope: %s", rec.Body.String())
	require.Equal(t, wantCode, got.Code, "body=%s", rec.Body.String())
}

// TestHandlerFailsClosed walks every way a delivery can be refused, since R4's S4 row makes provider identity the whole authorization.
func TestHandlerFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		provider   string
		verifier   ingest.Verifier
		handler    http.Handler
		path       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "no provider registered",
			path:       "/hooks/anyprovider/events",
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "unknown provider",
			provider:   "demo",
			verifier:   &fakeVerifier{},
			handler:    &spyHandler{},
			path:       "/hooks/nosuchprovider/events",
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "subtree root names no provider",
			provider:   "demo",
			verifier:   &fakeVerifier{},
			handler:    &spyHandler{},
			path:       "/hooks/",
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "signature does not verify",
			provider:   "demo",
			verifier:   &fakeVerifier{err: errors.New("bad signature")},
			handler:    &spyHandler{},
			path:       "/hooks/demo/events",
			body:       `{"id":"1"}`,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "unauthorized",
		},
		{
			name:       "provider registered with no verifier",
			provider:   "demo",
			handler:    &spyHandler{},
			path:       "/hooks/demo/events",
			body:       `{"id":"1"}`,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "unauthorized",
		},
		{
			name:       "verified delivery with nowhere to go",
			provider:   "demo",
			verifier:   &fakeVerifier{},
			path:       "/hooks/demo/events",
			body:       `{"id":"1"}`,
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			providers := map[string]ingest.Provider{}
			spy, _ := tt.handler.(*spyHandler)
			if tt.provider != "" {
				providers[tt.provider] = ingest.Provider{Verifier: tt.verifier, Handler: tt.handler}
			}
			h := ingest.NewHandler(ingest.HandlerParams{Providers: providers})

			rec := post(t, h, tt.path, tt.body)

			if spy != nil {
				require.False(t, spy.reached,
					"SECURITY: ingest admitted a delivery it did not verify — an unverified body reached a provider handler, and R4 makes provider identity the only authorization on S4; status=%d body=%s",
					rec.Code, rec.Body.String())
			}
			require.GreaterOrEqual(t, rec.Code, 400,
				"ingest answered a refused delivery with status %d — a delivery no verifier claims must never be a success; body=%s",
				rec.Code, rec.Body.String())
			require.Equal(t, tt.wantStatus, rec.Code, "body=%s", rec.Body.String())
			requireEnvelope(t, rec, tt.wantCode)
		})
	}
}

// TestVerifiedDeliveryReachesTheHandler is the positive half of TestHandlerFailsClosed.
func TestVerifiedDeliveryReachesTheHandler(t *testing.T) {
	const body = `{"event":"ping"}`
	v := &fakeVerifier{}
	spy := &spyHandler{}
	h := ingest.NewHandler(ingest.HandlerParams{
		Providers: map[string]ingest.Provider{"demo": {Verifier: v, Handler: spy}},
	})

	rec := post(t, h, "/hooks/demo/events", body)

	require.Equal(t, http.StatusAccepted, rec.Code, "body=%s", rec.Body.String())
	require.True(t, spy.reached, "a verified delivery must reach its provider handler")
	require.Equal(t, body, string(v.seen), "the verifier must see the raw body it has to sign over")
	require.Equal(t, body, string(spy.body), "the handler must be able to read the body the verifier checked")
}

// TestBodyIsBounded proves the read is bounded before verification.
func TestBodyIsBounded(t *testing.T) {
	v := &fakeVerifier{}
	spy := &spyHandler{}
	h := ingest.NewHandler(ingest.HandlerParams{
		MaxBodyBytes: 16,
		Providers:    map[string]ingest.Provider{"demo": {Verifier: v, Handler: spy}},
	})

	rec := post(t, h, "/hooks/demo/events", strings.Repeat("x", 64))

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, "body=%s", rec.Body.String())
	requireEnvelope(t, rec, "payload_too_large")
	require.False(t, spy.reached, "an over-limit body reached a provider handler")
	require.Nil(t, v.seen, "an over-limit body was handed to a verifier")
}

// TestBasePathIsHonoured proves the mount resolves the provider segment under a configured HTTP base path.
func TestBasePathIsHonoured(t *testing.T) {
	spy := &spyHandler{}
	h := ingest.NewHandler(ingest.HandlerParams{
		BasePath:  "/app/hooks",
		Providers: map[string]ingest.Provider{"demo": {Verifier: &fakeVerifier{}, Handler: spy}},
	})

	require.Equal(t, http.StatusAccepted, post(t, h, "/app/hooks/demo/events", "{}").Code)
	require.True(t, spy.reached)

	spy.reached = false
	rec := post(t, h, "/app/hooks/nosuchprovider/events", "{}")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.False(t, spy.reached)
}

// TestVerificationFailureIsLoggedNotReturned proves an operator can debug a failing signature: the cause reaches the log, and only the outcome reaches the caller.
func TestVerificationFailureIsLoggedNotReturned(t *testing.T) {
	var logged bytes.Buffer
	h := ingest.NewHandler(ingest.HandlerParams{
		Log: slog.New(slog.NewTextHandler(&logged, nil)),
		Providers: map[string]ingest.Provider{
			"demo": {Verifier: &fakeVerifier{err: errors.New("signature digest mismatch")}, Handler: &spyHandler{}},
		},
	})

	rec := post(t, h, "/hooks/demo/events", "{}")

	require.Equal(t, http.StatusUnauthorized, rec.Code, "body=%s", rec.Body.String())
	require.Contains(t, logged.String(), "signature digest mismatch",
		"the verifier's cause was discarded, leaving an operator nothing to debug")
	require.NotContains(t, rec.Body.String(), "signature digest mismatch",
		"the cause leaked to an unauthenticated caller")
}
