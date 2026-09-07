package mailer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type resendCall struct {
	method  string
	headers http.Header
	body    resendPayload
}

type resendServer struct {
	srv      *httptest.Server
	calls    []resendCall
	statuses []int
	header   http.Header
	respBody string
}

func newResendServer(t *testing.T, respBody string, statuses ...int) *resendServer {
	t.Helper()
	rs := &resendServer{statuses: statuses, respBody: respBody, header: http.Header{}}
	rs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := resendCall{method: r.Method, headers: r.Header.Clone()}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &call.body); err != nil {
				t.Errorf("parse body: %v; raw=%s", err, raw)
			}
		}
		status := http.StatusOK
		if n := len(rs.calls); n < len(rs.statuses) {
			status = rs.statuses[n]
		} else if len(rs.statuses) > 0 {
			status = rs.statuses[len(rs.statuses)-1]
		}
		rs.calls = append(rs.calls, call)
		for k, vs := range rs.header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(rs.respBody))
	}))
	t.Cleanup(rs.srv.Close)
	return rs
}

func (rs *resendServer) last() resendCall {
	return rs.calls[len(rs.calls)-1]
}

func newResendAt(t *testing.T, rs *resendServer, cfg ResendConfig) *Resend {
	t.Helper()
	cfg.Endpoint = rs.srv.URL
	r, err := NewResend(Config{From: "default@x", Resend: cfg})
	if err != nil {
		t.Fatalf("NewResend: %v", err)
	}
	return r
}

func validMessage() Message {
	return Message{To: "t@x", From: "f@x", Subject: "hi", TextBody: "plain", HTMLBody: "<b>rich</b>"}
}

func TestResend_Send_PostsExpectedPayload(t *testing.T) {
	rs := newResendServer(t, `{"id":"abc-123"}`, http.StatusOK)
	r := newResendAt(t, rs, ResendConfig{APIKey: "re_secret"})

	if err := r.Send(t.Context(), validMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(rs.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(rs.calls))
	}
	got := rs.last()
	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if want := "Bearer re_secret"; got.headers.Get("Authorization") != want {
		t.Errorf("Authorization = %q, want %q", got.headers.Get("Authorization"), want)
	}
	if ct := got.headers.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if got.headers.Get("Idempotency-Key") == "" {
		t.Error("Idempotency-Key header missing")
	}
	if !strings.Contains(got.headers.Get("User-Agent"), "resend") {
		t.Errorf("User-Agent = %q, want the resend agent", got.headers.Get("User-Agent"))
	}
	if got.body.From != "f@x" {
		t.Errorf("from = %q, want f@x", got.body.From)
	}
	if len(got.body.To) != 1 || got.body.To[0] != "t@x" {
		t.Errorf("to = %v, want [t@x]", got.body.To)
	}
	if got.body.Subject != "hi" || got.body.Text != "plain" || got.body.HTML != "<b>rich</b>" {
		t.Errorf("body = %+v", got.body)
	}
}

func TestResend_Send_DefaultsFromWhenMissing(t *testing.T) {
	rs := newResendServer(t, `{}`, http.StatusOK)
	r := newResendAt(t, rs, ResendConfig{APIKey: "re_secret"})

	m := validMessage()
	m.From = ""
	if err := r.Send(t.Context(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := rs.last().body.From; got != "default@x" {
		t.Errorf("from = %q, want default@x", got)
	}
}

func TestResend_Send_OmitsEmptyBodies(t *testing.T) {
	rs := newResendServer(t, `{}`, http.StatusOK)
	r := newResendAt(t, rs, ResendConfig{APIKey: "re_secret"})

	m := validMessage()
	m.HTMLBody = ""
	if err := r.Send(t.Context(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := rs.last().body.HTML; got != "" {
		t.Errorf("html = %q, want empty", got)
	}
}

func TestResend_Send_RejectsBeforeAnyCall(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*Message)
		check func(error) bool
	}{
		{"crlf in subject", func(m *Message) { m.Subject = "hi\r\nBcc: e@v" }, IsHeaderInjectionError},
		{"crlf in to", func(m *Message) { m.To = "t@x\nBcc: e@v" }, IsHeaderInjectionError},
		{"crlf in from", func(m *Message) { m.From = "f@x\rBcc: e@v" }, IsHeaderInjectionError},
		{"empty to", func(m *Message) { m.To = "" }, IsIncompleteMessageError},
		{"no body", func(m *Message) { m.TextBody = ""; m.HTMLBody = "" }, IsIncompleteMessageError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs := newResendServer(t, `{}`, http.StatusOK)
			r := newResendAt(t, rs, ResendConfig{APIKey: "re_secret"})
			m := validMessage()
			tc.mut(&m)

			err := r.Send(t.Context(), m)
			if !tc.check(err) {
				t.Fatalf("unexpected error type %T: %v", err, err)
			}
			if len(rs.calls) != 0 {
				t.Errorf("issued %d HTTP calls, want 0", len(rs.calls))
			}
		})
	}
}

func TestResend_Send_Non2xxReturnsResendAPIError(t *testing.T) {
	rs := newResendServer(t, `{"message":"invalid_api_key"}`, http.StatusUnauthorized)
	r := newResendAt(t, rs, ResendConfig{APIKey: "re_wrong"})

	err := r.Send(t.Context(), validMessage())
	if !IsResendAPIError(err) {
		t.Fatalf("want ResendAPIError, got %T: %v", err, err)
	}
	var target *ResendAPIError
	if !errors.As(err, &target) {
		t.Fatal("errors.As failed")
	}
	if target.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", target.StatusCode)
	}
	if !strings.Contains(target.Body, "invalid_api_key") {
		t.Errorf("Body = %q, want the API message", target.Body)
	}
	if target.Retryable() {
		t.Error("401 must not be retryable")
	}
	if len(rs.calls) != 1 {
		t.Errorf("calls = %d, want 1 (no retry on 4xx)", len(rs.calls))
	}
}

