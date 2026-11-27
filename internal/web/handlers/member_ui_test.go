package handlers_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/web/handlers"
)

func TestMembersPage_LastOwnerNoteFollowsTheViewer(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)

	body := x.do(t, x.owner, http.MethodGet, "/orgs/acme/members", nil).Body.String()
	assert.Contains(t, body, "authz.last_owner")
	assert.NotContains(t, body, "members.last_owner_owner_only")
	assert.Regexp(t, `<select disabled[^>]*class="[^"]*cursor-not-allowed`, body)

	body = x.do(t, x.admin, http.MethodGet, "/orgs/acme/members", nil).Body.String()
	assert.Contains(t, body, "members.last_owner_owner_only", "an admin cannot make owners, so the owner hint does not apply")
	assert.NotContains(t, body, "authz.last_owner")
}

func TestMembers_SetRoleShowsTheNotice(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)

	rec := x.do(t, x.owner, http.MethodPost, "/orgs/acme/members/"+x.member.String()+"/role", url.Values{"role": {"admin"}})
	require.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	assert.Contains(t, loc, "role_updated=1")

	assert.Contains(t, x.do(t, x.owner, http.MethodGet, "/orgs/acme/members?role_updated=1", nil).Body.String(), "members.role_updated")
	assert.NotContains(t, x.do(t, x.owner, http.MethodGet, "/orgs/acme/members", nil).Body.String(), "members.role_updated")
}

func TestInvitesPage_MemberSeesOnlyTheReason(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	x.Deps.Caps.InvitesEnabled = false
	x.mux = http.NewServeMux()
	handlers.NewInviteHandler(x.Deps, x.Orgs, x.Invites).Register(x.mux)

	body := x.do(t, x.member, http.MethodGet, "/orgs/acme/invites", nil).Body.String()
	assert.Contains(t, body, "invites.read_only")
	assert.NotContains(t, body, "invites.disabled_banner")
	assert.NotContains(t, body, "invites.subtitle")

	body = x.do(t, x.admin, http.MethodGet, "/orgs/acme/invites", nil).Body.String()
	assert.Contains(t, body, "invites.disabled_banner")
	assert.Contains(t, body, "invites.subtitle")
}

func TestWritesToggle_DisabledNamesTheReason(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sp := f.seedSpreadsheet(t, c.ID)
	sh := f.seedSheet(t, sp.ID, "q1", sheet.VisibilityKey)
	f.actAsMember(t)

	body := f.do(t, http.MethodGet, f.path("/sheets/"+sh.ID.String()), "").Body.String()
	assert.Regexp(t, `<input data-writable[^>]*disabled[^>]*aria-describedby="sheet-writes-reason"|<input data-writable[^>]*aria-describedby="sheet-writes-reason"[^>]*disabled`, body)
	assert.Contains(t, body, `id="sheet-writes-reason"`)

	body = f.do(t, http.MethodGet, f.path("/spreadsheets/"+sp.ID.String()), "").Body.String()
	assert.Regexp(t, `<input data-spreadsheet-writable[^>]*aria-describedby="spreadsheet-writes-reason"`, body)
	assert.Contains(t, body, `id="spreadsheet-writes-reason"`)
}
