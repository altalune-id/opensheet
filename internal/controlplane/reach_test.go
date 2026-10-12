package controlplane_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	apikeyv1 "altalune.id/opensheet/gen/go/apikey/v1"
	credentialv1 "altalune.id/opensheet/gen/go/credential/v1"
	projectv1 "altalune.id/opensheet/gen/go/project/v1"
	sheetv1 "altalune.id/opensheet/gen/go/sheet/v1"
	spreadsheetv1 "altalune.id/opensheet/gen/go/spreadsheet/v1"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/spreadsheet"
)

type reachFixture struct {
	h                *osHarness
	orgID            uuid.UUID
	own, sibling     *project.Project
	ownSheet         *sheet.Sheet
	siblingSheet     *sheet.Sheet
	siblingWorkbook  *spreadsheet.Spreadsheet
	siblingTabBefore string
}

// SECURITY: the fakes filter by org only, so the handler's reach check is the sole guard these tests exercise.
func newReachFixture(t *testing.T) *reachFixture {
	t.Helper()
	ctx := context.Background()
	orgID := uuid.New()
	h := newOSHarnessWithKeys(t, humanPrincipal(orgID))

	own, err := project.New(orgID, "own", "Own")
	require.NoError(t, err)
	require.NoError(t, h.projs.Save(ctx, own))
	sibling, err := project.New(orgID, "sibling", "Sibling")
	require.NoError(t, err)
	require.NoError(t, h.projs.Save(ctx, sibling))

	f := &reachFixture{h: h, orgID: orgID, own: own, sibling: sibling}
	f.ownSheet = f.seedSheet(t, own, "own-sheet")
	f.siblingSheet = f.seedSheet(t, sibling, "sibling-sheet")
	f.siblingTabBefore = f.siblingSheet.Tab

	wb, err := spreadsheet.New(orgID, sibling.ID, uuid.New(), "FILE-sibling", "Sibling Workbook")
	require.NoError(t, err)
	require.NoError(t, h.sprds.Save(ctx, wb))
	f.siblingWorkbook = wb
	return f
}

func (f *reachFixture) seedSheet(t *testing.T, proj *project.Project, slug string) *sheet.Sheet {
	t.Helper()
	sh, err := sheet.New(sheet.NewParams{
		OrgID: proj.OrgID, ProjectID: proj.ID, SpreadsheetID: uuid.New(),
		Tab: "Q1", Slug: slug, Visibility: sheet.VisibilityKey, CacheTTL: time.Minute,
	})
	require.NoError(t, err)
	require.NoError(t, f.h.sheets.Save(context.Background(), sh))
	return sh
}

func (f *reachFixture) key(scopes []string, resourceIDs ...uuid.UUID) string {
	return f.h.mintKey(f.orgID, f.own.ID, scopes, resourceIDs)
}

func allScopes() []string {
	return []string{
		authn.ScopeSheetsRead, authn.ScopeSheetsWrite, authn.ScopeSheetsAdmin, authn.ScopeCachePurge,
		authn.ScopeSpreadsheetsRead, authn.ScopeSpreadsheetsWrite,
		authn.ScopeCredentialsRead, authn.ScopeCredentialsWrite,
		authn.ScopeAPIKeysRead, authn.ScopeAPIKeysWrite,
	}
}

func keyed[T any](key string, msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	withKey(key)(req.Header())
	return req
}

