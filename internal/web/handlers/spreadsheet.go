package handlers

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/i18n"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/spreadsheet"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/templates"
)

// SpreadsheetHandler owns the project-scoped spreadsheet registry pages.
type SpreadsheetHandler struct {
	Deps
	Spreadsheets *spreadsheet.Service
	Credentials  *credential.Service
}

// NewSpreadsheetHandler wires the handler.
func NewSpreadsheetHandler(d Deps, projects *project.Service, spreadsheets *spreadsheet.Service, credentials *credential.Service) *SpreadsheetHandler {
	d.Projects = projects
	return &SpreadsheetHandler{Deps: d, Spreadsheets: spreadsheets, Credentials: credentials}
}

// GetList renders the spreadsheet registry.
func (h *SpreadsheetHandler) GetList(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	h.remember(sc)
	q := sc.req.URL.Query()
	view, ok := h.fill(w, sc, templates.SpreadsheetsView{
		GoogleFileID: normalizeFileID(q.Get("google_file_id")),
		Title:        strings.TrimSpace(q.Get("title")),
	})
	if !ok {
		return
	}
	Render(w, sc.req, templates.SpreadsheetsLayout(
		h.LayoutForProject(sc.req, "Spreadsheets · "+sc.project.Name, sc.org.Slug, sc.project, "spreadsheets"),
		view,
	))
}

