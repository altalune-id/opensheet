package api_test

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/durationpb"

	sheetv1 "altalune.id/opensheet/gen/go/sheet/v1"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/sheet"
)

func (h *harness) seedSheet(orgID, projectID uuid.UUID, slug string) *sheet.Sheet {
	h.t.Helper()
	sh, err := sheet.New(orgID, projectID, uuid.New(), "Q1", slug, sheet.VisibilityKey, time.Minute)
	if err != nil {
		h.t.Fatalf("sheet.New: %v", err)
	}
	if err := h.sheets.Save(h.t.Context(), sh); err != nil {
		h.t.Fatalf("save sheet: %v", err)
	}
	return sh
}

func TestSheet_CreateGetListUpdateDelete_HappyPath(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)
	client := h.sheetClient()
	sprdID := uuid.New()

	create := connect.NewRequest(&sheetv1.CreateRequest{
		ProjectId:     proj.ID.String(),
		SpreadsheetId: sprdID.String(),
		Tab:           "Q1",
		Slug:          "prices",
		Visibility:    string(sheet.VisibilityKey),
		CacheTtl:      durationpb.New(2 * time.Minute),
	})
	withBearer(create.Header())
	createResp, err := client.Create(t.Context(), create)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	made := createResp.Msg.GetSheet()
	if made.GetSlug() != "prices" || made.GetTab() != "Q1" {
		t.Errorf("created = %+v", made)
	}
	if made.GetVisibility() != string(sheet.VisibilityKey) {
		t.Errorf("visibility = %q", made.GetVisibility())
	}
	if got := made.GetCacheTtl().AsDuration(); got != 2*time.Minute {
		t.Errorf("cache_ttl = %v, want 2m", got)
	}
	if made.GetSpreadsheetId() != sprdID.String() || made.GetProjectId() != proj.ID.String() {
		t.Errorf("ids not bound: %+v", made)
	}

	get := connect.NewRequest(&sheetv1.GetRequest{ProjectId: proj.ID.String(), SheetId: made.GetId()})
	withBearer(get.Header())
	getResp, err := client.Get(t.Context(), get)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if getResp.Msg.GetSheet().GetId() != made.GetId() {
		t.Error("Get returned another sheet")
	}

	list := connect.NewRequest(&sheetv1.ListRequest{ProjectId: proj.ID.String()})
	withBearer(list.Header())
	listResp, err := client.List(t.Context(), list)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if n := len(listResp.Msg.GetSheets()); n != 1 {
		t.Fatalf("sheets len = %d, want 1", n)
	}

	newTab, newVis := "Q2", string(sheet.VisibilityPublic)
	update := connect.NewRequest(&sheetv1.UpdateRequest{
		ProjectId:  proj.ID.String(),
		SheetId:    made.GetId(),
		Tab:        &newTab,
		Visibility: &newVis,
		CacheTtl:   durationpb.New(5 * time.Minute),
	})
	withBearer(update.Header())
	updateResp, err := client.Update(t.Context(), update)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	got := updateResp.Msg.GetSheet()
	if got.GetTab() != newTab || got.GetVisibility() != newVis {
		t.Errorf("updated = %+v", got)
	}
	if d := got.GetCacheTtl().AsDuration(); d != 5*time.Minute {
		t.Errorf("cache_ttl = %v, want 5m", d)
	}

	del := connect.NewRequest(&sheetv1.DeleteRequest{ProjectId: proj.ID.String(), SheetId: made.GetId()})
	withBearer(del.Header())
	if _, err := client.Delete(t.Context(), del); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if left, _ := h.sheets.List(t.Context(), orgID, proj.ID); len(left) != 0 {
		t.Errorf("sheets left = %d, want 0", len(left))
	}
}

func TestSheet_PurgeCache_DropsEveryTabOfTheSheet(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)
	sh := h.seedSheet(orgID, proj.ID, "prices")
	h.snaps.Seed(sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}, sheet.Snapshot{ETag: "e", Payload: []byte("[]")})

	req := connect.NewRequest(&sheetv1.PurgeCacheRequest{ProjectId: proj.ID.String(), SheetId: sh.ID.String()})
	withBearer(req.Header())
	if _, err := h.sheetClient().PurgeCache(t.Context(), req); err != nil {
		t.Fatalf("PurgeCache: %v", err)
	}
	if _, ok, _ := h.snaps.Get(t.Context(), sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}); ok {
		t.Error("snapshot survived PurgeCache")
	}
}

func TestSheet_List_CrossOrgProject_ReturnsPermissionDenied(t *testing.T) {
	h := newHarness(t, humanPrincipal(uuid.New()))
	proj := h.seedProject(uuid.New())

	req := connect.NewRequest(&sheetv1.ListRequest{ProjectId: proj.ID.String()})
	withBearer(req.Header())
	if _, err := h.sheetClient().List(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodePermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", got)
	}
}

func TestSheet_List_MissingPrincipal_ReturnsUnauthenticated(t *testing.T) {
	h := newHarness(t, session.Principal{})

	req := connect.NewRequest(&sheetv1.ListRequest{ProjectId: uuid.New().String()})
	if _, err := h.sheetClient().List(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

func TestSheet_PurgeCache_APIKeyPrincipal_Succeeds(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, keyPrincipal(orgID))
	proj := h.seedProject(orgID)
	sh := h.seedSheet(orgID, proj.ID, "prices")

	req := connect.NewRequest(&sheetv1.PurgeCacheRequest{ProjectId: proj.ID.String(), SheetId: sh.ID.String()})
	withBearer(req.Header())
	if _, err := h.sheetClient().PurgeCache(t.Context(), req); err != nil {
		t.Fatalf("PurgeCache with an api-key principal: %v", err)
	}
}

func TestSheet_Create_InvalidVisibility_ReturnsInvalidArgument(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)

	req := connect.NewRequest(&sheetv1.CreateRequest{
		ProjectId:     proj.ID.String(),
		SpreadsheetId: uuid.New().String(),
		Slug:          "prices",
		Visibility:    "everyone",
	})
	withBearer(req.Header())
	if _, err := h.sheetClient().Create(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got)
	}
}

func TestSheet_Get_UnknownID_ReturnsNotFound(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)

	req := connect.NewRequest(&sheetv1.GetRequest{ProjectId: proj.ID.String(), SheetId: uuid.New().String()})
	withBearer(req.Header())
	if _, err := h.sheetClient().Get(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeNotFound {
		t.Errorf("code = %v, want NotFound", got)
	}
}

func TestSheet_Update_WithNoFieldsSet_LeavesTheRowAlone(t *testing.T) {
	orgID := uuid.New()
	h := newHarness(t, humanPrincipal(orgID))
	proj := h.seedProject(orgID)
	sh := h.seedSheet(orgID, proj.ID, "prices")

	req := connect.NewRequest(&sheetv1.UpdateRequest{ProjectId: proj.ID.String(), SheetId: sh.ID.String()})
	withBearer(req.Header())
	resp, err := h.sheetClient().Update(t.Context(), req)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	got := resp.Msg.GetSheet()
	if got.GetTab() != sh.Tab || got.GetVisibility() != string(sh.Visibility) {
		t.Errorf("row changed: %+v", got)
	}
	if d := got.GetCacheTtl().AsDuration(); d != sh.CacheTTL {
		t.Errorf("cache_ttl = %v, want %v", d, sh.CacheTTL)
	}
}
