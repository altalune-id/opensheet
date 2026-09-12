package handlers

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/web/templates"
)

// APIKeyHandler owns the project-scoped API key pages.
type APIKeyHandler struct {
	Deps
	Keys   *apikey.Service
	Sheets *sheet.Service
}

// NewAPIKeyHandler wires the handler.
func NewAPIKeyHandler(d Deps, projects *project.Service, keys *apikey.Service, sheets *sheet.Service) *APIKeyHandler {
	d.Projects = projects
	return &APIKeyHandler{Deps: d, Keys: keys, Sheets: sheets}
}

// GetList renders the project's API keys.
func (h *APIKeyHandler) GetList(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireProject(w, r)
	if !ok {
		return
	}
	h.remember(sc)
	view, ok := h.fill(w, sc, templates.KeysView{})
	if !ok {
		return
	}
	Render(w, sc.req, templates.KeysLayout(
		h.LayoutForProject(sc.req, "API keys · "+sc.project.Name, sc.org.Slug, sc.project, "keys"),
		view,
	))
}

// PostCreate mints a key and renders its plaintext exactly once.
// SECURITY: the plaintext exists only in this response — it is never stored, logged or re-fetchable.
func (h *APIKeyHandler) PostCreate(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireProject(w, r)
	if !ok {
		return
	}
	if err := sc.req.ParseForm(); err != nil {
		h.ErrorPage(w, sc.req, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	form := sc.req.PostForm
	name := strings.TrimSpace(form.Get("name"))
	scopes := checkedScopes(form["scopes"])
	sheetIDs, msg := parseIDs(form["sheet_ids"])
	if msg != "" {
		h.writeSection(w, sc, templates.KeysView{Name: name, Error: msg, Scopes: scopeOptions(scopes)})
		return
	}
	expires, msg := parseExpiry(strings.TrimSpace(form.Get("expires_at")))
	if msg != "" {
		h.writeSection(w, sc, templates.KeysView{Name: name, Error: msg, Scopes: scopeOptions(scopes)})
		return
	}
	_, plaintext, err := h.Keys.Create(sc.req.Context(), apikey.CreateRequest{
		Name: name, Scopes: scopes, SheetIDs: sheetIDs, ExpiresAt: expires,
	})
	if err != nil {
		h.LogErr("web apikey: create", err)
		h.writeSection(w, sc, templates.KeysView{
			Name: name, Scopes: scopeOptions(scopes),
			Error: mintMessage(err), ErrorCode: ErrorRef(err),
		})
		return
	}
	h.writeSection(w, sc, templates.KeysView{Minted: plaintext})
}

// PostRevoke revokes a key without deleting its audit row.
func (h *APIKeyHandler) PostRevoke(w http.ResponseWriter, r *http.Request) {
	sc, id, ok := h.requireKey(w, r)
	if !ok {
		return
	}
	if _, err := h.Keys.Revoke(sc.req.Context(), id); err != nil && !apikey.IsNotFoundError(err) {
		h.LogErr("web apikey: revoke", err)
		h.writeSection(w, sc, templates.KeysView{Error: "Could not revoke that key.", ErrorCode: ErrorRef(err)})
		return
	}
	h.writeSection(w, sc, templates.KeysView{})
}

// PostDelete removes a key row.
func (h *APIKeyHandler) PostDelete(w http.ResponseWriter, r *http.Request) {
	sc, id, ok := h.requireKey(w, r)
	if !ok {
		return
	}
	if err := h.Keys.Delete(sc.req.Context(), id); err != nil && !apikey.IsNotFoundError(err) {
		h.LogErr("web apikey: delete", err)
		h.writeSection(w, sc, templates.KeysView{Error: "Could not delete that key.", ErrorCode: ErrorRef(err)})
		return
	}
	h.writeSection(w, sc, templates.KeysView{})
}

// Register wires the API key routes onto mux.
func (h *APIKeyHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/keys", h.GetList)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/keys", h.PostCreate)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/keys/{id}/revoke", h.PostRevoke)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/keys/{id}/delete", h.PostDelete)
}

