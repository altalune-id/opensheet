package handlers

import (
	"net/http"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/templates"
)

// GetPersonalTokens renders the caller's personal access tokens across every org they belong to.
func (h *APIKeyHandler) GetPersonalTokens(w http.ResponseWriter, r *http.Request) {
	p, sid, ok := h.RequireSignedIn(w, r)
	if !ok {
		return
	}
	v, err := h.personalView(r, p, sid, r.URL.Query().Get("org"), nil, "", "")
	if err != nil {
		h.listFailed(w, r, err)
		return
	}
	layout := h.Layout(r, "Personal access tokens", web.ActiveNav{Scope: web.NavScopeSettings, SettingsKey: "tokens"})
	Render(w, r, templates.APIKeysLayout(layout, v))
}

// GetPersonalTokenProjects returns the project picker for the org the form just switched to.
func (h *APIKeyHandler) GetPersonalTokenProjects(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrgFrom(w, r, func(r *http.Request) string { return r.URL.Query().Get("org") })
	if !ok {
		return
	}
	options, err := h.projectOptions(sc)
	if err != nil {
		h.listFailed(w, sc.req, err)
		return
	}
	Render(w, sc.req, templates.APIKeyGrantProjects(h.OrgFragmentBase(sc), options))
}

// PostPersonalTokenCreate mints a personal access token in the org the form names.
func (h *APIKeyHandler) PostPersonalTokenCreate(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrgFrom(w, r, formOrg)
	if !ok {
		return
	}
	h.mintInto(w, sc.req, h.personalWriter(w, sc), func(in mintInput) (*apikey.APIKey, string, error) {
		return h.Keys.MintPersonal(sc.req.Context(), in.name, in.scopes, in.grant, in.expiresAt)
	})
}

// PostPersonalTokenProjects widens one of the caller's tokens by the submitted projects.
func (h *APIKeyHandler) PostPersonalTokenProjects(w http.ResponseWriter, r *http.Request) {
	h.changePersonalToken(w, r, h.grantProjects)
}

// PostPersonalTokenAllProjects promotes one of the caller's tokens to every project of its org.
func (h *APIKeyHandler) PostPersonalTokenAllProjects(w http.ResponseWriter, r *http.Request) {
	h.changePersonalToken(w, r, h.grantAllProjects)
}

// PostPersonalTokenRevoke revokes one of the caller's tokens.
func (h *APIKeyHandler) PostPersonalTokenRevoke(w http.ResponseWriter, r *http.Request) {
	h.changePersonalToken(w, r, h.revoke)
}

func (h *APIKeyHandler) changePersonalToken(w http.ResponseWriter, r *http.Request, change keyChange) {
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return
	}
	h.changeInto(w, sc.req, h.personalWriter(w, sc), change)
}

func formOrg(r *http.Request) string {
	if err := r.ParseForm(); err != nil {
		return ""
	}
	return r.PostForm.Get("org")
}

func (h *APIKeyHandler) personalView(r *http.Request, p session.Principal, sid, selected string, minted *templates.APIKeyMinted, errKind, code string) (templates.APIKeysView, error) {
	scopes, err := h.MemberOrgScopes(r, p, sid)
	if err != nil {
		return templates.APIKeysView{}, err
	}
	v := baseView(web.Path(h.Cfg.HTTP.BasePath, "/settings/tokens"), minted, errKind, code)
	v.Personal, v.HasGrant, v.CanManage = true, true, true
	picked := pickedOrg(scopes, selected)
	for _, sc := range scopes {
		v.Orgs = append(v.Orgs, templates.APIKeyOrgOption{Slug: sc.org.Slug, Name: sc.org.Name, Selected: sc.org.Slug == picked})
		items, err := h.Keys.ListPersonal(sc.req.Context())
		if err != nil {
			return templates.APIKeysView{}, err
		}
		if len(items) == 0 && sc.org.Slug != picked {
			continue
		}
		options, err := h.projectOptions(sc)
		if err != nil {
			return templates.APIKeysView{}, err
		}
		if sc.org.Slug == picked {
			v.Projects = options
		}
		base := web.Path(h.Cfg.HTTP.BasePath, "/settings/tokens/"+sc.org.Slug)
		for _, k := range items {
			row := apiKeyRow(k, base, options)
			row.OrgName = sc.org.Name
			v.Items = append(v.Items, row)
		}
	}
	return v, nil
}

func pickedOrg(scopes []OrgScope, selected string) string {
	for _, sc := range scopes {
		if sc.org.Slug == selected {
			return selected
		}
	}
	if len(scopes) == 0 {
		return ""
	}
	return scopes[0].org.Slug
}

func (h *APIKeyHandler) personalWriter(w http.ResponseWriter, sc OrgScope) listWriter {
	return func(minted *templates.APIKeyMinted, errKind, code string) {
		v, err := h.personalView(sc.req, sc.principal, sc.sid, sc.org.Slug, minted, errKind, code)
		if err != nil {
			h.listFailed(w, sc.req, err)
			return
		}
		Render(w, sc.req, templates.APIKeyList(h.OrgFragmentBase(sc), v))
	}
}
