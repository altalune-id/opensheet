package onboard

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/template/internal/apperror"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer trace.Tracer = otel.Tracer("altalune.id/template/internal/onboard")

// Option customizes a Service at construction time.
type Option func(*Service)

// WithClock overrides the service's time source, useful in tests.
func WithClock(clock func() time.Time) Option {
	return func(s *Service) { s.clock = clock }
}

// Service is the driving port for bootstrap use cases.
type Service struct {
	store      Store
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
	clock      func() time.Time
}

// NewService wires a Service with the canonical dependencies.
func NewService(store Store, log *slog.Logger, unexpected apperror.UnexpectedFunc, opts ...Option) *Service {
	s := &Service{
		store:      store,
		log:        log,
		unexpected: unexpected,
		clock:      func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Required reports whether no bootstrap row exists yet.
func (s *Service) Required(ctx context.Context) (bool, error) {
	ctx, span := tracer.Start(ctx, "onboard.Required")
	defer span.End()
	_, err := s.store.Get(ctx)
	if err == nil {
		return false, nil
	}
	if IsNotOnboardedError(err) {
		return true, nil
	}
	return false, s.unexpected(ctx, "onboard.Required: get", err)
}

// Status returns the bootstrap row, or *NotOnboardedError.
func (s *Service) Status(ctx context.Context) (*Bootstrap, error) {
	ctx, span := tracer.Start(ctx, "onboard.Status")
	defer span.End()
	b, err := s.store.Get(ctx)
	if err != nil {
		if IsNotOnboardedError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "onboard.Status: get", err)
	}
	return b, nil
}

// Complete saves the singleton row and returns *AlreadyOnboardedError on conflict.
func (s *Service) Complete(ctx context.Context, by uuid.UUID, method Method) (*Bootstrap, error) {
	ctx, span := tracer.Start(ctx, "onboard.Complete")
	defer span.End()
	b, err := New(by, method, s.clock())
	if err != nil {
		return nil, err
	}
	if err := s.store.Save(ctx, b); err != nil {
		if IsAlreadyOnboardedError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "onboard.Complete: save", err, slog.String("method", string(method)))
	}
	return b, nil
}
