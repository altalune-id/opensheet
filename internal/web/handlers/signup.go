package handlers

import (
	"cmp"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/user"
	"altalune.id/template/internal/web"
	"altalune.id/template/internal/web/templates"
)

// SignupHandler owns /signup/complete — cloud-only workspace bootstrap for new OIDC users without a pre-existing membership.
type SignupHandler struct {
	Deps
	Users    *user.Service
	Orgs     *org.Service
	Projects *project.Service
}

// NewSignupHandler wires the handler.
func NewSignupHandler(d Deps, users *user.Service, orgs *org.Service, projects *project.Service) *SignupHandler {
	d.Orgs = orgs
	d.Projects = projects
	return &SignupHandler{Deps: d, Users: users, Orgs: orgs, Projects: projects}
}

// Register wires the /signup/complete routes onto mux.
func (h *SignupHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /signup/complete", h.GetSignup)
	mux.HandleFunc("POST /signup/complete", h.PostSignup)
}

// GetSignup renders the signup-complete form.
func (h *SignupHandler) GetSignup(w http.ResponseWriter, r *http.Request) {
	if h.Cfg.Mode != config.ModeCloud {
		h.ErrorPage(w, r, http.StatusNotFound, "Not found", "")
		return
	}
	p, _, ok := h.LoadSession(r)
	if !ok {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	if h.hasMembership(r, p.UserID) {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/"), http.StatusSeeOther)
		return
	}
	Render(w, r, templates.SignupCompleteLayout(h.Base(r, "Complete signup"), h.defaultView(p)))
}

// PostSignup validates the form, provisions the org + first project, stamps terms, and redirects to the project overview.
//
//nolint:gocyclo,funlen // linear signup flow reads more clearly as one function.
func (h *SignupHandler) PostSignup(w http.ResponseWriter, r *http.Request) {
	if h.Cfg.Mode != config.ModeCloud {
		h.ErrorPage(w, r, http.StatusNotFound, "Not found", "")
		return
	}
	p, sid, ok := h.LoadSession(r)
	if !ok {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	if h.hasMembership(r, p.UserID) {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/"), http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	view := h.defaultView(p)
	view.OrgName = strings.TrimSpace(r.PostForm.Get("org_name"))
	view.OrgSlug = strings.TrimSpace(r.PostForm.Get("org_slug"))
	view.ProjectName = strings.TrimSpace(r.PostForm.Get("project_name"))
	view.ProjectSlug = strings.TrimSpace(r.PostForm.Get("project_slug"))
	view.Name = strings.TrimSpace(r.PostForm.Get("name"))
	if view.ProjectName == "" {
		view.ProjectName = "Default Project"
	}
	if view.ProjectSlug == "" {
		view.ProjectSlug = h.defaultProjectSlug()
	}

	view.FieldErrors = map[string]string{}
	if view.OrgName == "" {
		view.FieldErrors["org_name"] = "Enter an organization name."
	}
	if view.OrgSlug == "" {
		view.FieldErrors["org_slug"] = "Enter an organization slug."
	}
	if view.AskDisplayName && view.Name == "" {
		view.FieldErrors["name"] = "Enter your display name."
	}
	if len(view.FieldErrors) > 0 {
		h.render(w, r, view)
		return
	}
	if view.AskAccept && r.PostForm.Get("accept_terms") != "1" {
		view.Error = "Please accept the terms to continue."
		h.render(w, r, view)
		return
	}

	ctx := r.Context()
	o, err := h.Orgs.Create(ctx, org.CreateRequest{Slug: view.OrgSlug, Name: view.OrgName, OwnerID: p.UserID})
	if err != nil {
		if org.IsAlreadyExistsError(err) {
			view.FieldErrors["org_slug"] = "This slug is already taken."
			h.render(w, r, view)
			return
		}
		if org.IsInvalidSlugError(err) {
			view.FieldErrors["org_slug"] = err.Error()
			h.render(w, r, view)
			return
		}
		if org.IsInvalidNameError(err) {
			view.FieldErrors["org_name"] = err.Error()
			h.render(w, r, view)
			return
		}
		h.LogErr("signup: create org", err)
		view.Error = "Could not create the organization."
		view.ErrorCode = ErrorRef(err)
		h.render(w, r, view)
		return
	}

	tctx := tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: p.UserID})
	proj, err := h.Projects.Create(tctx, o.ID, view.ProjectSlug, view.ProjectName)
	if err != nil {
		if project.IsAlreadyExistsError(err) {
			view.FieldErrors["project_slug"] = "This project slug is already taken."
			h.render(w, r, view)
			return
		}
		h.LogErr("signup: create project", err)
		view.Error = "Could not create the first project."
		view.ErrorCode = ErrorRef(err)
		h.render(w, r, view)
		return
	}

	if view.AskDisplayName {
		if err := h.Users.Rename(ctx, p.UserID, view.Name); err != nil {
			h.LogErr("signup: rename", err)
		} else {
			p.Name = view.Name
		}
	}
	if view.AskAccept {
		if err := h.Users.AcceptTerms(ctx, p.UserID); err != nil {
			h.LogErr("signup: accept terms", err)
		} else {
			p.TermsAcceptedAt = time.Now().UTC()
		}
	}

	p.ActiveOrgID = o.ID
	p.ActiveProjectID = proj.ID
	if err := h.UpdateSession(r, sid, p); err != nil {
		h.LogErr("signup: update session", err)
	}
	http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, projectPath(o.Slug, proj.Slug, "/overview")), http.StatusSeeOther) //nolint:gosec // G710: slug is validated by project.Create's slug pattern
}

func (h *SignupHandler) hasMembership(r *http.Request, userID uuid.UUID) bool {
	orgs, err := h.Orgs.List(r.Context(), userID)
	if err != nil {
		h.LogErr("signup: list orgs", err)
		return false
	}
	return len(orgs) > 0
}

func (h *SignupHandler) defaultProjectSlug() string {
	if s := strings.TrimSpace(h.Cfg.Tenant.PersonalProjectSlug); s != "" {
		return s
	}
	return "default"
}

func (h *SignupHandler) defaultView(p session.Principal) templates.SignupCompleteView {
	return templates.SignupCompleteView{
		Email:          p.Email,
		Name:           p.Name,
		AskDisplayName: strings.TrimSpace(p.Name) == "",
		AskAccept:      h.Cfg.Compliance.RequireAcceptance && p.TermsAcceptedAt.IsZero(),
		TermsURL:       cmp.Or(strings.TrimSpace(h.Cfg.Compliance.TermsURL), web.Path(h.Cfg.HTTP.BasePath, "/terms")),
		PrivacyURL:     cmp.Or(strings.TrimSpace(h.Cfg.Compliance.PrivacyURL), web.Path(h.Cfg.HTTP.BasePath, "/privacy")),
		OrgSlug:        user.SlugFromEmail(p.Email, ""),
		ProjectName:    "Default Project",
		ProjectSlug:    h.defaultProjectSlug(),
		FieldErrors:    map[string]string{},
	}
}

func (h *SignupHandler) render(w http.ResponseWriter, r *http.Request, view templates.SignupCompleteView) {
	Render(w, r, templates.SignupCompleteLayout(h.Base(r, "Complete signup"), view))
}
