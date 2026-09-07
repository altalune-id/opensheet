package mailer

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"altalune.id/template/httpclient"
	"altalune.id/template/nanoid"

	"github.com/go-resty/resty/v2"
)

// Resend driver defaults. https://resend.com/docs/api-reference/emails/send-email
const (
	DefaultResendEndpoint    = "https://api.resend.com/emails"
	DefaultResendMaxAttempts = 3
)

const (
	resendUserAgent      = "altempl-mailer-resend/1"
	resendBodyLimit      = 64 << 10
	resendTimeout        = 10 * time.Second
	resendIdempotencyLen = 24
	resendSnippetLimit   = 512
)

// Resend sends via the Resend HTTP API.
type Resend struct {
	from     string
	apiKey   string
	endpoint string
	rc       *resty.Client
}

// NewResend builds a Resend driver from cfg, rejecting a missing API key or an unusable endpoint.
func NewResend(cfg Config) (*Resend, error) {
	if cfg.From == "" {
		return nil, &MissingConfigError{Field: "from"}
	}
	if cfg.Resend.APIKey == "" {
		return nil, &MissingConfigError{Field: "resend.apiKey"}
	}
	endpoint := cmp.Or(cfg.Resend.Endpoint, DefaultResendEndpoint)
	if err := checkResendEndpoint(endpoint); err != nil {
		return nil, err
	}
	attempts := cmp.Or(cfg.Resend.MaxAttempts, DefaultResendMaxAttempts)
	if attempts < 1 {
		return nil, &InvalidConfigError{Field: "resend.maxAttempts", Value: strconv.Itoa(attempts), Reason: "must be at least 1"}
	}
	// SECURITY: the SSRF dial filter stays on for the public API; only an operator-set endpoint may reach a private address.
	custom := endpoint != DefaultResendEndpoint
	rc := httpclient.NewResty(httpclient.RestyOptions{
		Timeout:           resendTimeout,
		UserAgent:         resendUserAgent,
		ResponseBodyLimit: resendBodyLimit,
		AllowPrivateHosts: custom,
		// NOTE: replaying a POST is safe here only because every Send carries a fresh Idempotency-Key.
		Retry: httpclient.RetryPolicy{MaxAttempts: attempts, RetryNonIdempotent: true},
	})
	return &Resend{from: cfg.From, apiKey: cfg.Resend.APIKey, endpoint: endpoint, rc: rc}, nil
}

// Send delivers m through the Resend API, retrying rate-limit and server errors under one idempotency key, within a single request budget.
func (r *Resend) Send(ctx context.Context, m Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// NOTE: resty retries above http.Client, so its Timeout bounds one attempt; this deadline is what bounds the whole ladder.
	ctx, cancel := context.WithTimeout(ctx, resendTimeout)
	defer cancel()
	from := cmp.Or(m.From, r.from)
	// SECURITY: reject CR/LF in header fields (RFC 5322 sec 2.2.3) — Resend renders these into the outgoing message headers.
	if err := (Message{From: from, To: m.To, Subject: m.Subject}).Validate(); err != nil {
		return err
	}
	if m.To == "" {
		return &IncompleteMessageError{Field: "to"}
	}
	if m.TextBody == "" && m.HTMLBody == "" {
		return &IncompleteMessageError{Field: "body"}
	}
	key, err := nanoid.New(resendIdempotencyLen)
	if err != nil {
		return fmt.Errorf("mailer: resend: idempotency key: %w", err)
	}
	resp, err := r.rc.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetHeader("Accept", "application/json").
		// NOTE: every retry replays this key, so a duplicate POST cannot deliver a second copy.
		SetHeader("Idempotency-Key", key).
		SetAuthToken(r.apiKey).
		SetBody(resendPayload{
			From:    from,
			To:      []string{m.To},
			Subject: m.Subject,
			Text:    m.TextBody,
			HTML:    m.HTMLBody,
		}).
		Post(r.endpoint)
	if err != nil {
		return fmt.Errorf("mailer: resend: send: %w", err)
	}
	if resp.IsSuccess() {
		return nil
	}
	return &ResendAPIError{
		StatusCode: resp.StatusCode(),
		RetryAfter: httpclient.ParseRetryAfter(resp.Header().Get("Retry-After")),
		Body:       bodySnippet(resp.Body()),
	}
}

type resendPayload struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text,omitempty"`
	HTML    string   `json:"html,omitempty"`
}

// ResendAPIError is returned when the Resend API answers with a non-2xx status.
type ResendAPIError struct {
	StatusCode int
	RetryAfter time.Duration
	Body       string
}

func (e *ResendAPIError) Error() string {
	return fmt.Sprintf("mailer: resend: api status %d: %s", e.StatusCode, e.Body)
}

// Retryable reports whether the status is a rate limit or a server fault, so a later send may succeed.
func (e *ResendAPIError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= http.StatusInternalServerError
}

// IsResendAPIError reports whether err (or anything it wraps) is a ResendAPIError.
func IsResendAPIError(err error) bool {
	var target *ResendAPIError
	return errors.As(err, &target)
}

// IncompleteMessageError is returned when a Message lacks a field the driver requires.
type IncompleteMessageError struct {
	Field string
}

func (e *IncompleteMessageError) Error() string {
	return fmt.Sprintf("mailer: message %s required", e.Field)
}

// IsIncompleteMessageError reports whether err (or anything it wraps) is an IncompleteMessageError.
func IsIncompleteMessageError(err error) bool {
	var target *IncompleteMessageError
	return errors.As(err, &target)
}

func checkResendEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return &InvalidConfigError{Field: "resend.endpoint", Value: raw, Reason: err.Error()}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return &InvalidConfigError{Field: "resend.endpoint", Value: raw, Reason: "scheme must be http or https"}
	}
	if u.Host == "" {
		return &InvalidConfigError{Field: "resend.endpoint", Value: raw, Reason: "host required"}
	}
	return nil
}

func bodySnippet(b []byte) string {
	if len(b) <= resendSnippetLimit {
		return string(b)
	}
	return strings.ToValidUTF8(string(b[:resendSnippetLimit]), "")
}