func TestResend_Send_RetriesServerErrorThenSucceeds(t *testing.T) {
	rs := newResendServer(t, `{}`, http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusOK)
	r := newResendAt(t, rs, ResendConfig{APIKey: "re_secret"})

	if err := r.Send(t.Context(), validMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(rs.calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(rs.calls))
	}
	first := rs.calls[0].headers.Get("Idempotency-Key")
	if first == "" {
		t.Fatal("Idempotency-Key missing")
	}
	for i, c := range rs.calls {
		if got := c.headers.Get("Idempotency-Key"); got != first {
			t.Errorf("call %d Idempotency-Key = %q, want %q replayed", i, got, first)
		}
	}
}

func TestResend_Send_RetryExhaustedReturnsLastStatus(t *testing.T) {
	rs := newResendServer(t, `{"message":"slow down"}`, http.StatusTooManyRequests)
	rs.header.Set("Retry-After", "1")
	r := newResendAt(t, rs, ResendConfig{APIKey: "re_secret", MaxAttempts: 2})

	err := r.Send(t.Context(), validMessage())
	var target *ResendAPIError
	if !errors.As(err, &target) {
		t.Fatalf("want ResendAPIError, got %T: %v", err, err)
	}
	if target.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want 429", target.StatusCode)
	}
	if target.RetryAfter != time.Second {
		t.Errorf("RetryAfter = %v, want 1s", target.RetryAfter)
	}
	if !target.Retryable() {
		t.Error("429 must be retryable")
	}
	if len(rs.calls) != 2 {
		t.Errorf("calls = %d, want 2 (MaxAttempts)", len(rs.calls))
	}
}

func TestResend_Send_NoRetryWhenMaxAttemptsIsOne(t *testing.T) {
	rs := newResendServer(t, `{}`, http.StatusServiceUnavailable)
	r := newResendAt(t, rs, ResendConfig{APIKey: "re_secret", MaxAttempts: 1})

	if err := r.Send(t.Context(), validMessage()); err == nil {
		t.Fatal("expected an error from 503")
	}
	if len(rs.calls) != 1 {
		t.Errorf("calls = %d, want 1", len(rs.calls))
	}
}

func TestResend_Send_CanceledContextPreemptsCall(t *testing.T) {
	rs := newResendServer(t, `{}`, http.StatusOK)
	r := newResendAt(t, rs, ResendConfig{APIKey: "re_secret"})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := r.Send(ctx, validMessage())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if len(rs.calls) != 0 {
		t.Errorf("issued %d HTTP calls after cancel, want 0", len(rs.calls))
	}
}

func TestResend_Send_DeadlineDuringRequest(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(slow.Close)

	r, err := NewResend(Config{From: "f@x", Resend: ResendConfig{APIKey: "re_secret", Endpoint: slow.URL}})
	if err != nil {
		t.Fatalf("NewResend: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	if err := r.Send(ctx, validMessage()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded, got %v", err)
	}
}

