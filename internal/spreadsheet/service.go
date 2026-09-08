package spreadsheet

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/oauth2"

	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/gwerr"
	"altalune.id/opensheet/internal/platform/tenant"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/opensheet/internal/spreadsheet")

// TokenSources resolves a credential id to a Google token source.
type TokenSources interface {
	TokenSourceFor(ctx context.Context, credentialID uuid.UUID) (oauth2.TokenSource, error)
}

// Service is the spreadsheets driving port.
type Service struct {
	store       Store
	log         *slog.Logger
	unexpected  apperror.UnexpectedFunc
	tokens      TokenSources
	clients     gsheet.Factory
	writeTokens TokenSources
	writers     gsheet.WriterFactory
}

// NewService binds the service to its dependencies.
// NOTE: tokens and writeTokens are separate so a read stays read-scoped; only writeTokens is pinned to gsheet.ScopeReadWrite.
func NewService(
	store Store,
	log *slog.Logger,
	unexpected apperror.UnexpectedFunc,
	tokens TokenSources,
	clients gsheet.Factory,
	writeTokens TokenSources,
	writers gsheet.WriterFactory,
) *Service {
	return &Service{
		store:       store,
		log:         log.With("module", "spreadsheet"),
		unexpected:  unexpected,
		tokens:      tokens,
		clients:     clients,
		writeTokens: writeTokens,
		writers:     writers,
	}
}

// Register records a Google document in the caller's project, bound to one credential.
func (s *Service) Register(ctx context.Context, credentialID uuid.UUID, googleFileID, title string) (*Spreadsheet, error) {
	ctx, span := tracer.Start(ctx, "spreadsheet.Register")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	span.SetAttributes(
		attribute.String("org_id", tc.OrgID.String()),
		attribute.String("project_id", tc.ProjectID.String()),
	)

	sp, err := New(tc.OrgID, tc.ProjectID, credentialID, googleFileID, title)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	if saveErr := s.store.Save(ctx, sp); saveErr != nil {
		span.RecordError(saveErr)
		if IsAlreadyExistsError(saveErr) {
			return nil, saveErr
		}
		return nil, s.unexpected(ctx, "spreadsheet.Register: save", saveErr,
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	span.SetAttributes(attribute.String("spreadsheet.id", sp.ID.String()))
	return sp, nil
}

// List returns the documents registered in the caller's project, oldest first.
func (s *Service) List(ctx context.Context) ([]*Spreadsheet, error) {
	ctx, span := tracer.Start(ctx, "spreadsheet.List")
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
		return nil, s.unexpected(ctx, "spreadsheet.List: list", err,
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	return out, nil
}

// ByID returns the identified document when it belongs to the caller's project.
func (s *Service) ByID(ctx context.Context, id uuid.UUID) (*Spreadsheet, error) {
	ctx, span := tracer.Start(ctx, "spreadsheet.ByID",
		trace.WithAttributes(attribute.String("spreadsheet.id", id.String())))
	defer span.End()

	sp, err := s.resolve(ctx, id, "ByID")
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	return sp, nil
}

// ByGoogleFileID returns the document registered in the caller's project under fileID.
func (s *Service) ByGoogleFileID(ctx context.Context, fileID string) (*Spreadsheet, error) {
	ctx, span := tracer.Start(ctx, "spreadsheet.ByGoogleFileID")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	span.SetAttributes(
		attribute.String("org_id", tc.OrgID.String()),
		attribute.String("project_id", tc.ProjectID.String()),
	)

	sp, err := s.store.ByGoogleFileID(ctx, tc.OrgID, tc.ProjectID, fileID)
	if err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "spreadsheet.ByGoogleFileID: lookup", err,
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	return sp, nil
}

// Retitle renames the identified document in the caller's project.
func (s *Service) Retitle(ctx context.Context, id uuid.UUID, title string) (*Spreadsheet, error) {
	ctx, span := tracer.Start(ctx, "spreadsheet.Retitle",
		trace.WithAttributes(attribute.String("spreadsheet.id", id.String())))
	defer span.End()

	sp, err := s.resolve(ctx, id, "Retitle")
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	if err := sp.Retitle(title); err != nil {
		span.RecordError(err)
		return nil, err
	}
	if err := s.store.Save(ctx, sp); err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "spreadsheet.Retitle: save", err, "spreadsheet_id", id)
	}
	return sp, nil
}

// Rebind points the identified document at another credential.
func (s *Service) Rebind(ctx context.Context, id, credentialID uuid.UUID) (*Spreadsheet, error) {
	ctx, span := tracer.Start(ctx, "spreadsheet.Rebind",
		trace.WithAttributes(attribute.String("spreadsheet.id", id.String())))
	defer span.End()

	sp, err := s.resolve(ctx, id, "Rebind")
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	if err := sp.Rebind(credentialID); err != nil {
		span.RecordError(err)
		return nil, err
	}
	if err := s.store.Save(ctx, sp); err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "spreadsheet.Rebind: save", err, "spreadsheet_id", id)
	}
	return sp, nil
}