// PostCreate registers a Google document; an htmx post gets the refreshed section, a plain post lands on the new document.
func (h *SpreadsheetHandler) PostCreate(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	if err := sc.req.ParseForm(); err != nil {
		h.ErrorPage(w, sc.req, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	form := sc.req.PostForm
	fileID := normalizeFileID(form.Get("google_file_id"))
	title := strings.TrimSpace(form.Get("title"))
	credID, err := uuid.Parse(strings.TrimSpace(form.Get("credential_id")))
	if err != nil {
		h.createFailed(w, sc, templates.SpreadsheetsView{
			GoogleFileID: fileID, Title: title, Error: "Choose a credential to read this document with.",
		})
		return
	}
	sp, err := h.Spreadsheets.Register(sc.req.Context(), credID, fileID, title)
	if err != nil {
		if existing, ok := h.registeredByPick(sc, fileID, err); ok {
			suffix := "/spreadsheets/" + existing.ID.String()
			if existing.CredentialID == credID {
				suffix += "?restored=1"
			}
			h.redirect(w, sc, suffix)
			return
		}
		h.LogErr("web spreadsheet: register", err)
		h.createFailed(w, sc, templates.SpreadsheetsView{
			CredentialID: credID.String(), GoogleFileID: fileID, Title: title,
			Error: registerMessage(err), ErrorCode: ErrorRef(err),
			NoAccess: noAccessFor(sc, h.Credentials, h.Cfg.HTTP.BasePath, credID, err),
		})
		return
	}
	if !web.IsHTMXRequest(sc.req) {
		h.redirect(w, sc, "/spreadsheets/"+sp.ID.String()+"?added=1")
		return
	}
	h.writeSection(w, sc, templates.SpreadsheetsView{})
}

func (h *SpreadsheetHandler) registeredByPick(sc ProjectScope, fileID string, err error) (*spreadsheet.Spreadsheet, bool) {
	if web.IsHTMXRequest(sc.req) || !spreadsheet.IsAlreadyExistsError(err) {
		return nil, false
	}
	existing, lookupErr := h.Spreadsheets.ByGoogleFileID(sc.req.Context(), fileID)
	if lookupErr != nil {
		h.LogErr("web spreadsheet: registered lookup", lookupErr)
		return nil, false
	}
	return existing, true
}

func (h *SpreadsheetHandler) createFailed(w http.ResponseWriter, sc ProjectScope, view templates.SpreadsheetsView) {
	if web.IsHTMXRequest(sc.req) {
		h.writeSection(w, sc, view)
		return
	}
	filled, ok := h.fill(w, sc, view)
	if !ok {
		return
	}
	Render(w, sc.req, templates.SpreadsheetsLayout(
		h.LayoutForProject(sc.req, "Spreadsheets · "+sc.project.Name, sc.org.Slug, sc.project, "spreadsheets"),
		filled,
	))
}

// GetPick opens the Google Picker as the chosen Google account.
func (h *SpreadsheetHandler) GetPick(w http.ResponseWriter, r *http.Request) {
	if !h.Caps.GooglePicker {
		h.ErrorPage(w, r, http.StatusNotFound, "Not available", "This deployment has no Google Picker API key configured.")
		return
	}
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	credID, err := uuid.Parse(strings.TrimSpace(sc.req.URL.Query().Get("credential_id")))
	if err != nil {
		h.ErrorPage(w, sc.req, http.StatusBadRequest, "Bad picker link", "Open the Picker from the spreadsheets page.")
		return
	}
	c, err := h.Credentials.ByID(sc.req.Context(), credID)
	if err != nil {
		if credential.IsNotFoundError(err) {
			h.ErrorPage(w, sc.req, http.StatusNotFound, "Credential not found", "That credential no longer exists in this project.")
			return
		}
		h.LogErr("web spreadsheet: pick credential", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "Lookup failed", "Could not load that credential.", err)
		return
	}
	if c.Kind != credential.KindGoogleOAuth {
		h.ErrorPage(w, sc.req, http.StatusConflict, "Not a Google account",
			"The Picker needs a connected Google account. A service-account credential cannot open it.")
		return
	}
	if c.Status == credential.StatusReauthNeeded {
		h.ErrorPage(w, sc.req, http.StatusConflict, "Reconnect needed", connectMessage(&credential.ReauthNeededError{ID: c.ID.String()}))
		return
	}
	if !h.refuseNonConnector(w, sc, c) {
		return
	}
	state := PickerStateFor(h.Key(web.PurposeGooglePicker), sc.org.Slug, sc.project.Slug, c.ID)
	http.Redirect(w, sc.req, web.Path(h.Cfg.HTTP.BasePath, googlePickerPath)+"?state="+url.QueryEscape(state), http.StatusSeeOther)
}

// GetShow renders one registered spreadsheet.
func (h *SpreadsheetHandler) GetShow(w http.ResponseWriter, r *http.Request) {
	sc, sp, ok := h.requireSpreadsheet(w, r)
	if !ok {
		return
	}
	q := sc.req.URL.Query()
	view, ok := h.detail(w, sc, sp, templates.SpreadsheetView{Added: q.Has("added"), Restored: q.Has("restored")})
	if !ok {
		return
	}
	Render(w, sc.req, templates.SpreadsheetLayout(
		h.LayoutForProject(sc.req, spreadsheetTitle(sp)+" · "+sc.project.Name, sc.org.Slug, sc.project, "spreadsheets"),
		view,
	))
}

// PostUpdate retitles the spreadsheet, rebinds its credential and re-gates its writes.
func (h *SpreadsheetHandler) PostUpdate(w http.ResponseWriter, r *http.Request) {
	sc, sp, ok := h.requireSpreadsheet(w, r)
	if !ok {
		return
	}
	if err := sc.req.ParseForm(); err != nil {
		h.ErrorPage(w, sc.req, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	if title := strings.TrimSpace(sc.req.PostForm.Get("title")); title != sp.Title {
		if _, err := h.Spreadsheets.Retitle(sc.req.Context(), sp.ID, title); err != nil {
			h.renderDetailError(w, sc, sp, err)
			return
		}
	}
	credID, err := uuid.Parse(strings.TrimSpace(sc.req.PostForm.Get("credential_id")))
	if err == nil && credID != sp.CredentialID {
		if _, rErr := h.Spreadsheets.Rebind(sc.req.Context(), sp.ID, credID); rErr != nil {
			h.renderDetailError(w, sc, sp, rErr)
			return
		}
	}
	if writable := sc.req.PostForm.Get("writable") == "1"; writable != sp.Writable {
		if _, wErr := h.Spreadsheets.SetWritable(sc.req.Context(), sp.ID, writable); wErr != nil {
			h.renderDetailError(w, sc, sp, wErr)
			return
		}
	}
	h.redirect(w, sc, "/spreadsheets/"+sp.ID.String())
}

// PostDelete unregisters the spreadsheet.
func (h *SpreadsheetHandler) PostDelete(w http.ResponseWriter, r *http.Request) {
	sc, sp, ok := h.requireSpreadsheet(w, r)
	if !ok {
		return
	}
	if err := h.Spreadsheets.Delete(sc.req.Context(), sp.ID); err != nil && !spreadsheet.IsNotFoundError(err) {
		h.LogErr("web spreadsheet: delete", err)
		h.renderDetailError(w, sc, sp, err)
		return
	}
	h.redirect(w, sc, "/spreadsheets")
}

// GetTabs renders the document's tab names, which costs one Google round-trip.
func (h *SpreadsheetHandler) GetTabs(w http.ResponseWriter, r *http.Request) {
	sc, sp, ok := h.requireSpreadsheet(w, r)
	if !ok {
		return
	}
	tabs, err := h.Spreadsheets.ListTabs(sc.req.Context(), sp.ID)
	if err != nil {
		h.LogErr("web spreadsheet: list tabs", err)
		Render(w, sc.req, templates.SpreadsheetTabs(h.ProjectFragmentBase(sc), nil, tabsMessage(err), ErrorRef(err),
			noAccessFor(sc, h.Credentials, h.Cfg.HTTP.BasePath, sp.CredentialID, err)))
		return
	}
	Render(w, sc.req, templates.SpreadsheetTabs(h.ProjectFragmentBase(sc), tabs, "", "", nil))
}

// Register wires the spreadsheet routes onto mux.
func (h *SpreadsheetHandler) Register(mux web.Mux) {
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/spreadsheets", h.GetList)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/spreadsheets", h.PostCreate)
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/spreadsheets/pick", h.GetPick)
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/spreadsheets/{id}", h.GetShow)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/spreadsheets/{id}", h.PostUpdate)
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/spreadsheets/{id}/tabs", h.GetTabs)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/spreadsheets/{id}/delete", h.PostDelete)
}

func (h *SpreadsheetHandler) requireSpreadsheet(w http.ResponseWriter, r *http.Request) (ProjectScope, *spreadsheet.Spreadsheet, bool) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return ProjectScope{}, nil, false
	}
	id, ok := h.pathID(w, sc.req, "spreadsheet")
	if !ok {
		return ProjectScope{}, nil, false
	}
	sp, err := h.Spreadsheets.ByID(sc.req.Context(), id)
	if err != nil {
		if spreadsheet.IsNotFoundError(err) {
			h.ErrorPage(w, sc.req, http.StatusNotFound, "Not found", "That spreadsheet is not registered in this project.")
			return ProjectScope{}, nil, false
		}
		h.LogErr("web spreadsheet: byID", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "Lookup failed", "Could not load that spreadsheet.", err)
		return ProjectScope{}, nil, false
	}
	return sc, sp, true
}

