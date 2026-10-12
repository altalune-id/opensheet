package controlplane

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	authv1 "altalune.id/opensheet/gen/go/auth/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
)

type recoverTestSink struct {
	mu       sync.Mutex
	messages []string
}

func (s *recoverTestSink) Report(_ context.Context, inc *apperror.Incident) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, inc.Message)
}

func (s *recoverTestSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

type alwaysOKVerifier struct{}

func (alwaysOKVerifier) Verify(_ context.Context, _ string) (session.Principal, error) {
	return session.Principal{}, nil
}

func TestServer_HandlerOptions_RecoversPanicAndReportsUnexpected(t *testing.T) {
	sink := &recoverTestSink{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := apperror.NewReporter(log, false, apperror.WithSinks(sink))

	s := &Server{
		Kernel: &platform.Kernel{
			Log:      log,
			Reporter: reporter,
			Verifier: alwaysOKVerifier{},
		},
		Authn: authn.Chain{tokens.NewAuthenticator(alwaysOKVerifier{})},
	}

	const procedure = "/test.PanicService/Panic"
	handler := connect.NewUnaryHandlerSimple(
		procedure,
		func(_ context.Context, _ *authv1.WhoamiRequest) (*authv1.WhoamiResponse, error) {
			panic("boom")
		},
		s.handlerOptions()...,
	)
	mux := http.NewServeMux()
	mux.Handle(procedure, handler)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	client := connect.NewClient[authv1.WhoamiRequest, authv1.WhoamiResponse](http.DefaultClient, ts.URL+procedure)
	req := connect.NewRequest(&authv1.WhoamiRequest{})
	req.Header().Set("Authorization", "Bearer stub.token.value")

	_, err := client.CallUnary(t.Context(), req)
	if err == nil {
		t.Fatal("expected a Connect error from the panicking method, got nil")
	}
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("expected the client to receive a *connect.Error rather than a dropped connection, got %T: %v", err, err)
	}
	if cerr.Code() != connect.CodeInternal {
		t.Errorf("connect error code = %v, want %v — an unrecovered panic surfaces as a transport failure (e.g. Unavailable/Unknown), not a clean RPC-level Internal error", cerr.Code(), connect.CodeInternal)
	}
	if cerr.Message() == "boom" {
		t.Errorf("bare panic value leaked to the client: %q", cerr.Message())
	}

	details := cerr.Details()
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1 — a panic must carry the same ErrorDetail every other unexpected error does", len(details))
	}
	msg, err := details[0].Value()
	if err != nil {
		t.Fatalf("decoding error detail: %v", err)
	}
	ed, ok := msg.(*apperrorv1.ErrorDetail)
	if !ok {
		t.Fatalf("unexpected detail type %T", msg)
	}
	if ed.Code != apperror.CodeUnexpectedError {
		t.Errorf("ErrorDetail.Code = %q, want %q", ed.Code, apperror.CodeUnexpectedError)
	}

	if got := sink.count(); got != 1 {
		t.Fatalf("reporter.Unexpected calls = %d, want 1 — the panic never reached telemetry", got)
	}
}

func TestServer_RecoverHTTP_RecoversPanicAndReportsUnexpected(t *testing.T) {
	sink := &recoverTestSink{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := apperror.NewReporter(log, false, apperror.WithSinks(sink))
	s := &Server{Kernel: &platform.Kernel{Log: log, Reporter: reporter}}

	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/openapi.yaml", nil)
	s.recoverHTTP(panicking).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d — a panic in a plain handler must return 500, not drop the connection", rec.Code, http.StatusInternalServerError)
	}
	if got := sink.count(); got != 1 {
		t.Fatalf("reporter.Unexpected calls = %d, want 1 — the panic never reached telemetry", got)
	}
}