// TestKeyNeverReachesASiblingProject pins a key to the project it was minted in. SECURITY: every row names a verb that took a sibling project's id; revert the reach check and each one fails.
func TestKeyNeverReachesASiblingProject(t *testing.T) {
	f := newReachFixture(t)
	key := f.key(allScopes())
	sibling := f.sibling.ID.String()
	siblingSheet := f.siblingSheet.ID.String()
	siblingWorkbook := f.siblingWorkbook.ID.String()
	sheets, sprds, creds := f.h.sheetClient(), f.h.spreadsheetClient(), f.h.credentialClient()

	tests := []struct {
		name string
		call func(context.Context) error
		want connect.Code
	}{
		{"SheetList", func(ctx context.Context) error {
			_, err := sheets.List(ctx, keyed(key, &sheetv1.ListRequest{ProjectId: sibling}))
			return err
		}, connect.CodePermissionDenied},
		{"SheetCreate", func(ctx context.Context) error {
			_, err := sheets.Create(ctx, keyed(key, &sheetv1.CreateRequest{
				ProjectId: sibling, SpreadsheetId: siblingWorkbook, Tab: "Q1", Slug: "escalate", Visibility: string(sheet.VisibilityKey)}))
			return err
		}, connect.CodePermissionDenied},
		{"SheetGet", func(ctx context.Context) error {
			_, err := sheets.Get(ctx, keyed(key, &sheetv1.GetRequest{ProjectId: sibling, SheetId: siblingSheet}))
			return err
		}, connect.CodeNotFound},
		{"SheetUpdate", func(ctx context.Context) error {
			tab := "Q2"
			_, err := sheets.Update(ctx, keyed(key, &sheetv1.UpdateRequest{ProjectId: sibling, SheetId: siblingSheet, Tab: &tab}))
			return err
		}, connect.CodeNotFound},
		{"SheetPurgeCache", func(ctx context.Context) error {
			_, err := sheets.PurgeCache(ctx, keyed(key, &sheetv1.PurgeCacheRequest{ProjectId: sibling, SheetId: siblingSheet}))
			return err
		}, connect.CodeNotFound},
		{"SheetDelete", func(ctx context.Context) error {
			_, err := sheets.Delete(ctx, keyed(key, &sheetv1.DeleteRequest{ProjectId: sibling, SheetId: siblingSheet}))
			return err
		}, connect.CodeNotFound},
		{"SpreadsheetList", func(ctx context.Context) error {
			_, err := sprds.List(ctx, keyed(key, &spreadsheetv1.ListRequest{ProjectId: sibling}))
			return err
		}, connect.CodePermissionDenied},
		{"SpreadsheetGet", func(ctx context.Context) error {
			_, err := sprds.Get(ctx, keyed(key, &spreadsheetv1.GetRequest{ProjectId: sibling, SpreadsheetId: siblingWorkbook}))
			return err
		}, connect.CodePermissionDenied},
		{"SpreadsheetDelete", func(ctx context.Context) error {
			_, err := sprds.Delete(ctx, keyed(key, &spreadsheetv1.DeleteRequest{ProjectId: sibling, SpreadsheetId: siblingWorkbook}))
			return err
		}, connect.CodePermissionDenied},
		{"CredentialList", func(ctx context.Context) error {
			_, err := creds.List(ctx, keyed(key, &credentialv1.ListRequest{ProjectId: sibling}))
			return err
		}, connect.CodePermissionDenied},
		{"CredentialDelete", func(ctx context.Context) error {
			_, err := creds.Delete(ctx, keyed(key, &credentialv1.DeleteRequest{ProjectId: sibling, CredentialId: uuid.NewString()}))
			return err
		}, connect.CodePermissionDenied},
		{"APIKeyCreate", func(ctx context.Context) error {
			_, err := f.h.apikeyClient().Create(ctx, keyed(key, &apikeyv1.CreateRequest{
				ExpiresAt: timestamppb.New(time.Now().Add(24 * time.Hour)), ProjectId: sibling, Name: "escalate", Scopes: []string{authn.ScopeSheetsRead}}))
			return err
		}, connect.CodePermissionDenied},
		{"APIKeyList", func(ctx context.Context) error {
			_, err := f.h.apikeyClient().List(ctx, keyed(key, &apikeyv1.ListRequest{ProjectId: sibling}))
			return err
		}, connect.CodePermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(t.Context())
			require.Error(t, err, "a key minted for one project reached its sibling")
			require.Equal(t, tt.want, connectCode(err), "err=%v", err)
		})
	}

	stored, err := f.h.sheets.ByID(context.Background(), f.siblingSheet.ID)
	require.NoError(t, err, "the sibling sheet must survive")
	require.Equal(t, f.siblingTabBefore, stored.Tab, "the sibling sheet must be left untouched")
	_, err = f.h.sprds.ByID(context.Background(), f.siblingWorkbook.ID)
	require.NoError(t, err, "the sibling spreadsheet must survive")
	siblings, err := f.h.sheets.List(context.Background(), f.orgID, f.sibling.ID)
	require.NoError(t, err)
	require.Len(t, siblings, 1, "no sheet may be published into the sibling project")
}

