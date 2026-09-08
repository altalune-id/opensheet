package sheet

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/tenant"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer trace.Tracer = otel.Tracer("altalune.id/opensheet/internal/sheet")

// Capabilities reports config-derived feature flags the service must honour.
type Capabilities interface {
	PublicSheetsEnabled() bool
}

// Service is the sheets driving port.
type Service struct {
	store      Store
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
	caps       Capabilities
	snaps      SnapshotStore
	attempts   IdempotencyStore
}

// NewService binds the service to its dependencies.
func NewService(
	store Store,
	log *slog.Logger,
	unexpected apperror.UnexpectedFunc,
	caps Capabilities,
	snaps SnapshotStore,
	attempts IdempotencyStore,
) *Service {
	return &Service{
		store:      store,
		log:        log.With("module", "sheet"),
		unexpected: unexpected,
		caps:       caps,
		snaps:      snaps,
		attempts:   attempts,
	}
}

// Create publishes a tab of spreadsheetID under slug in the caller's tenant scope.
func (s *Service) Create(ctx context.Context, spreadsheetID uuid.UUID, tab, slug string, vis Visibility, ttl time.Duration, writable bool) (*Sheet, error) {
	ctx, span := tracer.Start(ctx, "sheet.Create",
		trace.WithAttributes(
			attribute.String("spreadsheet_id", spreadsheetID.String()),
			attribute.String("sheet.slug", slug),
		))
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	span.SetAttributes(
		attribute.String("org_id", tc.OrgID.String()),
		attribute.String("project_id", tc.ProjectID.String()),
	)

	if err := s.gatePublic(vis, slug); err != nil {
		span.RecordError(err)
		return nil, err
	}

	sh, err := New(NewParams{
		OrgID:         tc.OrgID,
		ProjectID:     tc.ProjectID,
		SpreadsheetID: spreadsheetID,
		Tab:           tab,
		Slug:          slug,
		Visibility:    vis,
		CacheTTL:      ttl,
		Writable:      writable,
	})
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	_, err = s.store.BySlug(ctx, tc.OrgID, tc.ProjectID, slug)
	if err == nil {
		return nil, &AlreadyExistsError{Field: "slug", Value: slug}
	}
	if !IsNotFoundError(err) {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "sheet.Create: bySlug", err,
			"org_id", tc.OrgID, "project_id", tc.ProjectID, "slug", slug)
	}

	if err := s.store.Save(ctx, sh); err != nil {
		if IsAlreadyExistsError(err) {
			return nil, err
		}
		span.RecordError(err)
		return nil, s.unexpected(ctx, "sheet.Create: save", err,
			"org_id", tc.OrgID, "project_id", tc.ProjectID, "slug", slug)
	}
	span.SetAttributes(attribute.String("sheet.id", sh.ID.String()))
	return sh, nil
}

// ByID returns the identified sheet when it belongs to the caller's tenant scope.
func (s *Service) ByID(ctx context.Context, id uuid.UUID) (*Sheet, error) {
	ctx, span := tracer.Start(ctx, "sheet.ByID",
		trace.WithAttributes(attribute.String("sheet.id", id.String())))
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	return s.loadScoped(ctx, span, tc, id, "sheet.ByID")
}

// BySlug returns the sheet published under slug in the caller's project scope.
func (s *Service) BySlug(ctx context.Context, slug string) (*Sheet, error) {
	ctx, span := tracer.Start(ctx, "sheet.BySlug",
		trace.WithAttributes(attribute.String("sheet.slug", slug)))
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	span.SetAttributes(
		attribute.String("org_id", tc.OrgID.String()),
		attribute.String("project_id", tc.ProjectID.String()),
	)

	sh, err := s.store.BySlug(ctx, tc.OrgID, tc.ProjectID, slug)
	if err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "sheet.BySlug: bySlug", err,
			"org_id", tc.OrgID, "project_id", tc.ProjectID, "slug", slug)
	}
	return sh, nil
}

