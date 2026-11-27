package dataplane

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/authz"
)

type gatedPurger struct{ *fakePurger }

func (g gatedPurger) PurgeCache(ctx context.Context, id uuid.UUID) error {
	if err := authz.Can(ctx, authz.CachePurge); err != nil {
		return err
	}
	return g.fakePurger.PurgeCache(ctx, id)
}

type gatedTabber struct{ *fakeTabber }

func (g gatedTabber) ListTabs(ctx context.Context, id uuid.UUID) ([]string, error) {
	if err := authz.Can(ctx, authz.SpreadsheetsRead); err != nil {
		return nil, err
	}
	return g.fakeTabber.ListTabs(ctx, id)
}

func (g gatedTabber) AddTab(ctx context.Context, id uuid.UUID, title string) error {
	if err := authz.Can(ctx, authz.SpreadsheetsWrite); err != nil {
		return err
	}
	return g.fakeTabber.AddTab(ctx, id, title)
}

func TestDataPlane_MemberKeyAndTokenUseBoundContext(t *testing.T) {
	for name, cred := range map[string]string{"member's token": testToken, "member's key": testKey} {
		t.Run(name, func(t *testing.T) {
			g := newTokenRig(authn.ScopeCachePurge, authn.ScopeSpreadsheetsRead, authn.ScopeSpreadsheetsWrite)
			g.roles.Set(g.orgs.ref.ID, g.creator, "member")
			h := g.rig
			handler := NewSheetHandler(SheetHandlerParams{
				Orgs: h.orgs, Projects: h.projects, Sheets: h.sheets, Reader: h.reader, Inspector: h.inspector,
				Purger: gatedPurger{h.purger}, Writer: h.writer, Tabs: gatedTabber{h.tabs}, Authz: g.authorizer(), Caps: h.caps,
				Log: discardLog(),
			})
			header := http.Header{"Authorization": {"Bearer " + cred}}
			for _, c := range []struct {
				method, path string
				body         []byte
			}{
				{http.MethodDelete, rowsPath + "/cache", nil},
				{http.MethodGet, g.tabsPath(), nil},
				{http.MethodPost, g.tabsPath(), []byte(`{"title":"Q2"}`)},
			} {
				if rec := serve(t, handler, c.method, c.path, c.body, header); rec.Code >= 300 {
					t.Errorf("%s %s: %d %s", c.method, c.path, rec.Code, rec.Body.String())
				}
			}
			if len(h.purger.calls) != 1 || len(h.tabs.ctxs) != 2 {
				t.Errorf("purge %d, tabs %d; want 1 and 2", len(h.purger.calls), len(h.tabs.ctxs))
			}
		})
	}
}
