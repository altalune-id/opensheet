package handlers

import (
	"cmp"
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/onboard"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/user"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/templates"
	slugs "altalune.id/opensheet/slug"
)

const (
	minOnboardPasswordLen = 8
	defaultProjectName    = "Default Project"
)

// SetupCookieName carries the /onboard setup token across the OIDC round-trip.
const SetupCookieName = "opensheet_setup"

const setupCookieTTL = 30 * time.Minute

// OnboardHandler renders and processes the first-time bootstrap flow at /onboard.
type OnboardHandler struct {
	Deps
	Users    *user.Service
	Orgs     *org.Service
	Projects *project.Service
	Onboards *onboard.Service
	Required *atomic.Bool

	// OnComplete runs after onboarding completes; it owns clearing Required on every instance.
	OnComplete func(ctx context.Context)

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
	onComplete func(ctx context.Context),
	setupToken string,
) *OnboardHandler {
	return &OnboardHandler{
		Deps:       d,
		Users:      users,
		Orgs:       orgs,
		Projects:   projects,
		Onboards:   onboards,
		Required:   required,
		OnComplete: onComplete,
		SetupToken: setupToken,
	}
}

// Register wires the /onboard routes onto mux.
func (h *OnboardHandler) Register(mux web.Mux) {
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
	value, err := web.VerifyCookie(h.Key(web.PurposeOnboard), c.Value)
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

func (h *OnboardHandler) rememberSetupToken(w http.ResponseWriter, r *http.Request) {
	if h.SetupToken == "" {
		return
	}
	web.SetCookie(w, web.CookieOpts{
		Name:         SetupCookieName,
		Value:        web.SignCookie(h.Key(web.PurposeOnboard), h.SetupToken),
		BasePath:     h.Cfg.HTTP.BasePath,
		CookieSecure: h.SecureCookie(r),
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
	h.rememberSetupToken(w, r)
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
	view := h.defaultView()
	view.Email, view.Name = email, name
	h.applyPostedOrgAndProject(r, &view)

	if email == "" {
		view.FieldErrors["email"] = onboardErrEmailRequired
	}
	if name == "" {
		view.FieldErrors["name"] = onboardErrNameRequired
	}
	if len(password) < minOnboardPasswordLen {
		view.FieldErrors["password"] = onboardErrPasswordShort
	}
	checkOnboardOrgAndProject(view)
	if len(view.FieldErrors) > 0 {
		h.render(w, r, view)
		return
	}

	if !h.stillRequired(w, r) {
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
			view.FieldErrors["email"] = onboardErrEmailInvalid
		case user.IsInvalidNameError(err):
			view.FieldErrors["name"] = onboardErrNameInvalid
		case user.IsAlreadyExistsError(err):
			view.FieldErrors["email"] = onboardErrEmailTaken
		default:
			h.LogErr("web onboard: create user", err)
			view.Error = "Could not create admin."
			view.ErrorCode = ErrorRef(err)
		}
		h.render(w, r, view)
		return
	}

	o, err := h.Orgs.BootstrapSingleton(r.Context(), view.OrgSlug, view.OrgName, u.ID)
	if err != nil {
		if !orgFieldError(view.FieldErrors, err) {
			h.LogErr("web onboard: bootstrap org", err)
			view.Error = "Could not create organization."
			view.ErrorCode = ErrorRef(err)
		}
		h.render(w, r, view)
		return
	}
	view.OrgSlug = o.Slug

	p, projErr := h.projectForOnboard(r, o.ID, u.ID, view.ProjectSlug, view.ProjectName)
	if projErr != nil {
		if !projectFieldError(view.FieldErrors, projErr) {
			h.LogErr("web onboard: create project", projErr)
			view.Error = "Could not create the first project."
		}
		h.render(w, r, view)
		return
	}
	view.ProjectSlug = p.Slug

	if _, err := h.Onboards.Complete(r.Context(), u.ID, onboard.MethodWebOnboard); err != nil {
		if !onboard.IsAlreadyOnboardedError(err) {
			h.LogErr("web onboard: mark bootstrap", err)
			view.Error = "Could not mark deployment as onboarded."
			view.ErrorCode = ErrorRef(err)
			h.render(w, r, view)
			return
		}
	}
	h.completed(r.Context())

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
	h.rememberSetupToken(w, r)
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
	view := h.oidcFinalizeView(p, "", nil)
	h.applyPostedOrgAndProject(r, &view)

	checkOnboardOrgAndProject(view)
	if len(view.FieldErrors) > 0 {
		h.render(w, r, view)
		return
	}

	if !h.stillRequired(w, r) {
		return
	}
	if err := h.Users.Promote(r.Context(), p.UserID); err != nil {
		h.LogErr("web onboard: promote oidc admin", err)
		view.Error = "Could not promote admin."
		view.ErrorCode = ErrorRef(err)
		h.render(w, r, view)
		return
	}
	o, err := h.Orgs.BootstrapSingleton(r.Context(), view.OrgSlug, view.OrgName, p.UserID)
	if err != nil {
		if !orgFieldError(view.FieldErrors, err) {
			h.LogErr("web onboard: bootstrap org for oidc admin", err)
			view.Error = "Could not create the first organization."
			view.ErrorCode = ErrorRef(err)
		}
		h.render(w, r, view)
		return
	}
	view.OrgSlug = o.Slug
	proj, projErr := h.projectForOnboard(r, o.ID, p.UserID, view.ProjectSlug, view.ProjectName)
	if projErr != nil {
		if !projectFieldError(view.FieldErrors, projErr) {
			h.LogErr("web onboard: bootstrap project for oidc admin", projErr)
			view.Error = "Could not create the first project."
		}
		h.render(w, r, view)
		return
	}
	view.ProjectSlug = proj.Slug
	if _, err := h.Onboards.Complete(r.Context(), p.UserID, onboard.MethodWebOnboard); err != nil {
		if !onboard.IsAlreadyOnboardedError(err) {
			h.LogErr("web onboard: oidc complete", err)
			view.Error = "Could not mark deployment as onboarded."
			view.ErrorCode = ErrorRef(err)
			h.render(w, r, view)
			return
		}
	}
	h.completed(r.Context())
	p.IsAdmin = true
	p.ActiveOrgID = o.ID
	p.ActiveProjectID = projectID(proj)
	if err := h.UpdateSession(r, sid, p); err != nil {
		h.LogErr("web onboard: refresh session", err)
	}
	http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/"), http.StatusSeeOther)
}

// SECURITY: the in-process gate can be stale on a replica that booted before onboarding finished elsewhere, so the DB decides before any admin is created or promoted.
func (h *OnboardHandler) stillRequired(w http.ResponseWriter, r *http.Request) bool {
	required, err := h.Onboards.Required(r.Context())
	if err != nil {
		h.LogErr("web onboard: re-check required", err)
		h.ErrorPage(w, r, http.StatusServiceUnavailable, "Setup unavailable", "Could not confirm the onboarding state. Please try again.")
		return false
	}
	if required {
		return true
	}
	if h.Required != nil {
		h.Required.Store(false)
	}
	http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/"), http.StatusSeeOther)
	return false
}

func (h *OnboardHandler) completed(ctx context.Context) {
	if h.OnComplete != nil {
		h.OnComplete(ctx)
	}
}

func (h *OnboardHandler) oidcFinalizeView(p session.Principal, errMsg string, fieldErrs map[string]string) templates.OnboardView {
	if fieldErrs == nil {
		fieldErrs = map[string]string{}
	}
	return templates.OnboardView{
		OIDCFinalize: true,
		Email:        p.Email,
		Name:         cmp.Or(strings.TrimSpace(p.Name), strings.TrimSpace(p.Email)),
		OrgSlug:      firstRunSlug(h.Cfg.Tenant.SingletonOrg.Slug),
		OrgName:      strings.TrimSpace(h.Cfg.Tenant.SingletonOrg.Name),
		ProjectSlug:  firstRunSlug(h.Cfg.Tenant.PersonalProjectSlug),
		ProjectName:  defaultProjectName,
		FieldErrors:  fieldErrs,
		Error:        errMsg,
		SetupToken:   h.SetupToken,
	}
}

func (h *OnboardHandler) defaultView() templates.OnboardView {
	return templates.OnboardView{
		LocalAuth:   h.localOnboardAllowed(),
		OIDCAuth:    h.Caps.ExternalIdentity,
		OrgSlug:     firstRunSlug(h.Cfg.Tenant.SingletonOrg.Slug),
		OrgName:     h.Cfg.Tenant.SingletonOrg.Name,
		ProjectSlug: firstRunSlug(h.Cfg.Tenant.PersonalProjectSlug),
		ProjectName: defaultProjectName,
		FieldErrors: map[string]string{},
		SetupToken:  h.SetupToken,
	}
}

func firstRunSlug(configured string) string {
	return cmp.Or(strings.TrimSpace(configured), slugs.Generate())
}

func (h *OnboardHandler) applyPostedOrgAndProject(r *http.Request, view *templates.OnboardView) {
	view.OrgSlug = cmp.Or(strings.TrimSpace(r.PostForm.Get("org_slug")), strings.TrimSpace(h.Cfg.Tenant.SingletonOrg.Slug))
	view.OrgName = strings.TrimSpace(r.PostForm.Get("org_name"))
	view.ProjectSlug = cmp.Or(strings.TrimSpace(r.PostForm.Get("project_slug")), strings.TrimSpace(h.Cfg.Tenant.PersonalProjectSlug))
	view.ProjectName = cmp.Or(strings.TrimSpace(r.PostForm.Get("project_name")), defaultProjectName)
}

//i18n:use onboard.error.*
const (
	onboardErrEmailRequired      = "onboard.error.email_required"
	onboardErrEmailInvalid       = "onboard.error.email_invalid"
	onboardErrEmailTaken         = "onboard.error.email_taken"
	onboardErrNameRequired       = "onboard.error.name_required"
	onboardErrNameInvalid        = "onboard.error.name_invalid"
	onboardErrPasswordShort      = "onboard.error.password_short"
	onboardErrOrgNameRequired    = "onboard.error.org_name_required"
	onboardErrOrgNameInvalid     = "onboard.error.org_name_invalid"
	onboardErrProjectNameInvalid = "onboard.error.project_name_invalid"
	onboardErrSlugInvalid        = "onboard.error.slug_invalid"
	onboardErrSlugTaken          = "onboard.error.slug_taken"
)

func checkOnboardOrgAndProject(view templates.OnboardView) {
	if view.OrgSlug != "" && org.ValidateSlug(view.OrgSlug) != nil {
		view.FieldErrors["org_slug"] = onboardErrSlugInvalid
	}
	switch {
	case view.OrgName == "":
		view.FieldErrors["org_name"] = onboardErrOrgNameRequired
	case org.ValidateName(view.OrgName) != nil:
		view.FieldErrors["org_name"] = onboardErrOrgNameInvalid
	}
	if view.ProjectSlug != "" && project.ValidateSlug(view.ProjectSlug) != nil {
		view.FieldErrors["project_slug"] = onboardErrSlugInvalid
	}
	if project.ValidateName(view.ProjectName) != nil {
		view.FieldErrors["project_name"] = onboardErrProjectNameInvalid
	}
}

func orgFieldError(errs map[string]string, err error) bool {
	switch {
	case org.IsInvalidSlugError(err):
		errs["org_slug"] = onboardErrSlugInvalid
	case org.IsUnreadableExistingOrgError(err):
		errs["org_slug"] = onboardErrSlugTaken
	case org.IsInvalidNameError(err):
		errs["org_name"] = onboardErrOrgNameInvalid
	default:
		return false
	}
	return true
}

func projectFieldError(errs map[string]string, err error) bool {
	switch {
	case project.IsInvalidSlugError(err):
		errs["project_slug"] = onboardErrSlugInvalid
	case project.IsAlreadyExistsError(err):
		errs["project_slug"] = onboardErrSlugTaken
	case project.IsInvalidNameError(err):
		errs["project_name"] = onboardErrProjectNameInvalid
	default:
		return false
	}
	return true
}

// SECURITY: cloud mode offers the local admin path only when genesis.breakGlass=true is explicitly opted in.
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
