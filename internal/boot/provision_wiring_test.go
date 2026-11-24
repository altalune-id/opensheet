package boot_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/authn"
)

const provisionProcedure = "/api/spreadsheet.v1.SpreadsheetService/Provision"

// TestServicesOpensheet_ProvisionWired proves boot hands the workflow to the RPC: a bad slug is judged by the sheet publisher it was built with.
func TestServicesOpensheet_ProvisionWired(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{})
	token := f.issuer.mint(t, controlAudience, []string{authn.ScopeSheetsAdmin, authn.ScopeSpreadsheetsWrite})

	rec := f.post(t, provisionProcedure, token, map[string]any{
		"projectId": f.projectID, "credentialId": uuid.NewString(), "title": "Budget", "idempotencyKey": "connect-1",
		"tabs": []map[string]any{{"title": "Q1", "slug": "Bad Slug", "headers": []string{"id"}}},
	})

	require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
	require.Contains(t, rec.Body.String(), "Invalid provision request: tabs.slug", "the slug rule comes from the wired sheet publisher")
}
