package controlplane_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	credentialv1 "altalune.id/opensheet/gen/go/credential/v1"
	projectv1 "altalune.id/opensheet/gen/go/project/v1"
	sheetv1 "altalune.id/opensheet/gen/go/sheet/v1"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/sheet"
)

type multiOrgFixture struct {
	h                                  *osHarness
	firstID, secondID, foreignID       uuid.UUID
	firstProj, secondProj, foreignProj *project.Project
}

func multiOrgToken(userID uuid.UUID) session.Principal {
	return session.Principal{UserID: userID, Email: "multi@example.com", Source: session.SourceToken, Scopes: authn.MintableScopes()}
}

// NOTE: principal sees the org ids before the harness exists, so a test can pin a key or a token to one of them.
func newMultiOrgFixture(t *testing.T, userID uuid.UUID, principal func(f *multiOrgFixture) session.Principal) *multiOrgFixture {
	t.Helper()
	f := &multiOrgFixture{firstID: uuid.New(), secondID: uuid.New(), foreignID: uuid.New()}
	f.h = newOSHarness(t, principal(f))
	f.firstProj = f.seedOrg(t, f.firstID, "first-org", userID)
	f.secondProj = f.seedOrg(t, f.secondID, "second-org", userID)
	f.foreignProj = f.seedOrg(t, f.foreignID, "foreign-org", uuid.New())
	return f
}

func (f *multiOrgFixture) seedOrg(t *testing.T, id uuid.UUID, slug string, member uuid.UUID) *project.Project {
	t.Helper()
	ctx := context.Background()
	o, err := org.NewOrg(slug, slug, member)
	require.NoError(t, err)
	o.ID = id
	require.NoError(t, f.h.orgs.Save(ctx, o))
	m, err := org.NewMembership(id, member, org.RoleMember)
	require.NoError(t, err)
	require.NoError(t, f.h.orgs.SaveMembership(ctx, m))
	proj, err := project.New(id, slug+"-project", slug)
	require.NoError(t, err)
	require.NoError(t, f.h.projs.Save(ctx, proj))
	return proj
}

func (f *multiOrgFixture) listCredentials(projectID uuid.UUID) error {
	req := connect.NewRequest(&credentialv1.ListRequest{ProjectId: projectID.String()})
	withBearer(req.Header())
	_, err := f.h.credentialClient().List(context.Background(), req)
	return err
}

func (f *multiOrgFixture) getSheet(t *testing.T, proj *project.Project) error {
	t.Helper()
	sh, err := sheet.New(sheet.NewParams{
		OrgID: proj.OrgID, ProjectID: proj.ID, SpreadsheetID: uuid.New(),
		Tab: "Q1", Slug: "second-sheet", Visibility: sheet.VisibilityKey, CacheTTL: time.Minute,
	})
	require.NoError(t, err)
	require.NoError(t, f.h.sheets.Save(context.Background(), sh))
	req := connect.NewRequest(&sheetv1.GetRequest{ProjectId: proj.ID.String(), SheetId: sh.ID.String()})
	withBearer(req.Header())
	_, err = f.h.sheetClient().Get(context.Background(), req)
	return err
}

func (f *multiOrgFixture) listProjects(t *testing.T) map[string]string {
	t.Helper()
	req := connect.NewRequest(&projectv1.ListProjectsRequest{})
	withBearer(req.Header())
	resp, err := f.h.projectClient().ListProjects(context.Background(), req)
	require.NoError(t, err)
	got := map[string]string{}
	for _, p := range resp.Msg.GetProjects() {
		got[p.GetSlug()] = p.GetOrgSlug()
	}
	return got
}

func TestListProjects_TokenInTwoOrgsListsBoth(t *testing.T) {
	userID := uuid.New()
	f := newMultiOrgFixture(t, userID, func(*multiOrgFixture) session.Principal { return multiOrgToken(userID) })

	require.Equal(t, map[string]string{
		"first-org-project":  "first-org",
		"second-org-project": "second-org",
	}, f.listProjects(t), "a token in two orgs lists both orgs' projects, each with its org slug, and nothing else")
}

func TestListProjects_KeyUnchanged(t *testing.T) {
	f := newMultiOrgFixture(t, uuid.New(), func(f *multiOrgFixture) session.Principal { return keyPrincipal(f.firstID) })

	require.Equal(t, map[string]string{"first-org-project": "first-org"}, f.listProjects(t),
		"a key lists its own org's projects only")
}

func TestScopeToProject_TokenResolvesSecondOrg(t *testing.T) {
	userID := uuid.New()
	for name, active := range map[string]func(*multiOrgFixture) uuid.UUID{
		"no active org":    func(*multiOrgFixture) uuid.UUID { return uuid.Nil },
		"active elsewhere": func(f *multiOrgFixture) uuid.UUID { return f.firstID },
	} {
		t.Run(name, func(t *testing.T) {
			f := newMultiOrgFixture(t, userID, func(f *multiOrgFixture) session.Principal {
				p := multiOrgToken(userID)
				p.ActiveOrgID = active(f)
				return p
			})

			require.NoError(t, f.listCredentials(f.secondProj.ID), "a member of the project's org must reach it")
			require.NoError(t, f.getSheet(t, f.secondProj), "a sheet in the person's second org must resolve too")
		})
	}
}

func TestScopeToProject_TokenNonMemberProjectNotFound(t *testing.T) {
	userID := uuid.New()
	f := newMultiOrgFixture(t, userID, func(*multiOrgFixture) session.Principal { return multiOrgToken(userID) })

	err := f.listCredentials(f.foreignProj.ID)
	require.Error(t, err)
	require.Equal(t, connect.CodeNotFound, connectCode(err), "a project outside every membership reads as absent")
}

// SECURITY: the key carries a member's user id, so only its key-ness keeps it out of the person's other org.
func TestScopeToProject_KeyNeverLeavesActiveOrg(t *testing.T) {
	userID := uuid.New()
	f := newMultiOrgFixture(t, userID, func(f *multiOrgFixture) session.Principal {
		key := keyPrincipal(f.firstID)
		key.UserID = userID
		return key
	})

	require.NoError(t, f.listCredentials(f.firstProj.ID))
	require.Error(t, f.listCredentials(f.secondProj.ID), "a key must never reach outside its active org")
}
