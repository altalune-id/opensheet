package handlers

import (
	"cmp"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/spreadsheet"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/templates"
)

// maxPreviewRows bounds how much of a tab the preview table renders.
const maxPreviewRows = 50

// SheetHandler owns the project-scoped published-sheet pages.
type SheetHandler struct {
	Deps
	Sheets       *sheet.Service
	Spreadsheets *spreadsheet.Service
	Read         *sheet.ReadWorkflow
}

// NewSheetHandler wires the handler.
func NewSheetHandler(d Deps, projects *project.Service, sheets *sheet.Service, spreadsheets *spreadsheet.Service, read *sheet.ReadWorkflow) *SheetHandler {
	d.Projects = projects
	return &SheetHandler{Deps: d, Sheets: sheets, Spreadsheets: spreadsheets, Read: read}
}

// GetList renders the published sheets.
func (h *SheetHandler) GetList(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireProject(w, r)
	if !ok {
		return
	}
	h.remember(sc)
	view, ok := h.fill(w, sc, templates.SheetsView{})
	if !ok {
		return
	}
	Render(w, sc.req, templates.SheetsLayout(
		h.LayoutForProject(sc.req, "Sheets · "+sc.project.Name, sc.org.Slug, sc.project, "sheets"),
		view,
	))
}

