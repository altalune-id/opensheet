package handlers

import (
	"cmp"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/onboard"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/user"
	"altalune.id/template/internal/web"
	"altalune.id/template/internal/web/templates"
)

const minOnboardPasswordLen = 8

// SetupCookieName carries the /onboard setup token across the OIDC round-trip.
const SetupCookieName = "altempl_setup"

const setupCookieTTL = 30 * time.Minute

// OnboardHandler renders and processes the first-time bootstrap flow at /onboard.
type OnboardHandler struct {
	Deps
	Users    *user.Service
	Orgs     *org.Service
	Projects *project.Service
	Onboards *onboard.Service
	Required *atomic.Bool

	// SetupToken gates every /onboard route while it is non-empty.
	SetupToken string
}

// NewOnboardHandler wires the /onboard handler.
func NewOnboardHandler(
	d Deps,
	users *user.Service,
	orgs *org.Service,
	projects *project.Service,
	onboards *onboard.Service,
	required *atomic.Bool,
	setupToken string,
) *OnboardHandler {
	return &OnboardHandler{
		Deps:       d,
		Users:      users,
		Orgs:       orgs,
		Projects:   projects,
		Onboards:   onboards,
		Required:   required,
		SetupToken: setupToken,
	}
}

// Register wires the /onboard routes onto mux.
func (h *OnboardHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /onboard", h.GetOnboard)
	mux.HandleFunc("POST /onboard/local", h.PostLocal)
	mux.HandleFunc("GET /onboard/oidc", h.GetOIDCStart)
	mux.HandleFunc("GET /onboard/complete", h.GetOIDCComplete)
	mux.HandleFunc("POST /onboard/complete", h.PostOIDCComplete)
}

// SECURITY: constant-time comparison — the token gates a privileged action and an attacker can retry freely.
func (h *OnboardHandler) tokenOK(r *http.Request) bool {
	if h.SetupToken == "" {
		return true
	}
	if constantTimeEqual(r.FormValue("token"), h.SetupToken) {
		return true
	}
	c, err := r.Cookie(SetupCookieName)
	if err != nil {
		return false
	}
	value, err := web.VerifyCookie(h.SecretBytes(), c.Value)
	if err != nil {
		return false
	}
	return constantTimeEqual(value, h.SetupToken)
}

