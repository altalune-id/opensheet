package invite

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/mailer"
	"altalune.id/template/nanoid"
)

// DefaultTTL is applied when SendRequest.TTL is zero.
const DefaultTTL = 7 * 24 * time.Hour

// TokenGenFunc mints a fresh raw token and its stored hash.
type TokenGenFunc = func() (raw, hash string, err error)

// SendRequest is the input to SendWorkflow.Execute.
type SendRequest struct {
	Email string
	Role  Role
	TTL   time.Duration
}

// SendWorkflow issues an invite: mints a token, persists the aggregate, and emails the raw token in an accept URL.
type SendWorkflow struct {
	invites    Store
	mailer     mailer.Mailer
	baseURL    string
	tokenGen   TokenGenFunc
	clock      func() time.Time
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
}

// SendOption configures a SendWorkflow.
type SendOption func(*SendWorkflow)

// WithTokenGen overrides the token generator (defaults to nanoid.NewInviteToken).
func WithTokenGen(gen TokenGenFunc) SendOption {
	return func(w *SendWorkflow) { w.tokenGen = gen }
}

// WithClock overrides the workflow clock (defaults to time.Now UTC).
func WithClock(clock func() time.Time) SendOption {
	return func(w *SendWorkflow) { w.clock = clock }
}

// NewSendWorkflow wires the workflow.
func NewSendWorkflow(invites Store, mail mailer.Mailer, baseURL string, log *slog.Logger, unexpected apperror.UnexpectedFunc, opts ...SendOption) *SendWorkflow {
	w := &SendWorkflow{
		invites:    invites,
		mailer:     mail,
		baseURL:    strings.TrimRight(baseURL, "/"),
		tokenGen:   nanoid.NewInviteToken,
		clock:      func() time.Time { return time.Now().UTC() },
		log:        log,
		unexpected: unexpected,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Execute mints an invite, persists it, and emails the raw token.
func (w *SendWorkflow) Execute(ctx context.Context, req SendRequest) (*Invite, error) {
	ctx, span := tracer.Start(ctx, "invite.Send.Execute")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	span.SetAttributes(
		attribute.String("org_id", tc.OrgID.String()),
	)

	if !req.Role.IsValid() {
		return nil, &InvalidRoleError{Role: string(req.Role)}
	}
	if _, err := normalizeEmail(req.Email); err != nil {
		return nil, err
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}

	raw, _, err := w.tokenGen()
	if err != nil {
		span.RecordError(err)
		return nil, w.unexpected(ctx, "invite.Send: tokenGen", fmt.Errorf("invite.Send: tokenGen: %w", err))
	}
	inv, err := New(NewParams{
		OrgID: tc.OrgID,
		Email: req.Email,
		Role:  req.Role,
		TTL:   ttl,
		Token: raw,
		Now:   w.clock(),
	})
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	if err := w.invites.Save(ctx, inv); err != nil {
		span.RecordError(err)
		return nil, w.unexpected(ctx, "invite.Send: save", fmt.Errorf("invite.Send: save: %w", err),
			"invite_id", inv.ID)
	}

	msg := mailer.Message{
		To:      inv.Email,
		Subject: "You've been invited",
		TextBody: fmt.Sprintf(
			"Accept your invite:\n\n%s/invites/accept?token=%s\n\nExpires at %s.",
			w.baseURL, raw, inv.ExpiresAt.Format(time.RFC3339),
		),
	}
	if err := w.mailer.Send(ctx, msg); err != nil {
		span.RecordError(err)
		return nil, w.unexpected(ctx, "invite.Send: mail", fmt.Errorf("invite.Send: mail: %w", err),
			"invite_id", inv.ID)
	}
	span.SetAttributes(attribute.String("invite.id", inv.ID.String()))
	return inv, nil
}