// PostCreate publishes a tab under a slug and returns the refreshed section.
func (h *SheetHandler) PostCreate(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.requireProject(w, r)
	if !ok {
		return
	}
	if err := sc.req.ParseForm(); err != nil {
		h.ErrorPage(w, sc.req, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	form := sc.req.PostForm
	in := templates.SheetsView{
		SpreadsheetID: strings.TrimSpace(form.Get("spreadsheet_id")),
		Slug:          strings.TrimSpace(form.Get("slug")),
		Tab:           strings.TrimSpace(form.Get("tab")),
		CacheTTL:      strings.TrimSpace(form.Get("cache_ttl")),
		Public:        form.Get("visibility") == string(sheet.VisibilityPublic),
	}
	sprdID, err := uuid.Parse(in.SpreadsheetID)
	if err != nil {
		in.Error = "Choose the spreadsheet this tab belongs to."
		h.writeSection(w, sc, in)
		return
	}
	if msg := h.publicRefusal(in.Public, form.Get("public_ack")); msg != "" {
		in.Error = msg
		h.writeSection(w, sc, in)
		return
	}
	ttl, msg := parseTTL(in.CacheTTL)
	if msg != "" {
		in.Error = msg
		h.writeSection(w, sc, in)
		return
	}
	if _, err := h.Sheets.Create(sc.req.Context(), sprdID, in.Tab, in.Slug, visibilityOf(in.Public), ttl); err != nil {
		h.LogErr("web sheet: create", err)
		in.Error, in.ErrorCode = publishMessage(err), ErrorRef(err)
		h.writeSection(w, sc, in)
		return
	}
	h.writeSection(w, sc, templates.SheetsView{})
}

// GetShow renders one published sheet.
func (h *SheetHandler) GetShow(w http.ResponseWriter, r *http.Request) {
	sc, sh, ok := h.requireSheet(w, r)
	if !ok {
		return
	}
	Render(w, sc.req, templates.SheetLayout(
		h.LayoutForProject(sc.req, sh.Slug+" · "+sc.project.Name, sc.org.Slug, sc.project, "sheets"),
		h.detail(sc, sh, ""),
	))
}

// PostUpdate applies the edit form to the sheet.
func (h *SheetHandler) PostUpdate(w http.ResponseWriter, r *http.Request) {
	sc, sh, ok := h.requireSheet(w, r)
	if !ok {
		return
	}
	if err := sc.req.ParseForm(); err != nil {
		h.ErrorPage(w, sc.req, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	form := sc.req.PostForm
	public := form.Get("visibility") == string(sheet.VisibilityPublic)
	if msg := h.publicRefusal(public && sh.Visibility != sheet.VisibilityPublic, form.Get("public_ack")); msg != "" {
		h.renderDetail(w, sc, sh, msg, nil)
		return
	}
	ttl, msg := parseTTL(strings.TrimSpace(form.Get("cache_ttl")))
	if msg != "" {
		h.renderDetail(w, sc, sh, msg, nil)
		return
	}
	tab := strings.TrimSpace(form.Get("tab"))
	vis := visibilityOf(public)
	if _, err := h.Sheets.Update(sc.req.Context(), sh.ID, sheet.UpdateInput{Tab: &tab, Visibility: &vis, CacheTTL: &ttl}); err != nil {
		h.LogErr("web sheet: update", err)
		h.renderDetail(w, sc, sh, publishMessage(err), err)
		return
	}
	h.redirect(w, sc, "/sheets/"+sh.ID.String())
}

// PostDelete unpublishes the sheet.
func (h *SheetHandler) PostDelete(w http.ResponseWriter, r *http.Request) {
	sc, sh, ok := h.requireSheet(w, r)
	if !ok {
		return
	}
	if err := h.Sheets.Delete(sc.req.Context(), sh.ID); err != nil && !sheet.IsNotFoundError(err) {
		h.LogErr("web sheet: delete", err)
		h.renderDetail(w, sc, sh, "Could not unpublish that sheet.", err)
		return
	}
	h.redirect(w, sc, "/sheets")
}

// PostPurge drops every cached tab of the sheet.
// NOTE: the web UI purges through the service, not the data plane's DELETE — a cookie-accepting
// DELETE outside this middleware chain would have no CSRF protection.
func (h *SheetHandler) PostPurge(w http.ResponseWriter, r *http.Request) {
	sc, sh, ok := h.requireSheet(w, r)
	if !ok {
		return
	}
	if err := h.Sheets.PurgeCache(sc.req.Context(), sh.ID); err != nil {
		h.LogErr("web sheet: purge", err)
		Render(w, sc.req, templates.SheetPurged(h.fragment(sc), "Could not purge the cache."))
		return
	}
	Render(w, sc.req, templates.SheetPurged(h.fragment(sc), ""))
}

// GetPreview renders one read of the sheet, which may cost a Google round-trip.
func (h *SheetHandler) GetPreview(w http.ResponseWriter, r *http.Request) {
	sc, sh, ok := h.requireSheet(w, r)
	if !ok {
		return
	}
	rows, err := h.Read.Rows(sc.req.Context(), sh)
	if err != nil {
		h.LogErr("web sheet: preview", err)
		Render(w, sc.req, templates.SheetPreviewFragment(h.fragment(sc), templates.SheetPreview{Failure: previewMessage(err)}))
		return
	}
	Render(w, sc.req, templates.SheetPreviewFragment(h.fragment(sc), previewOf(rows)))
}

// Register wires the sheet routes onto mux.
func (h *SheetHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/sheets", h.GetList)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/sheets", h.PostCreate)
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/sheets/{id}", h.GetShow)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/sheets/{id}", h.PostUpdate)
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/sheets/{id}/preview", h.GetPreview)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/sheets/{id}/purge", h.PostPurge)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/sheets/{id}/delete", h.PostDelete)
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/spreadsheets/{id}/publish", h.GetBulkPanel)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/spreadsheets/{id}/publish", h.PostBulkPublish)
}

// GetBulkPanel renders the tab-picker panel for one document, which costs one Google round-trip.
func (h *SheetHandler) GetBulkPanel(w http.ResponseWriter, r *http.Request) {
	sc, sp, ok := h.requireRegisteredSpreadsheet(w, r)
	if !ok {
		return
	}
	Render(w, sc.req, templates.SpreadsheetBulkPublish(h.fragment(sc), h.bulkPanel(sc, sp)))
}

