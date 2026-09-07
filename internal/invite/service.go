package invite

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform/tenant"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/template/internal/invite")

// Service is the invites driving port.
type Service struct {
	store      Store
	send       *SendWorkflow
	accept     *AcceptWorkflow
	enabled    bool
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
}

// NewService binds the service to its dependencies; enabled=false blocks Send with InvitesDisabledError.
func NewService(store Store, send *SendWorkflow, accept *AcceptWorkflow, enabled bool, log *slog.Logger, unexpected apperror.UnexpectedFunc) *Service {
	return &Service{
		store:      store,
		send:       send,
		accept:     accept,
		enabled:    enabled,
		log:        log.With("module", "invite"),
		unexpected: unexpected,
	}
}

// Enabled reports whether invite issuance is allowed for this deployment.
func (s *Service) Enabled() bool { return s.enabled }

// Send mints an invite, persists it, and emails the raw token; refused with InvitesDisabledError when disabled.
func (s *Service) Send(ctx context.Context, req SendRequest) (*Invite, error) {
	ctx, span := tracer.Start(ctx, "invite.Send")
	defer span.End()
	if !s.enabled {
		return nil, &InvitesDisabledError{Reason: "invites require OIDC in selfhosted mode; set ALT_OIDC_ISSUER to enable"}
	}
	return s.send.Execute(ctx, req)
}

// HasPendingForEmail reports whether email has any pending (non-consumed) invite.
func (s *Service) HasPendingForEmail(ctx context.Context, email string) (bool, error) {
	ctx, span := tracer.Start(ctx, "invite.HasPendingForEmail")
	defer span.End()
	invs, err := s.store.FindPendingForEmail(ctx, email)
	if err != nil {
		return false, s.unexpected(ctx, "invite.HasPendingForEmail", err)
	}
	return len(invs) > 0, nil
}

// Accept redeems a raw token: verifies the invite, finds-or-creates the user, enrols the membership, and marks the invite consumed.
func (s *Service) Accept(ctx context.Context, req AcceptRequest) (*AcceptResult, error) {
	ctx, span := tracer.Start(ctx, "invite.Accept")
	defer span.End()
	return s.accept.Execute(ctx, req)
}

// ListPending returns unused invites in the caller's org.
func (s *Service) ListPending(ctx context.Context) ([]*Invite, error) {
	ctx, span := tracer.Start(ctx, "invite.ListPending")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	span.SetAttributes(attribute.String("org_id", tc.OrgID.String()))

	out, err := s.store.ListPending(ctx, tc.OrgID)
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "invite.ListPending", fmt.Errorf("invite.ListPending: %w", err),
			"org_id", tc.OrgID)
	}
	return out, nil
}

// Revoke deletes a pending invite in the caller's org.
func (s *Service) Revoke(ctx context.Context, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "invite.Revoke",
		trace.WithAttributes(attribute.String("invite.id", id.String())))
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}

	inv, err := s.store.ByID(ctx, id)
	if err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, "invite.Revoke: byID", fmt.Errorf("invite.Revoke: byID: %w", err), "invite_id", id)
	}
	if inv.OrgID != tc.OrgID {
		return &NotFoundError{ID: id.String()}
	}
	if err := s.store.Delete(ctx, id); err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, "invite.Revoke: delete", fmt.Errorf("invite.Revoke: delete: %w", err), "invite_id", id)
	}
	return nil
}
