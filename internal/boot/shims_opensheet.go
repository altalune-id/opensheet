package boot

import (
	"context"

	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/spreadsheet"
)

type capsForSheet struct{ caps capabilities.Capabilities }

func (c capsForSheet) PublicSheetsEnabled() bool { return c.caps.PublicSheets }

type spreadsheetsForSheet struct{ svc *spreadsheet.Service }

func (s spreadsheetsForSheet) SourceFor(ctx context.Context, spreadsheetID uuid.UUID) (sheet.Source, error) {
	sp, err := s.svc.ByID(ctx, spreadsheetID)
	if err != nil {
		return sheet.Source{}, err
	}
	return sheet.Source{GoogleFileID: sp.GoogleFileID, CredentialID: sp.CredentialID}, nil
}

type tokensForSheetRead struct{ svc *credential.Service }

func (t tokensForSheetRead) TokenSourceFor(ctx context.Context, id uuid.UUID) (oauth2.TokenSource, error) {
	return t.svc.TokenSourceFor(ctx, id, gsheet.ScopeReadOnly)
}

type tokensForSheetWrite struct{ svc *credential.Service }

func (t tokensForSheetWrite) TokenSourceFor(ctx context.Context, id uuid.UUID) (oauth2.TokenSource, error) {
	return t.svc.TokenSourceFor(ctx, id, gsheet.ScopeReadWrite)
}

type tokensForSpreadsheetRead struct{ svc *credential.Service }

func (t tokensForSpreadsheetRead) TokenSourceFor(ctx context.Context, id uuid.UUID) (oauth2.TokenSource, error) {
	return t.svc.TokenSourceFor(ctx, id, gsheet.ScopeReadOnly)
}

type tokensForSpreadsheetWrite struct{ svc *credential.Service }

func (t tokensForSpreadsheetWrite) TokenSourceFor(ctx context.Context, id uuid.UUID) (oauth2.TokenSource, error) {
	return t.svc.TokenSourceFor(ctx, id, gsheet.ScopeReadWrite)
}

var (
	_ sheet.TokenSources       = tokensForSheetRead{}
	_ sheet.TokenSources       = tokensForSheetWrite{}
	_ spreadsheet.TokenSources = tokensForSpreadsheetRead{}
	_ spreadsheet.TokenSources = tokensForSpreadsheetWrite{}
)

type credentialsForSheet struct{ svc *credential.Service }

func (c credentialsForSheet) MarkReauthNeeded(ctx context.Context, credentialID uuid.UUID) error {
	_, err := c.svc.MarkReauthNeeded(ctx, credentialID)
	return err
}

type sheetsForData struct{ svc *sheet.Service }

// NOTE: sheet.Service.BySlug reads org and project off the context, so the shim scopes them explicitly.
func (s sheetsForData) BySlug(ctx context.Context, orgID, projectID uuid.UUID, slug string) (*sheet.Sheet, error) {
	scoped := tenant.Into(ctx, tenant.Context{OrgID: orgID, ProjectID: projectID})
	return s.svc.BySlug(scoped, slug)
}