func (h *SpreadsheetHandler) redirect(w http.ResponseWriter, sc ProjectScope, suffix string) {
	http.Redirect(w, sc.req, web.Path(h.Cfg.HTTP.BasePath, projectPath(sc.org.Slug, sc.project.Slug, suffix)), http.StatusSeeOther) //nolint:gosec // G710: both slugs are validated by their own slug patterns
}

func (h *SpreadsheetHandler) writeSection(w http.ResponseWriter, sc ProjectScope, view templates.SpreadsheetsView) {
	filled, ok := h.fill(w, sc, view)
	if !ok {
		return
	}
	Render(w, sc.req, templates.SpreadsheetSection(h.ProjectFragmentBase(sc), filled))
}

func (h *SpreadsheetHandler) fill(w http.ResponseWriter, sc ProjectScope, view templates.SpreadsheetsView) (templates.SpreadsheetsView, bool) {
	creds, ok := h.credentialOptions(w, sc)
	if !ok {
		return templates.SpreadsheetsView{}, false
	}
	items, err := h.Spreadsheets.List(sc.req.Context())
	if err != nil {
		h.LogErr("web spreadsheet: list", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "List failed", "Could not load spreadsheets.", err)
		return templates.SpreadsheetsView{}, false
	}
	names := credentialNames(creds)
	view.ProjectSlug = sc.project.Slug
	view.Credentials = credentialSelect(creds)
	if h.Caps.GooglePicker {
		view.PickCredentials = pickableCredentials(creds, sc.principal.UserID)
	}
	view.Rows = make([]templates.SpreadsheetRow, 0, len(items))
	for _, sp := range items {
		view.Rows = append(view.Rows, templates.SpreadsheetRow{
			ID:             sp.ID.String(),
			Title:          spreadsheetTitle(sp),
			GoogleFileID:   sp.GoogleFileID,
			CredentialName: names[sp.CredentialID],
			Writable:       sp.Writable,
		})
	}
	return view, true
}

func (h *SpreadsheetHandler) detail(w http.ResponseWriter, sc ProjectScope, sp *spreadsheet.Spreadsheet, view templates.SpreadsheetView) (templates.SpreadsheetView, bool) {
	creds, ok := h.credentialOptions(w, sc)
	if !ok {
		return templates.SpreadsheetView{}, false
	}
	view.ProjectSlug = sc.project.Slug
	view.ID = sp.ID.String()
	view.Title = spreadsheetTitle(sp)
	view.GoogleFileID = sp.GoogleFileID
	view.CredentialID = sp.CredentialID.String()
	view.Credentials = credentialSelect(creds)
	view.Writable = sp.Writable
	return view, true
}