// SetWritable decides whether the data plane may mutate the identified document's structure.
func (s *Service) SetWritable(ctx context.Context, id uuid.UUID, writable bool) (*Spreadsheet, error) {
	ctx, span := tracer.Start(ctx, "spreadsheet.SetWritable",
		trace.WithAttributes(
			attribute.String("spreadsheet.id", id.String()),
			attribute.Bool("writable", writable),
		))
	defer span.End()

	sp, err := s.resolve(ctx, id, "SetWritable")
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	sp.SetWritable(writable)
	if err := s.store.Save(ctx, sp); err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "spreadsheet.SetWritable: save", err, "spreadsheet_id", id)
	}
	return sp, nil
}

// Delete removes the identified document, cascading the sheets published from it.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "spreadsheet.Delete",
		trace.WithAttributes(attribute.String("spreadsheet.id", id.String())))
	defer span.End()

	if _, err := s.resolve(ctx, id, "Delete"); err != nil {
		span.RecordError(err)
		return err
	}
	if err := s.store.Delete(ctx, id); err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, "spreadsheet.Delete: delete", err, "spreadsheet_id", id)
	}
	return nil
}

// ListTabs asks Google for the document's tab titles, through its bound credential.
func (s *Service) ListTabs(ctx context.Context, id uuid.UUID) ([]string, error) {
	ctx, span := tracer.Start(ctx, "spreadsheet.ListTabs",
		trace.WithAttributes(attribute.String("spreadsheet.id", id.String())))
	defer span.End()

	sp, err := s.resolve(ctx, id, "ListTabs")
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	ts, err := s.tokens.TokenSourceFor(ctx, sp.CredentialID)
	if err != nil {
		span.RecordError(err)
		return nil, s.translate(ctx, "spreadsheet.ListTabs: token source", err, sp)
	}
	client, err := s.clients(ctx, ts)
	if err != nil {
		span.RecordError(err)
		return nil, s.translate(ctx, "spreadsheet.ListTabs: build client", err, sp)
	}
	tabs, err := client.Tabs(ctx, sp.GoogleFileID)
	if err != nil {
		span.RecordError(err)
		return nil, s.translate(ctx, "spreadsheet.ListTabs: tabs", gwerr.AppError(err), sp)
	}
	span.SetAttributes(attribute.Int("spreadsheet.tabs", len(tabs)))
	return tabs, nil
}

// AddTab creates a tab titled title in the identified document, through its bound credential.
func (s *Service) AddTab(ctx context.Context, spreadsheetID uuid.UUID, title string) error {
	ctx, span := tracer.Start(ctx, "spreadsheet.AddTab",
		trace.WithAttributes(attribute.String("spreadsheet.id", spreadsheetID.String())))
	defer span.End()

	sp, err := s.resolve(ctx, spreadsheetID, "AddTab")
	if err != nil {
		span.RecordError(err)
		return err
	}
	// SECURITY: the flag is checked before any Google call, so a non-writable document is never mutated and never probed through the write path.
	if !sp.Writable {
		refusal := &NotWritableError{ID: sp.ID.String(), GoogleFileID: sp.GoogleFileID}
		span.RecordError(refusal)
		return refusal
	}

	ts, err := s.writeTokens.TokenSourceFor(ctx, sp.CredentialID)
	if err != nil {
		span.RecordError(err)
		return s.translate(ctx, "spreadsheet.AddTab: token source", err, sp)
	}
	writer, err := s.writers(ctx, ts)
	if err != nil {
		span.RecordError(err)
		return s.translate(ctx, "spreadsheet.AddTab: build writer", err, sp)
	}
	if err := writer.AddTab(ctx, sp.GoogleFileID, title); err != nil {
		span.RecordError(err)
		return s.translate(ctx, "spreadsheet.AddTab: add tab", gwerr.AppError(err), sp)
	}
	return nil
}

func (s *Service) resolve(ctx context.Context, id uuid.UUID, op string) (*Spreadsheet, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	sp, err := s.store.ByID(ctx, id)
	if err != nil {
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "spreadsheet."+op+": byID", err, "spreadsheet_id", id)
	}
	if sp.OrgID != tc.OrgID || sp.ProjectID != tc.ProjectID {
		return nil, &NotFoundError{ID: id.String()}
	}
	return sp, nil
}

// NOTE: a Google or credential failure already carries a wire code and is an expected outcome the UI shows, so it passes through instead of being reported as an incident.
func (s *Service) translate(ctx context.Context, situation string, err error, sp *Spreadsheet) error {
	if _, ok := apperror.AsAppError(err); ok {
		return err
	}
	return s.unexpected(ctx, situation, err,
		"spreadsheet_id", sp.ID, "credential_id", sp.CredentialID)
}
