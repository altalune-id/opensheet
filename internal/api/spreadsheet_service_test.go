package api_test

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	spreadsheetv1 "altalune.id/opensheet/gen/go/spreadsheet/v1"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/spreadsheet"
)

const googleFileID = "1AbC-_dEf"

func (h *harness) seedSpreadsheet(orgID, projectID uuid.UUID) *spreadsheet.Spreadsheet {
	h.t.Helper()
	sp, err := spreadsheet.New(orgID, projectID, uuid.New(), googleFileID, "Prices")
	if err != nil {
		h.t.Fatalf("spreadsheet.New: %v", err)
	}
	if err := h.sprds.Save(h.t.Context(), sp); err != nil {
		h.t.Fatalf("save spreadsheet: %v", err)
	}
	return sp
}

func TestSpreadsheet_CreateGetListUpdateDelete_HappyPath(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)
	client := h.spreadsheetClient()
	credID := uuid.New()

	create := connect.NewRequest(&spreadsheetv1.CreateRequest{
		ProjectId:    proj.ID.String(),
		CredentialId: credID.String(),
		GoogleFileId: googleFileID,
		Title:        "  Prices  ",
	})
	withBearer(create.Header())
	createResp, err := client.Create(t.Context(), create)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	made := createResp.Msg.GetSpreadsheet()
	if made.GetTitle() != "Prices" || made.GetGoogleFileId() != googleFileID {
		t.Errorf("created = %+v", made)
	}
	if made.GetCredentialId() != credID.String() || made.GetProjectId() != proj.ID.String() {
		t.Errorf("ids not bound: %+v", made)
	}

	get := connect.NewRequest(&spreadsheetv1.GetRequest{ProjectId: proj.ID.String(), SpreadsheetId: made.GetId()})
	withBearer(get.Header())
	getResp, err := client.Get(t.Context(), get)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if getResp.Msg.GetSpreadsheet().GetId() != made.GetId() {
		t.Error("Get returned another spreadsheet")
	}

	list := connect.NewRequest(&spreadsheetv1.ListRequest{ProjectId: proj.ID.String()})
	withBearer(list.Header())
	listResp, err := client.List(t.Context(), list)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if n := len(listResp.Msg.GetSpreadsheets()); n != 1 {
		t.Fatalf("spreadsheets len = %d, want 1", n)
	}

	newTitle, newCred := "Q1 prices", uuid.New().String()
	update := connect.NewRequest(&spreadsheetv1.UpdateRequest{
		ProjectId:     proj.ID.String(),
		SpreadsheetId: made.GetId(),
		Title:         &newTitle,
		CredentialId:  &newCred,
	})
	withBearer(update.Header())
	updateResp, err := client.Update(t.Context(), update)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := updateResp.Msg.GetSpreadsheet(); got.GetTitle() != newTitle || got.GetCredentialId() != newCred {
		t.Errorf("updated = %+v", got)
	}

	del := connect.NewRequest(&spreadsheetv1.DeleteRequest{ProjectId: proj.ID.String(), SpreadsheetId: made.GetId()})
	withBearer(del.Header())
	if _, err := client.Delete(t.Context(), del); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if left, _ := h.sprds.List(t.Context(), orgID, proj.ID); len(left) != 0 {
		t.Errorf("spreadsheets left = %d, want 0", len(left))
	}
}

func TestSpreadsheet_Update_WithNoFieldsSet_LeavesTheRowAlone(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)
	sp := h.seedSpreadsheet(orgID, proj.ID)

	req := connect.NewRequest(&spreadsheetv1.UpdateRequest{
		ProjectId:     proj.ID.String(),
		SpreadsheetId: sp.ID.String(),
	})
	withBearer(req.Header())
	resp, err := h.spreadsheetClient().Update(t.Context(), req)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := resp.Msg.GetSpreadsheet(); got.GetTitle() != sp.Title || got.GetCredentialId() != sp.CredentialID.String() {
		t.Errorf("row changed: %+v", got)
	}
}

