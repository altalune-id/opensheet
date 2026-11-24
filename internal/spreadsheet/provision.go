package spreadsheet

import (
	"context"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/gwerr"
	"altalune.id/opensheet/internal/platform/tenant"
)

const (
	maxProvisionKeyLen = 128
	maxProvisionTabs   = 10
	maxProvisionCols   = 50
	idHeader           = "id"
)

// ProvisionTab is one tab a Provision call creates, writes headers into and publishes under Slug.
type ProvisionTab struct {
	Title   string
	Slug    string
	Headers []string
}

// ProvisionRequest asks for a new Google spreadsheet in the caller's project, created through CredentialID.
type ProvisionRequest struct {
	CredentialID   uuid.UUID
	Title          string
	Tabs           []ProvisionTab
	IdempotencyKey string
}

// PublishedSheet is a sheet a Provision call published for one of its tabs.
type PublishedSheet struct {
	ID   uuid.UUID
	Slug string
	Tab  string
}

type publisher interface {
	Publish(ctx context.Context, spreadsheetID uuid.UUID, tab, slug string) (PublishedSheet, error)
	ValidSlug(slug string) error
}

// ProvisionWorkflow creates a Google spreadsheet, registers it under an idempotency key and publishes its tabs.
type ProvisionWorkflow struct {
	spreadsheets *Service
	writers      gsheet.WriterFactory
	writeTokens  TokenSources
	pub          publisher
	log          *slog.Logger
	unexpected   apperror.UnexpectedFunc
}

// NewProvisionWorkflow binds the workflow to the services and Google writer it drives.
func NewProvisionWorkflow(
	spreadsheets *Service,
	writers gsheet.WriterFactory,
	writeTokens TokenSources,
	pub publisher,
	log *slog.Logger,
	unexpected apperror.UnexpectedFunc,
) *ProvisionWorkflow {
	return &ProvisionWorkflow{
		spreadsheets: spreadsheets,
		writers:      writers,
		writeTokens:  writeTokens,
		pub:          pub,
		log:          log.With("module", "spreadsheet", "workflow", "provision"),
		unexpected:   unexpected,
	}
}

// Run provisions req in the caller's project scope; a retry under the same key resumes instead of creating a second file.
func (w *ProvisionWorkflow) Run(ctx context.Context, req ProvisionRequest) (*Spreadsheet, []PublishedSheet, error) {
	ctx, span := tracer.Start(ctx, "spreadsheet.Provision")
	defer span.End()

	if err := w.validate(req); err != nil {
		return nil, nil, err
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, nil, err
	}
	sp, err := w.existing(ctx, tc, req.IdempotencyKey)
	if err != nil {
		span.RecordError(err)
		return nil, nil, err
	}
	credentialID := req.CredentialID
	if sp != nil {
		credentialID = sp.CredentialID
	}
	writer, err := w.writer(ctx, credentialID)
	if err != nil {
		span.RecordError(err)
		return nil, nil, err
	}
	if sp == nil {
		sp, err = w.create(ctx, tc, writer, req)
		if err != nil {
			span.RecordError(err)
			return nil, nil, err
		}
	}
	if err := w.writeHeaders(ctx, writer, sp.GoogleFileID, req.Tabs); err != nil {
		span.RecordError(err)
		return nil, nil, err
	}
	sheets := make([]PublishedSheet, 0, len(req.Tabs))
	for _, tab := range req.Tabs {
		ps, pErr := w.pub.Publish(ctx, sp.ID, strings.TrimSpace(tab.Title), tab.Slug)
		if pErr != nil {
			span.RecordError(pErr)
			return nil, nil, pErr
		}
		sheets = append(sheets, ps)
	}
	return sp, sheets, nil
}