func (h *SpreadsheetHandler) renderDetailError(w http.ResponseWriter, sc ProjectScope, sp *spreadsheet.Spreadsheet, cause error) {
	msg := registerMessage(cause)
	if deniedWrites(cause) {
		msg = i18n.TranslatorFrom(sc.req.Context()).T("writes.managers_only")
	}
	view, ok := h.detail(w, sc, sp, templates.SpreadsheetView{Error: msg, ErrorCode: ErrorRef(cause)})
	if !ok {
		return
	}
	Render(w, sc.req, templates.SpreadsheetLayout(
		h.LayoutForProject(sc.req, spreadsheetTitle(sp)+" · "+sc.project.Name, sc.org.Slug, sc.project, "spreadsheets"),
		view,
	))
}

func (h *SpreadsheetHandler) credentialOptions(w http.ResponseWriter, sc ProjectScope) ([]*credential.Credential, bool) {
	creds, err := h.Credentials.List(sc.req.Context())
	if err != nil {
		h.LogErr("web spreadsheet: list credentials", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "List failed", "Could not load credentials.", err)
		return nil, false
	}
	return creds, true
}

func pickableCredentials(creds []*credential.Credential, userID uuid.UUID) []templates.OptionRow {
	out := make([]templates.OptionRow, 0, len(creds))
	for _, c := range creds {
		if c.Kind != credential.KindGoogleOAuth || c.Status != credential.StatusActive || c.AuthorizedByUserID != userID {
			continue
		}
		label := c.Name
		if c.GoogleAccountEmail != "" {
			label += " (" + c.GoogleAccountEmail + ")"
		}
		out = append(out, templates.OptionRow{Value: c.ID.String(), Label: label})
	}
	return out
}

func noAccessFor(sc ProjectScope, creds *credential.Service, basePath string, credID uuid.UUID, err error) *templates.NoAccessView {
	if code := ErrorRef(err); code != apperror.CodeGoogleNotFound && code != apperror.CodeGooglePermissionDenied {
		return nil
	}
	c, lookupErr := creds.ByID(sc.req.Context(), credID)
	if lookupErr != nil {
		return nil
	}
	if c.Kind != credential.KindGoogleOAuth || c.AuthorizedByUserID != sc.principal.UserID {
		return &templates.NoAccessView{Kind: string(c.Kind), Email: c.GoogleAccountEmail}
	}
	pick := web.Path(basePath, projectPath(sc.org.Slug, sc.project.Slug, "/spreadsheets/pick")) + "?credential_id=" + c.ID.String()
	return &templates.NoAccessView{Kind: string(c.Kind), Email: c.GoogleAccountEmail, PickURL: pick}
}

func credentialSelect(creds []*credential.Credential) []templates.OptionRow {
	out := make([]templates.OptionRow, 0, len(creds))
	for _, c := range creds {
		out = append(out, templates.OptionRow{Value: c.ID.String(), Label: c.Name})
	}
	return out
}

func credentialNames(creds []*credential.Credential) map[uuid.UUID]string {
	out := make(map[uuid.UUID]string, len(creds))
	for _, c := range creds {
		out[c.ID] = c.Name
	}
	return out
}

func spreadsheetTitle(sp *spreadsheet.Spreadsheet) string {
	if sp.Title != "" {
		return sp.Title
	}
	return sp.GoogleFileID
}

func normalizeFileID(raw string) string {
	raw = strings.TrimSpace(raw)
	const marker = "/d/"
	i := strings.Index(raw, marker)
	if i < 0 {
		return raw
	}
	rest := raw[i+len(marker):]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		return rest[:j]
	}
	return rest
}

func registerMessage(err error) string {
	switch {
	case spreadsheet.IsInvalidFileIDError(err),
		spreadsheet.IsInvalidTitleError(err),
		spreadsheet.IsInvalidCredentialError(err),
		spreadsheet.IsAlreadyExistsError(err):
		return err.Error()
	case credential.IsNotFoundError(err):
		return "That credential no longer exists in this project."
	case credential.IsReauthNeededError(err):
		return "That credential needs reconnecting before it can be used."
	}
	return "Could not register that spreadsheet."
}

func tabsMessage(err error) string {
	if credential.IsReauthNeededError(err) {
		return "The credential behind this document needs reconnecting."
	}
	return "Could not read the document's tabs from Google."
}
