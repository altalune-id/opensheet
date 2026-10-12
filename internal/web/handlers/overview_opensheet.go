package handlers

import (
	"net/http"

	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/spreadsheet"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/templates"
)

// OpensheetOverviewHandler owns the project overview page.
type OpensheetOverviewHandler struct {
	Deps
	Credentials  *credential.Service
	Spreadsheets *spreadsheet.Service
	Sheets       *sheet.Service
}

// NewOpensheetOverviewHandler wires the handler.
func NewOpensheetOverviewHandler(d Deps, projects *project.Service, credentials *credential.Service, spreadsheets *spreadsheet.Service, sheets *sheet.Service) *OpensheetOverviewHandler {
	d.Projects = projects
	return &OpensheetOverviewHandler{Deps: d, Credentials: credentials, Spreadsheets: spreadsheets, Sheets: sheets}
}

// GetOverview renders the project overview page.
func (h *OpensheetOverviewHandler) GetOverview(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	h.remember(sc)
	ctx := sc.req.Context()
	creds, err := h.Credentials.List(ctx)
	if err != nil {
		h.overviewFailed(w, sc, "credentials", err)
		return
	}
	sprds, err := h.Spreadsheets.List(ctx)
	if err != nil {
		h.overviewFailed(w, sc, "spreadsheets", err)
		return
	}
	sheets, err := h.Sheets.List(ctx)
	if err != nil {
		h.overviewFailed(w, sc, "sheets", err)
		return
	}
	Render(w, sc.req, templates.ProjectOverviewLayout(
		h.LayoutForProject(sc.req, "Overview · "+sc.project.Name, sc.org.Slug, sc.project, "overview"),
		templates.ProjectOverviewView{
			OrgSlug:      sc.org.Slug,
			ProjectID:    sc.project.ID.String(),
			ProjectSlug:  sc.project.Slug,
			ProjectName:  sc.project.Name,
			Credentials:  len(creds),
			Spreadsheets: len(sprds),
			Sheets:       len(sheets),
		},
	))
}

// Register wires the overview route onto mux.
func (h *OpensheetOverviewHandler) Register(mux web.Mux) {
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/overview", h.GetOverview)
}

func (h *OpensheetOverviewHandler) overviewFailed(w http.ResponseWriter, sc ProjectScope, what string, err error) {
	h.LogErr("web overview: list "+what, err)
	h.ErrorPage(w, sc.req, http.StatusInternalServerError, "Overview failed", "Could not load the project's "+what+".", err)
}