func (w *ProvisionWorkflow) existing(ctx context.Context, tc tenant.Context, key string) (*Spreadsheet, error) {
	sp, err := w.spreadsheets.store.ByProvisionKey(ctx, tc.OrgID, tc.ProjectID, key)
	if IsNotFoundError(err) {
		return nil, nil
	}
	if err != nil {
		return nil, w.unexpected(ctx, "spreadsheet.Provision: by key", err, "org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	return sp, nil
}

// SECURITY: the credential resolves under the caller's project scope, so a credential_id from another project creates nothing.
func (w *ProvisionWorkflow) writer(ctx context.Context, credentialID uuid.UUID) (*gsheet.Writer, error) {
	ts, err := w.writeTokens.TokenSourceFor(ctx, credentialID)
	if err != nil {
		return nil, w.passthrough(ctx, "spreadsheet.Provision: token source", err)
	}
	writer, err := w.writers(ctx, ts)
	if err != nil {
		return nil, w.passthrough(ctx, "spreadsheet.Provision: build writer", err)
	}
	return writer, nil
}

func (w *ProvisionWorkflow) create(ctx context.Context, tc tenant.Context, writer *gsheet.Writer, req ProvisionRequest) (*Spreadsheet, error) {
	titles := make([]string, len(req.Tabs))
	for i, tab := range req.Tabs {
		titles[i] = strings.TrimSpace(tab.Title)
	}
	fileID, err := writer.CreateSpreadsheet(ctx, strings.TrimSpace(req.Title), titles)
	if err != nil {
		return nil, w.passthrough(ctx, "spreadsheet.Provision: create", gwerr.AppError(err))
	}

	sp, err := New(tc.OrgID, tc.ProjectID, req.CredentialID, fileID, req.Title)
	if err != nil {
		return nil, err
	}
	sp.ProvisionKey = req.IdempotencyKey
	sp.Writable = true
	saveErr := w.spreadsheets.save(ctx, tc, sp, "spreadsheet.Provision: save")
	if !IsAlreadyExistsError(saveErr) {
		return sp, saveErr
	}
	winner, err := w.existing(ctx, tc, req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if winner == nil {
		return nil, saveErr
	}
	w.log.WarnContext(ctx, "spreadsheet: provision lost a same-key race; a Google file is orphaned",
		"org_id", tc.OrgID, "project_id", tc.ProjectID, "orphan_file_id", fileID)
	return winner, nil
}

func (w *ProvisionWorkflow) writeHeaders(ctx context.Context, writer *gsheet.Writer, fileID string, tabs []ProvisionTab) error {
	rows := make([]gsheet.HeaderRow, len(tabs))
	for i, tab := range tabs {
		cells := make([]any, len(tab.Headers))
		for j, h := range tab.Headers {
			cells[j] = strings.TrimSpace(h)
		}
		rows[i] = gsheet.HeaderRow{Tab: strings.TrimSpace(tab.Title), Cells: cells}
	}
	if err := writer.UpdateHeaders(ctx, fileID, rows); err != nil {
		return w.passthrough(ctx, "spreadsheet.Provision: headers", gwerr.AppError(err))
	}
	return nil
}

func (w *ProvisionWorkflow) passthrough(ctx context.Context, situation string, err error) error {
	if _, ok := apperror.AsAppError(err); ok {
		return err
	}
	return w.unexpected(ctx, situation, err)
}

func (w *ProvisionWorkflow) validate(req ProvisionRequest) error {
	if n := len(req.IdempotencyKey); n == 0 || n > maxProvisionKeyLen {
		return &ProvisionInvalidError{Field: "idempotency_key", Reason: "must be 1 to 128 characters"}
	}
	if _, err := validateTitle(req.Title); err != nil {
		return &ProvisionInvalidError{Field: "title", Reason: err.Error()}
	}
	if n := len(req.Tabs); n == 0 || n > maxProvisionTabs {
		return &ProvisionInvalidError{Field: "tabs", Reason: "must hold 1 to 10 tabs"}
	}
	titles := map[string]bool{}
	slugs := map[string]bool{}
	for _, tab := range req.Tabs {
		title := strings.TrimSpace(tab.Title)
		if title == "" || utf8.RuneCountInString(title) > gsheet.MaxTabTitleRunes {
			return &ProvisionInvalidError{Field: "tabs.title", Reason: "must be 1 to 100 characters"}
		}
		if titles[title] {
			return &ProvisionInvalidError{Field: "tabs.title", Reason: "duplicate tab " + title}
		}
		titles[title] = true
		if err := validateHeaders(tab.Headers); err != nil {
			return err
		}
		if err := w.pub.ValidSlug(tab.Slug); err != nil {
			return &ProvisionInvalidError{Field: "tabs.slug", Reason: err.Error()}
		}
		if slugs[tab.Slug] {
			return &ProvisionInvalidError{Field: "tabs.slug", Reason: "duplicate slug " + tab.Slug}
		}
		slugs[tab.Slug] = true
	}
	return nil
}

func validateHeaders(headers []string) error {
	if n := len(headers); n == 0 || n > maxProvisionCols {
		return &ProvisionInvalidError{Field: "tabs.headers", Reason: "must hold 1 to 50 headers"}
	}
	if _, warnings := gsheet.NormalizeHeaders(headers); len(warnings) > 0 {
		return &ProvisionInvalidError{Field: "tabs.headers", Reason: warnings[0]}
	}
	seen := map[string]bool{}
	for _, h := range headers {
		folded := strings.ToLower(strings.TrimSpace(h))
		if seen[folded] {
			return &ProvisionInvalidError{Field: "tabs.headers", Reason: "duplicate header " + strings.TrimSpace(h)}
		}
		seen[folded] = true
	}
	if !seen[idHeader] {
		return &ProvisionInvalidError{Field: "tabs.headers", Reason: "needs an id column"}
	}
	return nil
}
