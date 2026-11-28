package controlplane_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/timestamppb"

	apikeyv1 "altalune.id/opensheet/gen/go/apikey/v1"
	credentialv1 "altalune.id/opensheet/gen/go/credential/v1"
	projectv1 "altalune.id/opensheet/gen/go/project/v1"
	sheetv1 "altalune.id/opensheet/gen/go/sheet/v1"
	spreadsheetv1 "altalune.id/opensheet/gen/go/spreadsheet/v1"
	"altalune.id/opensheet/internal/controlplane"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
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
	person           session.Principal
}

// SECURITY: the fakes filter by org only, so the handler's reach check is the sole guard these tests exercise.
func newReachFixture(t *testing.T) *reachFixture {
	t.Helper()
	ctx := context.Background()
	orgID := uuid.New()
	person := humanPrincipal(orgID)
	h := newOSHarnessWithKeys(t, person)
	o, err := org.NewOrg("reach-org", "Reach Org", uuid.New())
	require.NoError(t, err)
	o.ID = orgID
	require.NoError(t, h.orgs.Save(ctx, o))

	own, err := project.New(orgID, "own", "Own")
	require.NoError(t, err)
	require.NoError(t, h.projs.Save(ctx, own))
	sibling, err := project.New(orgID, "sibling", "Sibling")
	require.NoError(t, err)
	require.NoError(t, h.projs.Save(ctx, sibling))

	f := &reachFixture{h: h, orgID: orgID, own: own, sibling: sibling, person: person}
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

type siblingCall struct {
	name      string
	procedure string
	call      func(context.Context) error
	want      connect.Code
}

func as[T any](key string, msg *T) *connect.Request[T] {
	if key == "" {
		req := connect.NewRequest(msg)
		withBearer(req.Header())
		return req
	}
	return keyed(key, msg)
}

func siblingCalls(f *reachFixture, key string) []siblingCall {
	sibling := f.sibling.ID.String()
	siblingSheet := f.siblingSheet.ID.String()
	siblingWorkbook := f.siblingWorkbook.ID.String()
	sheets, sprds, creds := f.h.sheetClient(), f.h.spreadsheetClient(), f.h.credentialClient()
	return []siblingCall{
		{"SheetList", "/sheet.v1.SheetService/List", func(ctx context.Context) error {
			_, err := sheets.List(ctx, as(key, &sheetv1.ListRequest{ProjectId: sibling}))
			return err
		}, connect.CodePermissionDenied},
		{"SheetCreate", "/sheet.v1.SheetService/Create", func(ctx context.Context) error {
			_, err := sheets.Create(ctx, as(key, &sheetv1.CreateRequest{
				ProjectId: sibling, SpreadsheetId: siblingWorkbook, Tab: "Q1", Slug: "escalate", Visibility: string(sheet.VisibilityKey)}))
			return err
		}, connect.CodePermissionDenied},
		{"SheetGet", "/sheet.v1.SheetService/Get", func(ctx context.Context) error {
			_, err := sheets.Get(ctx, as(key, &sheetv1.GetRequest{ProjectId: sibling, SheetId: siblingSheet}))
			return err
		}, connect.CodeNotFound},
		{"SheetUpdate", "/sheet.v1.SheetService/Update", func(ctx context.Context) error {
			tab := "Q2"
			_, err := sheets.Update(ctx, as(key, &sheetv1.UpdateRequest{ProjectId: sibling, SheetId: siblingSheet, Tab: &tab}))
			return err
		}, connect.CodeNotFound},
		{"SheetPurgeCache", "/sheet.v1.SheetService/PurgeCache", func(ctx context.Context) error {
			_, err := sheets.PurgeCache(ctx, as(key, &sheetv1.PurgeCacheRequest{ProjectId: sibling, SheetId: siblingSheet}))
			return err
		}, connect.CodeNotFound},
		{"SheetDelete", "/sheet.v1.SheetService/Delete", func(ctx context.Context) error {
			_, err := sheets.Delete(ctx, as(key, &sheetv1.DeleteRequest{ProjectId: sibling, SheetId: siblingSheet}))
			return err
		}, connect.CodeNotFound},
		{"SpreadsheetList", "/spreadsheet.v1.SpreadsheetService/List", func(ctx context.Context) error {
			_, err := sprds.List(ctx, as(key, &spreadsheetv1.ListRequest{ProjectId: sibling}))
			return err
		}, connect.CodePermissionDenied},
		{"SpreadsheetGet", "/spreadsheet.v1.SpreadsheetService/Get", func(ctx context.Context) error {
			_, err := sprds.Get(ctx, as(key, &spreadsheetv1.GetRequest{ProjectId: sibling, SpreadsheetId: siblingWorkbook}))
			return err
		}, connect.CodePermissionDenied},
		{"SpreadsheetDelete", "/spreadsheet.v1.SpreadsheetService/Delete", func(ctx context.Context) error {
			_, err := sprds.Delete(ctx, as(key, &spreadsheetv1.DeleteRequest{ProjectId: sibling, SpreadsheetId: siblingWorkbook}))
			return err
		}, connect.CodePermissionDenied},
		{"CredentialList", "/credential.v1.CredentialService/List", func(ctx context.Context) error {
			_, err := creds.List(ctx, as(key, &credentialv1.ListRequest{ProjectId: sibling}))
			return err
		}, connect.CodePermissionDenied},
		{"CredentialDelete", "/credential.v1.CredentialService/Delete", func(ctx context.Context) error {
			_, err := creds.Delete(ctx, as(key, &credentialv1.DeleteRequest{ProjectId: sibling, CredentialId: uuid.NewString()}))
			return err
		}, connect.CodePermissionDenied},
		{"APIKeyCreate", "/apikey.v1.APIKeyService/Create", func(ctx context.Context) error {
			_, err := f.h.apikeyClient().Create(ctx, as(key, &apikeyv1.CreateRequest{
				ExpiresAt: timestamppb.New(time.Now().Add(24 * time.Hour)), ProjectId: sibling, Name: "escalate", Scopes: []string{authn.ScopeSheetsRead}}))
			return err
		}, connect.CodePermissionDenied},
		{"APIKeyList", "/apikey.v1.APIKeyService/List", func(ctx context.Context) error {
			_, err := f.h.apikeyClient().List(ctx, as(key, &apikeyv1.ListRequest{ProjectId: sibling}))
			return err
		}, connect.CodePermissionDenied},
		{"APIKeyRevoke", "/apikey.v1.APIKeyService/Revoke", func(ctx context.Context) error {
			_, err := f.h.apikeyClient().Revoke(ctx, as(key, &apikeyv1.RevokeRequest{ProjectId: sibling, Id: uuid.NewString()}))
			return err
		}, connect.CodePermissionDenied},
		{"CredentialStartGoogleConnect", "/credential.v1.CredentialService/StartGoogleConnect", func(ctx context.Context) error {
			_, err := creds.StartGoogleConnect(ctx, as(key, &credentialv1.StartGoogleConnectRequest{ProjectId: sibling}))
			return err
		}, connect.CodePermissionDenied},
		{"CredentialUploadServiceAccount", "/credential.v1.CredentialService/UploadServiceAccount", func(ctx context.Context) error {
			_, err := creds.UploadServiceAccount(ctx, as(key, &credentialv1.UploadServiceAccountRequest{ProjectId: sibling, Name: "escalate", ServiceAccountJson: []byte("{}")}))
			return err
		}, connect.CodePermissionDenied},
		{"SpreadsheetCreate", "/spreadsheet.v1.SpreadsheetService/Create", func(ctx context.Context) error {
			_, err := sprds.Create(ctx, as(key, &spreadsheetv1.CreateRequest{ProjectId: sibling, CredentialId: uuid.NewString(), GoogleFileId: "FILE-x", Title: "x"}))
			return err
		}, connect.CodePermissionDenied},
		{"SpreadsheetListTabs", "/spreadsheet.v1.SpreadsheetService/ListTabs", func(ctx context.Context) error {
			_, err := sprds.ListTabs(ctx, as(key, &spreadsheetv1.ListTabsRequest{ProjectId: sibling, SpreadsheetId: siblingWorkbook}))
			return err
		}, connect.CodePermissionDenied},
		{"SpreadsheetProvision", "/spreadsheet.v1.SpreadsheetService/Provision", func(ctx context.Context) error {
			_, err := sprds.Provision(ctx, as(key, &spreadsheetv1.ProvisionRequest{ProjectId: sibling, CredentialId: uuid.NewString(), Title: "x", IdempotencyKey: uuid.NewString()}))
			return err
		}, connect.CodePermissionDenied},
		{"SpreadsheetUpdate", "/spreadsheet.v1.SpreadsheetService/Update", func(ctx context.Context) error {
			title := "renamed"
			_, err := sprds.Update(ctx, as(key, &spreadsheetv1.UpdateRequest{ProjectId: sibling, SpreadsheetId: siblingWorkbook, Title: &title}))
			return err
		}, connect.CodePermissionDenied},
	}
}

// TestKeyNeverReachesASiblingProject pins a key to the project it was minted in. SECURITY: every row names a verb that took a sibling project's id; revert the reach check and each one fails.
func TestKeyNeverReachesASiblingProject(t *testing.T) {
	f := newReachFixture(t)
	key := f.key(allScopes())
	tests := siblingCalls(f, key)
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

// SECURITY: a restricted person gets NotFound, never PermissionDenied, so a project outside the grant reads as missing.
func TestControlPlane_RestrictedMemberOutsideGrantIsNotFound(t *testing.T) {
	f := newReachFixture(t)
	orgs := f.h.orgs
	ctx := context.Background()
	m, err := orgs.MembershipOf(ctx, f.orgID, f.person.UserID)
	require.NoError(t, err)
	m.Role = org.RoleAdmin
	require.NoError(t, orgs.SaveMembership(ctx, m))
	require.NoError(t, orgs.SetProjectGrant(ctx, f.orgID, f.person.UserID, org.ProjectGrant{ProjectIDs: []uuid.UUID{f.own.ID}}))

	for _, tt := range siblingCalls(f, "") {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(t.Context())
			require.Error(t, err, "a restricted person reached a project outside the grant")
			want := connect.CodeNotFound
			if controlplane.ScopeTable()[tt.procedure] == authn.SessionOnly {
				want = connect.CodePermissionDenied
				require.ErrorContains(t, err, "only a signed-in person", "a token is refused by the scope table, whatever the project")
			}
			require.Equal(t, want, connectCode(err), "err=%v", err)
		})
	}
	stored, err := f.h.sheets.ByID(ctx, f.siblingSheet.ID)
	require.NoError(t, err)
	require.Equal(t, f.siblingTabBefore, stored.Tab, "the sibling sheet must be left untouched")
}

func TestSiblingCallsCoverEveryProjectScopedProcedure(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range siblingCalls(newReachFixture(t), "") {
		covered[c.procedure] = true
	}
	for proc := range controlplane.ScopeTable() {
		parts := strings.Split(strings.TrimPrefix(proc, "/"), "/")
		d, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(parts[0]))
		require.NoError(t, err, proc)
		m := d.(protoreflect.ServiceDescriptor).Methods().ByName(protoreflect.Name(parts[1]))
		require.NotNil(t, m, proc)
		if m.Input().Fields().ByName("project_id") != nil {
			assert.True(t, covered[proc], "%s takes a project_id but siblingCalls never aims it at a project outside the grant", proc)
		}
	}
}
