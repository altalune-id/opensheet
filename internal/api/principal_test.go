package api_test

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	apikeyv1 "altalune.id/opensheet/gen/go/apikey/v1"
	credentialv1 "altalune.id/opensheet/gen/go/credential/v1"
	sheetv1 "altalune.id/opensheet/gen/go/sheet/v1"
	spreadsheetv1 "altalune.id/opensheet/gen/go/spreadsheet/v1"
	todov1 "altalune.id/opensheet/gen/go/todo/v1"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/project"
)

func TestPrincipal_AcceptsAnAPIKeySource(t *testing.T) {
	orgID := uuid.New()
	p := session.Principal{
		Name:            "ci-key",
		Source:          session.SourceAPIKey,
		ActiveOrgID:     orgID,
		ActiveProjectID: uuid.New(),
		Scopes:          []string{"sheets:read"},
	}
	h := newHarness(t, p)

	proj, err := project.New(orgID, "p1", "Project 1")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.projs.Save(t.Context(), proj); err != nil {
		t.Fatal(err)
	}

	req := connect.NewRequest(&todov1.CreateRequest{ProjectId: proj.ID.String(), Title: "from a machine"})
	withBearer(req.Header())

	resp, err := h.authClient().Create(t.Context(), req)
	if err != nil {
		t.Fatalf("Create with an api-key principal: %v", err)
	}
	if resp.Msg.Todo.ProjectId != proj.ID.String() {
		t.Errorf("project_id = %q, want %q", resp.Msg.Todo.ProjectId, proj.ID.String())
	}
}

func TestPrincipal_RejectsAnAPIKeyWithoutAnOrg(t *testing.T) {
	h := newHarness(t, session.Principal{Source: session.SourceAPIKey, Name: "orphan-key"})

	req := connect.NewRequest(&todov1.ListRequest{ProjectId: uuid.New().String()})
	withBearer(req.Header())

	if _, err := h.authClient().List(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

func TestPrincipal_RejectsAnEmptyPrincipal(t *testing.T) {
	h := newHarness(t, session.Principal{})

	req := connect.NewRequest(&todov1.ListRequest{ProjectId: uuid.New().String()})
	withBearer(req.Header())

	if _, err := h.authClient().List(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

func TestPrincipal_RejectsAHumanSourceWithoutAUserID(t *testing.T) {
	h := newHarness(t, session.Principal{Source: session.SourceToken, Email: "a@b", ActiveOrgID: uuid.New()})

	req := connect.NewRequest(&todov1.ListRequest{ProjectId: uuid.New().String()})
	withBearer(req.Header())

	if _, err := h.authClient().List(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

// NOTE: every request id is parsed before it reaches a domain service, so a malformed one is InvalidArgument, not NotFound.
func TestControlPlane_MalformedIDsReturnInvalidArgument(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)
	pid := proj.ID.String()
	badCred := "not-a-uuid"

	calls := map[string]func() error{
		"credential.Delete/credential_id": func() error {
			req := connect.NewRequest(&credentialv1.DeleteRequest{ProjectId: pid, CredentialId: badCred})
			withBearer(req.Header())
			_, err := h.credentialClient().Delete(t.Context(), req)
			return err
		},
		"spreadsheet.Create/credential_id": func() error {
			req := connect.NewRequest(&spreadsheetv1.CreateRequest{
				ProjectId: pid, CredentialId: badCred, GoogleFileId: googleFileID, Title: "Prices",
			})
			withBearer(req.Header())
			_, err := h.spreadsheetClient().Create(t.Context(), req)
			return err
		},
		"spreadsheet.Update/credential_id": func() error {
			sp := h.seedSpreadsheet(orgID, proj.ID)
			req := connect.NewRequest(&spreadsheetv1.UpdateRequest{
				ProjectId: pid, SpreadsheetId: sp.ID.String(), CredentialId: &badCred,
			})
			withBearer(req.Header())
			_, err := h.spreadsheetClient().Update(t.Context(), req)
			return err
		},
		"spreadsheet.ListTabs/spreadsheet_id": func() error {
			req := connect.NewRequest(&spreadsheetv1.ListTabsRequest{ProjectId: pid, SpreadsheetId: badCred})
			withBearer(req.Header())
			_, err := h.spreadsheetClient().ListTabs(t.Context(), req)
			return err
		},
		"spreadsheet.Delete/spreadsheet_id": func() error {
			req := connect.NewRequest(&spreadsheetv1.DeleteRequest{ProjectId: pid, SpreadsheetId: badCred})
			withBearer(req.Header())
			_, err := h.spreadsheetClient().Delete(t.Context(), req)
			return err
		},
		"sheet.Create/spreadsheet_id": func() error {
			req := connect.NewRequest(&sheetv1.CreateRequest{
				ProjectId: pid, SpreadsheetId: badCred, Slug: "prices", Visibility: "key",
			})
			withBearer(req.Header())
			_, err := h.sheetClient().Create(t.Context(), req)
			return err
		},
		"sheet.Delete/sheet_id": func() error {
			req := connect.NewRequest(&sheetv1.DeleteRequest{ProjectId: pid, SheetId: badCred})
			withBearer(req.Header())
			_, err := h.sheetClient().Delete(t.Context(), req)
			return err
		},
		"sheet.PurgeCache/sheet_id": func() error {
			req := connect.NewRequest(&sheetv1.PurgeCacheRequest{ProjectId: pid, SheetId: badCred})
			withBearer(req.Header())
			_, err := h.sheetClient().PurgeCache(t.Context(), req)
			return err
		},
		"sheet.Update/sheet_id": func() error {
			req := connect.NewRequest(&sheetv1.UpdateRequest{ProjectId: pid, SheetId: badCred})
			withBearer(req.Header())
			_, err := h.sheetClient().Update(t.Context(), req)
			return err
		},
		"apikey.Create/sheet_ids": func() error {
			req := connect.NewRequest(&apikeyv1.CreateRequest{
				ProjectId: pid, Name: "ci", Scopes: []string{"sheets:read"}, SheetIds: []string{badCred},
			})
			withBearer(req.Header())
			_, err := h.apiKeyClient().Create(t.Context(), req)
			return err
		},
		"apikey.Revoke/api_key_id": func() error {
			req := connect.NewRequest(&apikeyv1.RevokeRequest{ProjectId: pid, ApiKeyId: badCred})
			withBearer(req.Header())
			_, err := h.apiKeyClient().Revoke(t.Context(), req)
			return err
		},
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil {
				t.Fatal("expected error")
			}
			if got := connectCode(err); got != connect.CodeInvalidArgument {
				t.Errorf("code = %v, want InvalidArgument", got)
			}
		})
	}
}