func TestSpreadsheet_ListTabs_ReturnsGoogleTabTitles(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)

	sp := h.seedSpreadsheet(orgID, proj.ID)

	req := connect.NewRequest(&spreadsheetv1.ListTabsRequest{ProjectId: proj.ID.String(), SpreadsheetId: sp.ID.String()})
	withBearer(req.Header())
	resp, err := h.spreadsheetClient().ListTabs(t.Context(), req)
	if err != nil {
		t.Fatalf("ListTabs: %v", err)
	}
	if got := resp.Msg.GetTabs(); len(got) != 2 || got[0] != "Q1" || got[1] != "Q2" {
		t.Errorf("tabs = %v, want [Q1 Q2]", got)
	}
}

func TestSpreadsheet_List_CrossOrgProject_ReturnsPermissionDenied(t *testing.T) {
	h := newHarness(t, humanPrincipal(uuid.New()))
	proj := h.seedProject(uuid.New())

	req := connect.NewRequest(&spreadsheetv1.ListRequest{ProjectId: proj.ID.String()})
	withBearer(req.Header())
	if _, err := h.spreadsheetClient().List(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodePermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", got)
	}
}

func TestSpreadsheet_List_MissingPrincipal_ReturnsUnauthenticated(t *testing.T) {
	h := newHarness(t, session.Principal{})

	req := connect.NewRequest(&spreadsheetv1.ListRequest{ProjectId: uuid.New().String()})
	if _, err := h.spreadsheetClient().List(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

func TestSpreadsheet_Create_APIKeyPrincipal_Succeeds(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, keyPrincipal(orgID))
	proj := h.seedProject(orgID)

	req := connect.NewRequest(&spreadsheetv1.CreateRequest{
		ProjectId:    proj.ID.String(),
		CredentialId: uuid.New().String(),
		GoogleFileId: googleFileID,
		Title:        "Prices",
	})
	withBearer(req.Header())
	if _, err := h.spreadsheetClient().Create(t.Context(), req); err != nil {
		t.Fatalf("Create with an api-key principal: %v", err)
	}
}

func TestSpreadsheet_Get_UnknownID_ReturnsNotFound(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)

	req := connect.NewRequest(&spreadsheetv1.GetRequest{ProjectId: proj.ID.String(), SpreadsheetId: uuid.New().String()})
	withBearer(req.Header())
	if _, err := h.spreadsheetClient().Get(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeNotFound {
		t.Errorf("code = %v, want NotFound", got)
	}
}

func TestSpreadsheet_Get_InvalidUUID_ReturnsInvalidArgument(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)

	req := connect.NewRequest(&spreadsheetv1.GetRequest{ProjectId: proj.ID.String(), SpreadsheetId: "not-a-uuid"})
	withBearer(req.Header())
	if _, err := h.spreadsheetClient().Get(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got)
	}
}

func TestSpreadsheet_Create_EmptyGoogleFileID_ReturnsInvalidArgument(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)

	req := connect.NewRequest(&spreadsheetv1.CreateRequest{
		ProjectId:    proj.ID.String(),
		CredentialId: uuid.New().String(),
		GoogleFileId: "  ",
		Title:        "Prices",
	})
	withBearer(req.Header())
	if _, err := h.spreadsheetClient().Create(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got)
	}
}

func TestSpreadsheet_Update_UnknownID_ReturnsNotFound(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)

	title := "Renamed"
	req := connect.NewRequest(&spreadsheetv1.UpdateRequest{
		ProjectId:     proj.ID.String(),
		SpreadsheetId: uuid.New().String(),
		Title:         &title,
	})
	withBearer(req.Header())
	if _, err := h.spreadsheetClient().Update(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeNotFound {
		t.Errorf("code = %v, want NotFound", got)
	}
}