func constantTimeEqual(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// SECURITY: the refusal never reveals whether a token was supplied but wrong.
func (h *OnboardHandler) denySetup(w http.ResponseWriter, r *http.Request) {
	h.ErrorPage(w, r, http.StatusForbidden, "Setup is locked",
		"First-time setup requires the one-time setup token printed in the server logs.")
}

func (h *OnboardHandler) rememberSetupToken(w http.ResponseWriter) {
	if h.SetupToken == "" {
		return
	}
	web.SetCookie(w, web.CookieOpts{
		Name:         SetupCookieName,
		Value:        web.SignCookie(h.SecretBytes(), h.SetupToken),
		BasePath:     h.Cfg.HTTP.BasePath,
		CookieSecure: h.Cfg.HTTP.CookieSecure,
		MaxAge:       int(setupCookieTTL.Seconds()),
	})
}

// GetOnboard renders the setup page.
func (h *OnboardHandler) GetOnboard(w http.ResponseWriter, r *http.Request) {
	if h.Required != nil && !h.Required.Load() {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/"), http.StatusSeeOther)
		return
	}
	if !h.tokenOK(r) {
		h.denySetup(w, r)
		return
	}
	h.rememberSetupToken(w)
	Render(w, r, templates.OnboardLayout(h.Base(r, "First-time setup"), h.defaultView()))
}

// PostLocal handles the local-admin bootstrap form.
//
//nolint:gocyclo,funlen // linear setup flow reads more clearly as one function.
func (h *OnboardHandler) PostLocal(w http.ResponseWriter, r *http.Request) {
	if h.Required != nil && !h.Required.Load() {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/"), http.StatusSeeOther)
		return
	}
	if !h.tokenOK(r) {
		h.denySetup(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderErr(w, r, "Bad form.", h.defaultView())
		return
	}
	email := strings.TrimSpace(r.PostForm.Get("email"))
	name := strings.TrimSpace(r.PostForm.Get("name"))
	password := r.PostForm.Get("password")
	orgSlug := strings.TrimSpace(r.PostForm.Get("org_slug"))
	orgName := strings.TrimSpace(r.PostForm.Get("org_name"))
	projectSlug := strings.TrimSpace(r.PostForm.Get("project_slug"))
	projectName := strings.TrimSpace(r.PostForm.Get("project_name"))
	if projectSlug == "" {
		projectSlug = "default"
	}
	if projectName == "" {
		projectName = "Default Project"
	}

	view := h.defaultView()
	view.Email, view.Name = email, name
	view.OrgSlug, view.OrgName = orgSlug, orgName
	view.ProjectSlug, view.ProjectName = projectSlug, projectName
	view.FieldErrors = map[string]string{}

	if email == "" {
		view.FieldErrors["email"] = "Enter your email."
	}
	if name == "" {
		view.FieldErrors["name"] = "Enter your display name."
	}
	if len(password) < minOnboardPasswordLen {
		view.FieldErrors["password"] = "Use at least 8 characters."
	}
	if orgSlug == "" {
		view.FieldErrors["org_slug"] = "Enter an org slug."
	}
	if orgName == "" {
		view.FieldErrors["org_name"] = "Enter an org name."
	}
	if len(view.FieldErrors) > 0 {
		h.render(w, r, view)
		return
	}

	u, err := h.Users.Create(r.Context(), user.CreateRequest{
		Email:    email,
		Name:     name,
		Source:   user.SourceLocal,
		Password: password,
	})
	if err != nil {
		switch {
		case user.IsInvalidEmailError(err):
			view.FieldErrors["email"] = err.Error()
		case user.IsInvalidNameError(err):
			view.FieldErrors["name"] = err.Error()
		case user.IsAlreadyExistsError(err):
			view.FieldErrors["email"] = "A user with that email already exists."
		default:
			h.LogErr("web onboard: create user", err)
			view.Error = "Could not create admin."
			view.ErrorCode = ErrorRef(err)
		}
		h.render(w, r, view)
		return
	}

	o, err := h.Orgs.BootstrapSingleton(r.Context(), orgSlug, orgName, u.ID)
	if err != nil {
		h.LogErr("web onboard: bootstrap org", err)
		view.Error = "Could not create organization."
		view.ErrorCode = ErrorRef(err)
		h.render(w, r, view)
		return
	}

	p, projErr := h.projectForOnboard(r, o.ID, u.ID, projectSlug, projectName)
	if projErr != nil {
		h.LogErr("web onboard: create project", projErr)
		view.Error = "Could not create the first project."
		h.render(w, r, view)
		return
	}

	if _, err := h.Onboards.Complete(r.Context(), u.ID, onboard.MethodWebOnboard); err != nil {
		if !onboard.IsAlreadyOnboardedError(err) {
			h.LogErr("web onboard: mark bootstrap", err)
			view.Error = "Could not mark deployment as onboarded."
			view.ErrorCode = ErrorRef(err)
			h.render(w, r, view)
			return
		}
	}

	principal := session.Principal{
		UserID:          u.ID,
		Email:           u.Email,
		Name:            u.Name,
		Source:          session.SourceLocal,
		ActiveOrgID:     o.ID,
		ActiveProjectID: projectID(p),
		IsAdmin:         true,
		IssuedAt:        nowUTC(),
	}
	if err := h.WriteSession(w, r, principal); err != nil {
		h.LogErr("web onboard: write session", err)
		view.Error = "Setup completed, but sign-in failed. Please sign in manually."
		view.ErrorCode = ErrorRef(err)
		h.render(w, r, view)
		return
	}

	if h.Required != nil {
		h.Required.Store(false)
	}
	http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/"), http.StatusSeeOther)
}

// GetOIDCStart hands off to the OIDC login with a return path to /onboard/complete.
func (h *OnboardHandler) GetOIDCStart(w http.ResponseWriter, r *http.Request) {
	if h.Required != nil && !h.Required.Load() {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/"), http.StatusSeeOther)
		return
	}
	if !h.tokenOK(r) {
		h.denySetup(w, r)
		return
	}
	h.rememberSetupToken(w)
	dest := web.Path(h.Cfg.HTTP.BasePath, "/login/oidc") + "?return_to=" + url.QueryEscape(web.Path(h.Cfg.HTTP.BasePath, "/onboard/complete"))
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// GetOIDCComplete renders the finalize form so the freshly-signed-in OIDC user names the singleton org + first project before onboarding closes.
func (h *OnboardHandler) GetOIDCComplete(w http.ResponseWriter, r *http.Request) {
	if h.Required != nil && !h.Required.Load() {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/"), http.StatusSeeOther)
		return
	}
	if !h.tokenOK(r) {
		h.denySetup(w, r)
		return
	}
	p, _, ok := h.LoadSession(r)
	if !ok || p.UserID == [16]byte{} {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	h.render(w, r, h.oidcFinalizeView(p, "", nil))
}

//nolint:gocyclo,funlen // linear setup flow reads more clearly as one function.
func (h *OnboardHandler) PostOIDCComplete(w http.ResponseWriter, r *http.Request) {
	if h.Required != nil && !h.Required.Load() {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/"), http.StatusSeeOther)
		return
	}
	if !h.tokenOK(r) {
		h.denySetup(w, r)
		return
	}
	p, sid, ok := h.LoadSession(r)
	if !ok || p.UserID == [16]byte{} {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.render(w, r, h.oidcFinalizeView(p, "Bad form.", nil))
		return
	}
	orgSlug := strings.TrimSpace(r.PostForm.Get("org_slug"))
	orgName := strings.TrimSpace(r.PostForm.Get("org_name"))
	projectSlug := strings.TrimSpace(r.PostForm.Get("project_slug"))
	projectName := strings.TrimSpace(r.PostForm.Get("project_name"))
	if projectSlug == "" {
		projectSlug = "default"
	}
	if projectName == "" {
		projectName = "Default Project"
	}

	view := h.oidcFinalizeView(p, "", map[string]string{})
	view.OrgSlug, view.OrgName = orgSlug, orgName
	view.ProjectSlug, view.ProjectName = projectSlug, projectName

	if orgSlug == "" {
		view.FieldErrors["org_slug"] = "Enter an org slug."
	}
	if orgName == "" {
		view.FieldErrors["org_name"] = "Enter an org name."
	}
	if len(view.FieldErrors) > 0 {
		h.render(w, r, view)
		return
	}

	if err := h.Users.Promote(r.Context(), p.UserID); err != nil {
		h.LogErr("web onboard: promote oidc admin", err)
		view.Error = "Could not promote admin."
		view.ErrorCode = ErrorRef(err)
		h.render(w, r, view)
		return
	}
	o, err := h.Orgs.BootstrapSingleton(r.Context(), orgSlug, orgName, p.UserID)
	if err != nil {
		h.LogErr("web onboard: bootstrap org for oidc admin", err)
		view.Error = "Could not create the first organization."
		view.ErrorCode = ErrorRef(err)
		h.render(w, r, view)
		return
	}
	proj, projErr := h.projectForOnboard(r, o.ID, p.UserID, projectSlug, projectName)
	if projErr != nil {
		h.LogErr("web onboard: bootstrap project for oidc admin", projErr)
		view.Error = "Could not create the first project."
		h.render(w, r, view)
		return
	}
	if _, err := h.Onboards.Complete(r.Context(), p.UserID, onboard.MethodWebOnboard); err != nil {
		if !onboard.IsAlreadyOnboardedError(err) {
			h.LogErr("web onboard: oidc complete", err)
			view.Error = "Could not mark deployment as onboarded."
			view.ErrorCode = ErrorRef(err)
			h.render(w, r, view)
			return
		}
	}
	p.IsAdmin = true
	p.ActiveOrgID = o.ID
	p.ActiveProjectID = projectID(proj)
	if err := h.UpdateSession(r, sid, p); err != nil {
		h.LogErr("web onboard: refresh session", err)
	}
	if h.Required != nil {
		h.Required.Store(false)
	}
	http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/"), http.StatusSeeOther)
}

func (h *OnboardHandler) oidcFinalizeView(p session.Principal, errMsg string, fieldErrs map[string]string) templates.OnboardView {
	if fieldErrs == nil {
		fieldErrs = map[string]string{}
	}
	orgSlug := strings.TrimSpace(h.Cfg.Tenant.SingletonOrg.Slug)
	if orgSlug == "" {
		orgSlug = "default"
	}
	orgName := strings.TrimSpace(h.Cfg.Tenant.SingletonOrg.Name)
	return templates.OnboardView{
		OIDCFinalize: true,
		Email:        p.Email,
		Name:         cmp.Or(strings.TrimSpace(p.Name), strings.TrimSpace(p.Email)),
		OrgSlug:      orgSlug,
		OrgName:      orgName,
		ProjectSlug:  "default",
		ProjectName:  "Default Project",
		FieldErrors:  fieldErrs,
		Error:        errMsg,
		SetupToken:   h.SetupToken,
	}
}

func (h *OnboardHandler) defaultView() templates.OnboardView {
	return templates.OnboardView{
		LocalAuth:   h.localOnboardAllowed(),
		OIDCAuth:    h.Caps.ExternalIdentity,
		OrgSlug:     h.Cfg.Tenant.SingletonOrg.Slug,
		OrgName:     h.Cfg.Tenant.SingletonOrg.Name,
		ProjectSlug: "default",
		ProjectName: "Default Project",
		FieldErrors: map[string]string{},
		SetupToken:  h.SetupToken,
	}
}

// localOnboardAllowed reports whether the /onboard form should offer the local admin path.
// Selfhosted: always. Cloud: only when genesis.breakGlass=true is explicitly opted in.
func (h *OnboardHandler) localOnboardAllowed() bool {
	if h.Cfg.Mode != config.ModeCloud {
		return true
	}
	return h.Cfg.Genesis.BreakGlass
}

func (h *OnboardHandler) render(w http.ResponseWriter, r *http.Request, view templates.OnboardView) {
	Render(w, r, templates.OnboardLayout(h.Base(r, "First-time setup"), view))
}

func (h *OnboardHandler) renderErr(w http.ResponseWriter, r *http.Request, msg string, view templates.OnboardView) {
	view.Error = msg
	h.render(w, r, view)
}

func (h *OnboardHandler) projectForOnboard(r *http.Request, orgID, userID uuid.UUID, slug, name string) (*project.Project, error) {
	ctx := tenant.Into(r.Context(), tenant.Context{OrgID: orgID, UserID: userID})
	return h.Projects.BootstrapSystem(ctx, orgID, slug, name)
}

func projectID(p *project.Project) uuid.UUID {
	if p == nil {
		return uuid.Nil
	}
	return p.ID
}

func nowUTC() time.Time { return time.Now().UTC() }

// OnboardingGate is middleware that redirects every request to /onboard while required.Load() is true.
func OnboardingGate(basePath string, required *atomic.Bool) func(http.Handler) http.Handler {
	onboardPath := web.Path(basePath, "/onboard")
	allowPrefixes := []string{
		onboardPath,
		web.Path(basePath, "/static"),
		web.Path(basePath, "/oauth/callback"),
		web.Path(basePath, "/login/oidc"),
	}
	unprefixed := []string{"/healthz", "/readyz", "/robots.txt"}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if required == nil || !required.Load() {
				next.ServeHTTP(w, r)
				return
			}
			p := r.URL.Path
			for _, allowed := range unprefixed {
				if p == allowed {
					next.ServeHTTP(w, r)
					return
				}
			}
			for _, prefix := range allowPrefixes {
				if p == prefix || strings.HasPrefix(p, prefix+"/") {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Redirect(w, r, onboardPath, http.StatusSeeOther)
		})
	}
}