func (h *APIKeyHandler) requireKey(w http.ResponseWriter, r *http.Request) (projectScope, uuid.UUID, bool) {
	sc, ok := h.requireProject(w, r)
	if !ok {
		return projectScope{}, uuid.Nil, false
	}
	id, ok := h.pathID(w, sc.req, "API key")
	if !ok {
		return projectScope{}, uuid.Nil, false
	}
	return sc, id, true
}

func (h *APIKeyHandler) writeSection(w http.ResponseWriter, sc projectScope, view templates.KeysView) {
	filled, ok := h.fill(w, sc, view)
	if !ok {
		return
	}
	Render(w, sc.req, templates.KeySection(h.fragment(sc), filled))
}

// fill loads the list and the form's options. SECURITY: a row carries the public prefix only.
func (h *APIKeyHandler) fill(w http.ResponseWriter, sc projectScope, view templates.KeysView) (templates.KeysView, bool) {
	items, err := h.Keys.List(sc.req.Context())
	if err != nil {
		h.LogErr("web apikey: list", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "List failed", "Could not load API keys.", err)
		return templates.KeysView{}, false
	}
	sheets, err := h.Sheets.List(sc.req.Context())
	if err != nil {
		h.LogErr("web apikey: list sheets", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "List failed", "Could not load sheets.", err)
		return templates.KeysView{}, false
	}
	slugs := make(map[uuid.UUID]string, len(sheets))
	options := make([]templates.OptionRow, 0, len(sheets))
	for _, sh := range sheets {
		slugs[sh.ID] = sh.Slug
		options = append(options, templates.OptionRow{Value: sh.ID.String(), Label: sh.Slug})
	}
	view.ProjectSlug = sc.project.Slug
	view.SheetOptions = options
	if view.Scopes == nil {
		view.Scopes = scopeOptions(nil)
	}
	view.Rows = make([]templates.KeyRow, 0, len(items))
	for _, k := range items {
		view.Rows = append(view.Rows, templates.KeyRow{
			ID:       k.ID.String(),
			Name:     k.Name,
			Prefix:   apikey.Label + "_" + k.KeyPrefix,
			Scopes:   strings.Join(k.Scopes, " "),
			Sheets:   grantLabel(k.SheetIDs, slugs),
			Expires:  expiryLabel(k.ExpiresAt),
			LastUsed: lastUsedLabel(k.LastUsedAt),
			Revoked:  k.RevokedAt != nil,
		})
	}
	return view, true
}

func scopeOptions(checked []string) []templates.ScopeOption {
	all := authn.AllScopes()
	out := make([]templates.ScopeOption, 0, len(all))
	for _, s := range all {
		out = append(out, templates.ScopeOption{Value: s, Checked: slices.Contains(checked, s)})
	}
	return out
}

func checkedScopes(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func parseIDs(raw []string) (ids []uuid.UUID, failure string) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil {
			return nil, "One of the selected sheets is not a valid id."
		}
		out = append(out, id)
	}
	return out, ""
}

func parseExpiry(raw string) (at *time.Time, failure string) {
	if raw == "" {
		return nil, ""
	}
	day, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return nil, "Expiry must be a date."
	}
	end := day.UTC().Add(24*time.Hour - time.Second)
	return &end, ""
}

func grantLabel(ids []uuid.UUID, slugs map[uuid.UUID]string) string {
	if len(ids) == 0 {
		return "every sheet in this project"
	}
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		if slug, ok := slugs[id]; ok {
			names = append(names, slug)
			continue
		}
		names = append(names, id.String())
	}
	return strings.Join(names, ", ")
}

func expiryLabel(at *time.Time) string {
	if at == nil {
		return "no expiry"
	}
	return "expires " + at.Format(time.DateOnly)
}

func lastUsedLabel(at *time.Time) string {
	if at == nil {
		return "never used"
	}
	return "last used " + at.Format(time.RFC3339)
}

func mintMessage(err error) string {
	switch {
	case apikey.IsInvalidNameError(err), apikey.IsInvalidExpiryError(err), apikey.IsAlreadyExistsError(err):
		return err.Error()
	case apikey.IsInvalidScopeError(err):
		return "Choose at least one scope from the catalog."
	case apikey.IsUnknownSheetError(err):
		return "One of the selected sheets is not published in this project."
	}
	return "Could not mint that key."
}
