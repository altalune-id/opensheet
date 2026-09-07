package todo

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/google/uuid"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform/tenant"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/template/internal/todo")

// Service is the todos driving port.
type Service struct {
	store      Store
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
}

// NewService binds the service to its dependencies.
func NewService(store Store, log *slog.Logger, unexpected apperror.UnexpectedFunc) *Service {
	return &Service{store: store, log: log.With("module", "todo"), unexpected: unexpected}
}

// Create constructs a Todo in the caller's tenant scope and persists it.
func (s *Service) Create(ctx context.Context, title string) (*Todo, error) {
	ctx, span := tracer.Start(ctx, "todo.Create")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	span.SetAttributes(
		attribute.String("org_id", tc.OrgID.String()),
		attribute.String("project_id", tc.ProjectID.String()),
	)

	t, err := New(tc.OrgID, tc.ProjectID, title)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	if err := s.store.Save(ctx, t); err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "todo.Create: save", err,
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	span.SetAttributes(attribute.String("todo.id", t.ID.String()))
	return t, nil
}

// List returns todos in the caller's tenant scope.
func (s *Service) List(ctx context.Context, opts ListOpts) ([]*Todo, error) {
	ctx, span := tracer.Start(ctx, "todo.List")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	span.SetAttributes(
		attribute.String("org_id", tc.OrgID.String()),
		attribute.String("project_id", tc.ProjectID.String()),
	)

	out, err := s.store.List(ctx, tc.OrgID, tc.ProjectID, opts)
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "todo.List: list", err,
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	return out, nil
}

// Toggle flips the done flag on the identified todo.
func (s *Service) Toggle(ctx context.Context, id uuid.UUID) (*Todo, error) {
	ctx, span := tracer.Start(ctx, "todo.Toggle",
		trace.WithAttributes(attribute.String("todo.id", id.String())))
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}

	t, err := s.store.ByID(ctx, id)
	if err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "todo.Toggle: byID", err, "todo_id", id)
	}
	if t.OrgID != tc.OrgID || t.ProjectID != tc.ProjectID {
		return nil, &NotFoundError{ID: id.String()}
	}
	t.Toggle()
	if err := s.store.Save(ctx, t); err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "todo.Toggle: save", err, "todo_id", id)
	}
	return t, nil
}

// Delete removes the identified todo from the caller's tenant scope.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "todo.Delete",
		trace.WithAttributes(attribute.String("todo.id", id.String())))
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}

	t, err := s.store.ByID(ctx, id)
	if err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, "todo.Delete: byID", err, "todo_id", id)
	}
	if t.OrgID != tc.OrgID || t.ProjectID != tc.ProjectID {
		return &NotFoundError{ID: id.String()}
	}
	if err := s.store.Delete(ctx, id); err != nil {
		span.RecordError(err)
		return s.unexpected(ctx, "todo.Delete: delete", err, "todo_id", id)
	}
	return nil
}

// ByID returns the identified todo when it belongs to the caller's tenant scope.
func (s *Service) ByID(ctx context.Context, id uuid.UUID) (*Todo, error) {
	ctx, span := tracer.Start(ctx, "todo.ByID",
		trace.WithAttributes(attribute.String("todo.id", id.String())))
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	t, err := s.store.ByID(ctx, id)
	if err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "todo.ByID: byID", err, "todo_id", id)
	}
	if t.OrgID != tc.OrgID {
		return nil, &NotFoundError{ID: id.String()}
	}
	return t, nil
}

// ClearDone bulk-deletes completed todos in the caller's tenant scope.
func (s *Service) ClearDone(ctx context.Context) (int, error) {
	ctx, span := tracer.Start(ctx, "todo.ClearDone")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return 0, err
	}
	span.SetAttributes(
		attribute.String("org_id", tc.OrgID.String()),
		attribute.String("project_id", tc.ProjectID.String()),
	)

	n, err := s.store.ClearDone(ctx, tc.OrgID, tc.ProjectID)
	if err != nil {
		span.RecordError(err)
		return 0, s.unexpected(ctx, "todo.ClearDone: clear", err,
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	span.SetAttributes(attribute.Int("todo.cleared", n))
	return n, nil
}

// AutoCompleteStale marks every open todo older than olderThan as done in the caller's org scope.
func (s *Service) AutoCompleteStale(ctx context.Context, olderThan time.Duration) (int, error) {
	ctx, span := tracer.Start(ctx, "todo.AutoCompleteStale")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return 0, err
	}
	span.SetAttributes(attribute.String("org_id", tc.OrgID.String()))

	cutoff := time.Now().UTC().Add(-olderThan)
	n, err := s.store.MarkDoneOlderThan(ctx, tc.OrgID, cutoff, SweepBatchSize)
	if err != nil {
		span.RecordError(err)
		return 0, s.unexpected(ctx, "todo.AutoCompleteStale: mark done", err, "org_id", tc.OrgID)
	}
	span.SetAttributes(attribute.Int("todo.swept", n))
	return n, nil
}
