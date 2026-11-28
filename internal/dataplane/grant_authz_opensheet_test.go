package dataplane

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/authn"
)

func TestDataPlane_TokenOutsideGrantIs404(t *testing.T) {
	g := newTokenRig(authn.ScopeSheetsRead, authn.ScopeSpreadsheetsRead)
	g.seat()
	want := g.missingSlugBody(t)
	g.roles.Restrict(g.orgs.ref.ID, g.userID, uuid.New())

	for _, path := range []string{rowsPath, g.tabsPath()} {
		if code, body := g.call(t, http.MethodGet, path, nil, testToken); code != http.StatusNotFound || body != want {
			t.Errorf("%s: %d %s, want the missing-slug 404", path, code, body)
		}
	}
	_, _, err := g.authorizer().Authorize(t.Context(), testToken, authn.ScopeSheetsRead, g.orgs.ref.ID, g.projects.ref.ID, g.sheets.ref.ID)
	if !IsSheetNotFoundError(err) {
		t.Errorf("Authorize = %T %v, want the masked *SheetNotFoundError", err, err)
	}

	g.roles.Restrict(g.orgs.ref.ID, g.userID, g.projects.ref.ID)
	if code, body := g.call(t, http.MethodGet, rowsPath, nil, testToken); code != http.StatusOK {
		t.Errorf("granted project: %d %s, want 200", code, body)
	}
}

func TestDataPlane_KeyOutsideCreatorGrantIs404(t *testing.T) {
	g := newTokenRig(authn.ScopeSheetsRead, authn.ScopeSpreadsheetsRead)
	want := g.missingSlugBody(t)
	g.roles.Restrict(g.orgs.ref.ID, g.creator, uuid.New())

	for _, path := range []string{rowsPath, g.tabsPath()} {
		if code, body := g.call(t, http.MethodGet, path, nil, testKey); code != http.StatusNotFound || body != want {
			t.Errorf("%s: %d %s, want the missing-slug 404", path, code, body)
		}
	}
	if _, err := g.authorizeKey(t, authn.ScopeSheetsRead); !IsSheetNotFoundError(err) {
		t.Errorf("Authorize = %T %v, want the masked *SheetNotFoundError", err, err)
	}
}