// List returns every sheet in the caller's project scope.
func (s *Service) List(ctx context.Context) ([]*Sheet, error) {
	ctx, span := tracer.Start(ctx, "sheet.List")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	span.SetAttributes(
		attribute.String("org_id", tc.OrgID.String()),
		attribute.String("project_id", tc.ProjectID.String()),
	)

	out, err := s.store.List(ctx, tc.OrgID, tc.ProjectID)
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "sheet.List: list", err,
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	return out, nil
}

// Update applies the non-nil fields of in to the identified sheet.
func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*Sheet, error) {
	ctx, span := tracer.Start(ctx, "sheet.Update",
		trace.WithAttributes(attribute.String("sheet.id", id.String())))
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}

	if in.Visibility != nil {
		if gErr := s.gatePublic(*in.Visibility, ""); gErr != nil {
			span.RecordError(gErr)
			return nil, gErr
		}
	}

	sh, err := s.loadScoped(ctx, span, tc, id, "sheet.Update")
	if err != nil {
		return nil, err
	}

	if in.Visibility != nil {
		if pErr := sh.Publish(*in.Visibility); pErr != nil {
			span.RecordError(pErr)
			return nil, pErr
		}
	}
	if in.CacheTTL != nil {
		if tErr := sh.SetTTL(*in.CacheTTL); tErr != nil {
			span.RecordError(tErr)
			return nil, tErr
		}
	}
	if in.Tab != nil {
		sh.Retab(*in.Tab)
	}
	if in.Writable != nil {
		sh.SetWritable(*in.Writable)
	}

	if err := s.store.Save(ctx, sh); err != nil {
		if IsAlreadyExistsError(err) {
			return nil, err
		}
		span.RecordError(err)
		return nil, s.unexpected(ctx, "sheet.Update: save", err, "sheet_id", id)
	}
	return sh, nil
}

// Delete unpublishes the identified sheet.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "sheet.Delete",
		trace.WithAttributes(attribute.String("sheet.id", id.String())))
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	if _, err := s.loadScoped(ctx, span, tc, id, "sheet.Delete"); err != nil {
		return err
	}
	if err := s.store.Delete(ctx, id); err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, "sheet.Delete: delete", err, "sheet_id", id)
	}
	return nil
}

// PurgeCache drops every cached tab of the identified sheet in the caller's tenant scope.
func (s *Service) PurgeCache(ctx context.Context, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "sheet.PurgeCache",
		trace.WithAttributes(attribute.String("sheet.id", id.String())))
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	sh, err := s.loadScoped(ctx, span, tc, id, "sheet.PurgeCache")
	if err != nil {
		return err
	}
	if err := s.snaps.PurgeSheet(ctx, sh.ID); err != nil {
		span.RecordError(err)
		return s.unexpected(ctx, "sheet.PurgeCache: purge", err, "sheet_id", id)
	}
	return nil
}

// SweepWriteAttempts deletes the caller tenant's expired write attempts and reports how many went.
func (s *Service) SweepWriteAttempts(ctx context.Context) (int, error) {
	ctx, span := tracer.Start(ctx, "sheet.SweepWriteAttempts")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return 0, err
	}
	span.SetAttributes(attribute.String("org_id", tc.OrgID.String()))

	n, err := s.attempts.DeleteExpired(ctx)
	if err != nil {
		span.RecordError(err)
		return 0, s.unexpected(ctx, "sheet.SweepWriteAttempts: delete expired", err, "org_id", tc.OrgID)
	}
	span.SetAttributes(attribute.Int("sheet.write_attempts_swept", n))
	return n, nil
}

func (s *Service) gatePublic(vis Visibility, slug string) error {
	if vis != VisibilityPublic {
		return nil
	}
	if s.caps.PublicSheetsEnabled() {
		return nil
	}
	return &PublicDisabledError{Slug: slug}
}

func (s *Service) loadScoped(ctx context.Context, span trace.Span, tc tenant.Context, id uuid.UUID, op string) (*Sheet, error) {
	sh, err := s.store.ByID(ctx, id)
	if err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, op+": byID", err, "sheet_id", id)
	}
	if sh.OrgID != tc.OrgID || sh.ProjectID != tc.ProjectID {
		return nil, &NotFoundError{ID: id.String()}
	}
	return sh, nil
}