// PostBulkPublish publishes every ticked tab of one document and re-renders the panel with per-row outcomes.
func (h *SheetHandler) PostBulkPublish(w http.ResponseWriter, r *http.Request) {
	sc, sp, ok := h.requireRegisteredSpreadsheet(w, r)
	if !ok {
		return
	}
	if err := sc.req.ParseForm(); err != nil {
		h.ErrorPage(w, sc.req, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	view := h.bulkPanel(sc, sp)
	h.publishTicked(sc, sp, &view)
	Render(w, sc.req, templates.SpreadsheetBulkPublish(h.fragment(sc), view))
}

func (h *SheetHandler) requireRegisteredSpreadsheet(w http.ResponseWriter, r *http.Request) (projectScope, *spreadsheet.Spreadsheet, bool) {
	sc, ok := h.requireProject(w, r)
	if !ok {
		return projectScope{}, nil, false
	}
	id, ok := h.pathID(w, sc.req, "spreadsheet")
	if !ok {
		return projectScope{}, nil, false
	}
	sp, err := h.Spreadsheets.ByID(sc.req.Context(), id)
	if err != nil {
		if spreadsheet.IsNotFoundError(err) {
			h.ErrorPage(w, sc.req, http.StatusNotFound, "Not found", "That spreadsheet is not registered in this project.")
			return projectScope{}, nil, false
		}
		h.LogErr("web sheet: bulk byID", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "Lookup failed", "Could not load that spreadsheet.", err)
		return projectScope{}, nil, false
	}
	return sc, sp, true
}

func (h *SheetHandler) bulkPanel(sc projectScope, sp *spreadsheet.Spreadsheet) templates.BulkPublishView {
	view := templates.BulkPublishView{
		ProjectSlug:   sc.project.Slug,
		SpreadsheetID: sp.ID.String(),
		CacheTTL:      strconv.FormatInt(int64(sheet.DefaultCacheTTL/time.Second), 10),
	}
	tabs, err := h.Spreadsheets.ListTabs(sc.req.Context(), sp.ID)
	if err != nil {
		h.LogErr("web sheet: bulk list tabs", err)
		view.Unavailable = tabsMessage(err)
		return view
	}
	published := h.publishedTabs(sc, sp.ID)
	view.Rows = make([]templates.BulkTabRow, 0, len(tabs))
	for i, tab := range tabs {
		row := templates.BulkTabRow{
			Tab:       tab,
			TabValue:  strconv.Itoa(i),
			SlugField: bulkSlugField(i),
			Slug:      slugifyTab(tab),
		}
		if id, ok := published[tab]; ok {
			row.Published, row.SheetID = true, id
		}
		view.Rows = append(view.Rows, row)
	}
	return view
}

// NOTE: every ticked row is published on its own — a row that fails leaves the earlier successes in place, deliberately.
func (h *SheetHandler) publishTicked(sc projectScope, sp *spreadsheet.Spreadsheet, view *templates.BulkPublishView) {
	form := sc.req.PostForm
	view.Public = form.Get("visibility") == string(sheet.VisibilityPublic)
	if msg := h.publicRefusal(view.Public, form.Get("public_ack")); msg != "" {
		view.Notice = msg
		return
	}
	ttl, msg := parseTTL(strings.TrimSpace(form.Get("cache_ttl")))
	if msg != "" {
		view.Notice = msg
		return
	}
	vis := visibilityOf(view.Public)
	ticked := form["tab"]
	attempted := 0
	for i := range view.Rows {
		row := &view.Rows[i]
		if row.Published || !slices.Contains(ticked, row.TabValue) {
			continue
		}
		attempted++
		row.Slug = strings.TrimSpace(form.Get(row.SlugField))
		h.publishRow(sc, sp, row, vis, ttl)
	}
	if tab := strings.TrimSpace(form.Get("tab_name")); tab != "" {
		attempted++
		view.Manual.Tab = tab
		view.Manual.Slug = cmp.Or(strings.TrimSpace(form.Get("slug")), slugifyTab(tab))
		h.publishRow(sc, sp, &view.Manual, vis, ttl)
	}
	view.NoneSelected = attempted == 0
}

func (h *SheetHandler) publishRow(sc projectScope, sp *spreadsheet.Spreadsheet, row *templates.BulkTabRow, vis sheet.Visibility, ttl time.Duration) {
	if row.Slug == "" {
		row.SlugEmpty = true
		return
	}
	sh, err := h.Sheets.Create(sc.req.Context(), sp.ID, row.Tab, row.Slug, vis, ttl)
	if err != nil {
		h.LogErr("web sheet: bulk create", err)
		row.Failure = publishMessage(err)
		return
	}
	row.Published, row.Created, row.SheetID = true, true, sh.ID.String()
}

func (h *SheetHandler) publishedTabs(sc projectScope, sprdID uuid.UUID) map[string]string {
	items, err := h.Sheets.List(sc.req.Context())
	if err != nil {
		h.LogErr("web sheet: bulk list sheets", err)
		return nil
	}
	out := make(map[string]string, len(items))
	for _, sh := range items {
		if sh.SpreadsheetID == sprdID && sh.Tab != "" {
			out[sh.Tab] = sh.ID.String()
		}
	}
	return out
}

// NOTE: the index pairs a checkbox with its slug input; repeated unindexed keys would shift a neighbour's slug onto a row an operator never ticked.
func bulkSlugField(i int) string { return "slug." + strconv.Itoa(i) }

func (h *SheetHandler) requireSheet(w http.ResponseWriter, r *http.Request) (projectScope, *sheet.Sheet, bool) {
	sc, ok := h.requireProject(w, r)
	if !ok {
		return projectScope{}, nil, false
	}
	id, ok := h.pathID(w, sc.req, "sheet")
	if !ok {
		return projectScope{}, nil, false
	}
	sh, err := h.Sheets.ByID(sc.req.Context(), id)
	if err != nil {
		if sheet.IsNotFoundError(err) {
			h.ErrorPage(w, sc.req, http.StatusNotFound, "Not found", "That sheet is not published in this project.")
			return projectScope{}, nil, false
		}
		h.LogErr("web sheet: byID", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "Lookup failed", "Could not load that sheet.", err)
		return projectScope{}, nil, false
	}
	return sc, sh, true
}

// publicRefusal gates a public sheet on the capability and on the operator's explicit acknowledgement.
// SECURITY: enforced on the server, so hiding the radio in the template is a courtesy rather than the control.
func (h *SheetHandler) publicRefusal(public bool, ack string) string {
	if !public {
		return ""
	}
	if !h.Caps.PublicSheets {
		return "Public sheets are disabled in this deployment."
	}
	if ack != "1" {
		return "Tick the confirmation: anyone with the URL can read a public sheet's rows."
	}
	return ""
}

func (h *SheetHandler) redirect(w http.ResponseWriter, sc projectScope, suffix string) {
	http.Redirect(w, sc.req, web.Path(h.Cfg.HTTP.BasePath, projectPath(sc.org.Slug, sc.project.Slug, suffix)), http.StatusSeeOther) //nolint:gosec // G710: both slugs are validated by their own slug patterns
}

func (h *SheetHandler) writeSection(w http.ResponseWriter, sc projectScope, view templates.SheetsView) {
	filled, ok := h.fill(w, sc, view)
	if !ok {
		return
	}
	Render(w, sc.req, templates.SheetSection(h.fragment(sc), filled))
}

func (h *SheetHandler) fill(w http.ResponseWriter, sc projectScope, view templates.SheetsView) (templates.SheetsView, bool) {
	sprds, err := h.Spreadsheets.List(sc.req.Context())
	if err != nil {
		h.LogErr("web sheet: list spreadsheets", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "List failed", "Could not load spreadsheets.", err)
		return templates.SheetsView{}, false
	}
	items, err := h.Sheets.List(sc.req.Context())
	if err != nil {
		h.LogErr("web sheet: list", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "List failed", "Could not load sheets.", err)
		return templates.SheetsView{}, false
	}
	titles := make(map[uuid.UUID]string, len(sprds))
	options := make([]templates.OptionRow, 0, len(sprds))
	for _, sp := range sprds {
		titles[sp.ID] = spreadsheetTitle(sp)
		options = append(options, templates.OptionRow{Value: sp.ID.String(), Label: spreadsheetTitle(sp)})
	}
	view.ProjectSlug = sc.project.Slug
	view.Spreadsheets = options
	view.Rows = make([]templates.SheetRow, 0, len(items))
	for _, sh := range items {
		view.Rows = append(view.Rows, templates.SheetRow{
			ID:               sh.ID.String(),
			Slug:             sh.Slug,
			Tab:              tabLabel(sh.Tab),
			TTL:              ttlLabel(sh.CacheTTL),
			SpreadsheetTitle: titles[sh.SpreadsheetID],
			Public:           sh.Visibility == sheet.VisibilityPublic,
		})
	}
	return view, true
}

func (h *SheetHandler) detail(sc projectScope, sh *sheet.Sheet, failure string) templates.SheetView {
	title := ""
	if sp, err := h.Spreadsheets.ByID(sc.req.Context(), sh.SpreadsheetID); err == nil {
		title = spreadsheetTitle(sp)
	}
	return templates.SheetView{
		ProjectSlug:      sc.project.Slug,
		DataURL:          h.dataURL(sc, sh),
		ID:               sh.ID.String(),
		Slug:             sh.Slug,
		Tab:              sh.Tab,
		CacheTTL:         strconv.FormatInt(int64(sh.CacheTTL/time.Second), 10),
		SpreadsheetTitle: title,
		Public:           sh.Visibility == sheet.VisibilityPublic,
		Error:            failure,
	}
}

// dataURL is the absolute URL a consumer fetches the sheet from, for copying off the page.
func (h *SheetHandler) dataURL(sc projectScope, sh *sheet.Sheet) string {
	path := web.DataPath(h.Cfg.HTTP.BasePath, sc.org.Slug, sc.project.Slug, sh.Slug)
	return strings.TrimRight(h.Cfg.HTTP.BaseURL, "/") + path
}

func (h *SheetHandler) renderDetail(w http.ResponseWriter, sc projectScope, sh *sheet.Sheet, failure string, cause error) {
	view := h.detail(sc, sh, failure)
	view.ErrorCode = ErrorRef(cause)
	Render(w, sc.req, templates.SheetLayout(
		h.LayoutForProject(sc.req, sh.Slug+" · "+sc.project.Name, sc.org.Slug, sc.project, "sheets"),
		view,
	))
}

// previewOf flattens the row maps into a table.
// NOTE: a row is a JSON object, which has no key order, so the columns are sorted by name rather
// than by their position in the tab.
func previewOf(rows sheet.Rows) templates.SheetPreview {
	columns := map[string]struct{}{}
	for _, row := range rows.Values {
		for key := range row {
			columns[key] = struct{}{}
		}
	}
	cols := slices.Sorted(maps.Keys(columns))
	out := templates.SheetPreview{
		Columns:   cols,
		Warnings:  rows.Warnings,
		ETag:      rows.ETag,
		FetchedAt: rows.FetchedAt.Format(time.RFC3339),
		Cached:    rows.Cached,
		Stale:     rows.Stale,
		Truncated: len(rows.Values) > maxPreviewRows,
	}
	shown := rows.Values
	if out.Truncated {
		shown = shown[:maxPreviewRows]
	}
	out.Rows = make([][]string, 0, len(shown))
	for _, row := range shown {
		cells := make([]string, 0, len(cols))
		for _, col := range cols {
			cells = append(cells, row[col])
		}
		out.Rows = append(out.Rows, cells)
	}
	return out
}

func visibilityOf(public bool) sheet.Visibility {
	if public {
		return sheet.VisibilityPublic
	}
	return sheet.VisibilityKey
}

func tabLabel(tab string) string {
	if tab == "" {
		return "first tab"
	}
	return tab
}

func ttlLabel(ttl time.Duration) string {
	if ttl == sheet.DefaultCacheTTL {
		return "default TTL"
	}
	return ttl.String()
}

func parseTTL(raw string) (ttl time.Duration, failure string) {
	if raw == "" {
		return sheet.DefaultCacheTTL, ""
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs < 0 {
		return 0, "Cache TTL must be a whole number of seconds."
	}
	return time.Duration(secs) * time.Second, ""
}

func publishMessage(err error) string {
	switch {
	case sheet.IsInvalidSlugError(err),
		sheet.IsInvalidVisibilityError(err),
		sheet.IsInvalidTTLError(err),
		sheet.IsAlreadyExistsError(err),
		sheet.IsPublicDisabledError(err):
		return err.Error()
	case spreadsheet.IsNotFoundError(err):
		return "That spreadsheet is not registered in this project."
	}
	return "Could not publish that sheet."
}

func previewMessage(err error) string {
	switch {
	case sheet.IsPayloadTooLargeError(err), sheet.IsPublicDisabledError(err):
		return err.Error()
	case credential.IsReauthNeededError(err):
		return "The credential behind this sheet needs reconnecting."
	case spreadsheet.IsNotFoundError(err):
		return "The spreadsheet behind this sheet is no longer registered."
	}
	return "Could not read the rows from Google."
}