func TestKeyStillReachesItsOwnProject(t *testing.T) {
	f := newReachFixture(t)
	key := f.key(allScopes())
	own := f.own.ID.String()

	_, err := f.h.sheetClient().List(t.Context(), keyed(key, &sheetv1.ListRequest{ProjectId: own}))
	require.NoError(t, err)

	_, err = f.h.sheetClient().Get(t.Context(), keyed(key, &sheetv1.GetRequest{ProjectId: own, SheetId: f.ownSheet.ID.String()}))
	require.NoError(t, err)

	_, err = f.h.spreadsheetClient().List(t.Context(), keyed(key, &spreadsheetv1.ListRequest{ProjectId: own}))
	require.NoError(t, err)

	_, err = f.h.credentialClient().List(t.Context(), keyed(key, &credentialv1.ListRequest{ProjectId: own}))
	require.NoError(t, err)
}

// SECURITY: project_list hands an agent the ids every other tool takes, so a key must see only the projects it reaches.
func TestKeyProjectListShowsOnlyReachableProjects(t *testing.T) {
	f := newReachFixture(t)
	key := f.key(allScopes())

	resp, err := f.h.projectClient().ListProjects(t.Context(), keyed(key, &projectv1.ListProjectsRequest{}))
	require.NoError(t, err)

	ids := make([]string, 0, len(resp.Msg.GetProjects()))
	for _, p := range resp.Msg.GetProjects() {
		ids = append(ids, p.GetId())
	}
	require.Equal(t, []string{f.own.ID.String()}, ids)
}

func TestPersonStillReachesEverySiblingProject(t *testing.T) {
	f := newReachFixture(t)

	listReq := connect.NewRequest(&sheetv1.ListRequest{ProjectId: f.sibling.ID.String()})
	withBearer(listReq.Header())
	_, err := f.h.sheetClient().List(t.Context(), listReq)
	require.NoError(t, err, "org membership reaches every project in the org")

	getReq := connect.NewRequest(&sheetv1.GetRequest{ProjectId: f.sibling.ID.String(), SheetId: f.siblingSheet.ID.String()})
	withBearer(getReq.Header())
	_, err = f.h.sheetClient().Get(t.Context(), getReq)
	require.NoError(t, err)

	projReq := connect.NewRequest(&projectv1.ListProjectsRequest{})
	withBearer(projReq.Header())
	resp, err := f.h.projectClient().ListProjects(t.Context(), projReq)
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetProjects(), 2)
}

// SECURITY: a key restricted to named resources reaches only those, and never a project-wide verb.
func TestResourceBoundKeyReachesOnlyItsResources(t *testing.T) {
	f := newReachFixture(t)
	other := f.seedSheet(t, f.own, "other-sheet")
	key := f.key(allScopes(), f.ownSheet.ID)
	own := f.own.ID.String()

	_, err := f.h.sheetClient().Get(t.Context(), keyed(key, &sheetv1.GetRequest{ProjectId: own, SheetId: f.ownSheet.ID.String()}))
	require.NoError(t, err, "the named sheet must stay reachable")

	_, err = f.h.sheetClient().PurgeCache(t.Context(), keyed(key, &sheetv1.PurgeCacheRequest{ProjectId: own, SheetId: f.ownSheet.ID.String()}))
	require.NoError(t, err, "the named sheet's cache must stay purgeable")

	_, err = f.h.sheetClient().Get(t.Context(), keyed(key, &sheetv1.GetRequest{ProjectId: own, SheetId: other.ID.String()}))
	require.Equal(t, connect.CodeNotFound, connectCode(err), "an unnamed sheet must read as absent; err=%v", err)

	_, err = f.h.sheetClient().List(t.Context(), keyed(key, &sheetv1.ListRequest{ProjectId: own}))
	require.Equal(t, connect.CodePermissionDenied, connectCode(err), "a project-wide list would expose every sheet; err=%v", err)

	_, err = f.h.sheetClient().Create(t.Context(), keyed(key, &sheetv1.CreateRequest{
		ProjectId: own, SpreadsheetId: uuid.NewString(), Tab: "Q1", Slug: "escalate", Visibility: string(sheet.VisibilityKey)}))
	require.Equal(t, connect.CodePermissionDenied, connectCode(err), "a resource-bound key must not create; err=%v", err)
}