func TestNewResend_Validation(t *testing.T) {
	cases := []struct {
		name  string
		cfg   Config
		check func(error) bool
	}{
		{"missing from", Config{Resend: ResendConfig{APIKey: "k"}}, IsMissingConfigError},
		{"missing api key", Config{From: "f@x"}, IsMissingConfigError},
		{"bad endpoint scheme", Config{From: "f@x", Resend: ResendConfig{APIKey: "k", Endpoint: "ftp://x/y"}}, IsInvalidConfigError},
		{"endpoint without host", Config{From: "f@x", Resend: ResendConfig{APIKey: "k", Endpoint: "https:///emails"}}, IsInvalidConfigError},
		{"negative max attempts", Config{From: "f@x", Resend: ResendConfig{APIKey: "k", MaxAttempts: -1}}, IsInvalidConfigError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewResend(tc.cfg)
			if !tc.check(err) {
				t.Fatalf("unexpected error type %T: %v", err, err)
			}
		})
	}
}

func TestNewResend_DefaultsEndpointAndAttempts(t *testing.T) {
	r, err := NewResend(Config{From: "f@x", Resend: ResendConfig{APIKey: "k"}})
	if err != nil {
		t.Fatalf("NewResend: %v", err)
	}
	if r.endpoint != DefaultResendEndpoint {
		t.Errorf("endpoint = %q, want %q", r.endpoint, DefaultResendEndpoint)
	}
	if r.rc == nil {
		t.Error("resty client not built")
	}
}

func TestNew_ResendDriver(t *testing.T) {
	m, err := New(Config{Driver: "resend", From: "f@x", Resend: ResendConfig{APIKey: "k"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := m.(*Resend); !ok {
		t.Errorf("expected *Resend, got %T", m)
	}
}

func TestNew_ResendDriver_RequiresAPIKey(t *testing.T) {
	m, err := New(Config{Driver: "resend", From: "f@x"})
	if !IsMissingConfigError(err) {
		t.Fatalf("want MissingConfigError, got %T: %v", err, err)
	}
	if m != nil {
		t.Errorf("Mailer = %#v, want a nil interface, not one holding a nil *Resend", m)
	}
}

func TestCheckResendEndpoint(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{DefaultResendEndpoint, true},
		{"http://localhost:3000/emails", true},
		{"ftp://api.resend.com/emails", false},
		{"https:///emails", false},
		{"/emails", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			err := checkResendEndpoint(tc.in)
			if tc.ok && err != nil {
				t.Errorf("checkResendEndpoint(%q) = %v, want nil", tc.in, err)
			}
			if !tc.ok && !IsInvalidConfigError(err) {
				t.Errorf("checkResendEndpoint(%q): want InvalidConfigError, got %T: %v", tc.in, err, err)
			}
		})
	}
}

func TestBodySnippet_TruncatesOnRuneBoundary(t *testing.T) {
	long := strings.Repeat("é", resendSnippetLimit)
	got := bodySnippet([]byte(long))
	if len(got) > resendSnippetLimit {
		t.Errorf("len = %d, want <= %d", len(got), resendSnippetLimit)
	}
	if !utf8.ValidString(got) {
		t.Errorf("snippet is not valid UTF-8: %q", got)
	}
	short := "short body"
	if bodySnippet([]byte(short)) != short {
		t.Error("a short body must pass through unchanged")
	}
}

func TestResend_FailingUpstreamStaysWithinRequestBudget(t *testing.T) {
	// NOTE: invite mail is sent inline from the web handler, so the whole ladder must fit inside one request.
	if resendTimeout > 10*time.Second {
		t.Fatalf("resendTimeout = %v; a synchronous handler cannot afford that", resendTimeout)
	}

	rs := newResendServer(t, "", http.StatusServiceUnavailable)
	r := newResendAt(t, rs, ResendConfig{APIKey: "k", MaxAttempts: 4})

	start := time.Now()
	err := r.Send(t.Context(), validMessage())
	elapsed := time.Since(start)

	if !IsResendAPIError(err) {
		t.Fatalf("want ResendAPIError so the caller sees the upstream status, got %T: %v", err, err)
	}
	if len(rs.calls) != 4 {
		t.Fatalf("calls = %d, want 4; a budget that fits proves nothing if the ladder stopped retrying", len(rs.calls))
	}
	// NOTE: three backoffs start at 500ms, 1s and 2s, so anything faster means the ladder is no longer backing off.
	if elapsed < 3*time.Second {
		t.Fatalf("Send took only %v; the ladder cannot have backed off", elapsed)
	}
	if elapsed >= resendTimeout {
		t.Fatalf("Send took %v, budget is %v", elapsed, resendTimeout)
	}
}
