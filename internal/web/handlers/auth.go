package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/altalune-id/oidcrp"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/auth"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/user"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/templates"
)

// AuthHandler bundles the login/logout/callback routes into a single receiver.
type AuthHandler struct {
	Deps
	Auth     *auth.Service
	Users    *user.Service
	Orgs     *org.Service
	Projects *project.Service
	AltAuth  *oidcrp.Client
	Required *atomic.Bool
}

// NewAuthHandler wires the handler.
func NewAuthHandler(d Deps, authSvc *auth.Service, users *user.Service, orgs *org.Service, projects *project.Service, ac *oidcrp.Client, required *atomic.Bool) *AuthHandler {
	return &AuthHandler{Deps: d, Auth: authSvc, Users: users, Orgs: orgs, Projects: projects, AltAuth: ac, Required: required}
}

func (h *AuthHandler) resolveActiveTenant(ctx context.Context, p session.Principal) session.Principal {
	if p.ActiveOrgID != [16]byte{} && p.ActiveProjectID != [16]byte{} {
		return p
	}
	if h.Orgs == nil {
		return p
	}
	orgs, err := h.Orgs.List(ctx, p.UserID)
	if err != nil || len(orgs) == 0 {
		if err != nil {
			h.LogErr("web auth: resolve orgs", err)
		}
		return p
	}
	if p.ActiveOrgID == [16]byte{} {
		p.ActiveOrgID = orgs[0].ID
	}
	if h.Projects == nil {
		return p
	}
	tctx := tenant.Into(ctx, tenant.Context{OrgID: p.ActiveOrgID, UserID: p.UserID})
	projects, err := h.Projects.List(tctx, p.ActiveOrgID)
	if err != nil || len(projects) == 0 {
		if err != nil {
			h.LogErr("web auth: resolve projects", err)
		}
		return p
	}
	if p.ActiveProjectID == [16]byte{} {
		p.ActiveProjectID = projects[0].ID
	}
	return p
}

func (h *AuthHandler) reconcileGenesis(ctx context.Context, p session.Principal) session.Principal {
	if h.Users == nil {
		return p
	}
	outcome, err := h.Users.ReconcileGenesisAdmin(ctx)
	if err != nil {
		h.LogErr("web auth: reconcile genesis admin", err)
		return p
	}
	if p.IsAdmin || outcome == user.OutcomeUnconfigured || outcome == user.OutcomeUnclaimed {
		return p
	}
	u, err := h.Users.ByID(ctx, p.UserID)
	if err != nil {
		h.LogErr("web auth: reload user after genesis reconcile", err)
		return p
	}
	p.IsAdmin = u.IsAdmin
	return p
}

func (h *AuthHandler) localAuthEnabled(ctx context.Context) bool {
	if h.Caps.LocalIdentity {
		return true
	}
	if h.Users == nil {
		return false
	}
	has, err := h.Users.HasLocalUsers(ctx)
	if err != nil {
		h.LogErr("auth: HasLocalUsers probe", err)
		return false
	}
	return has
}

// GetLogin renders the login page.
func (h *AuthHandler) GetLogin(w http.ResponseWriter, r *http.Request) {
	if p, _, ok := h.LoadSession(r); ok && p.UserID != [16]byte{} {
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, r.URL.Query().Get("return_to")), http.StatusSeeOther) //nolint:gosec // G710: return_to sanitized via ResolveReturnTo → SanitizeReturnTo
		return
	}
	var lastEmail string
	if h.AltAuth != nil {
		lastEmail = h.lastUser(r)
	}
	Render(w, r, templates.LoginLayout(h.Base(r, "Sign in"), templates.LoginView{
		ReturnTo:  SanitizeReturnTo(r.URL.Query().Get("return_to")),
		LastEmail: lastEmail,
		LocalAuth: h.localAuthEnabled(r.Context()),
	}))
}

// GetAdminLogin is the break-glass path.
func (h *AuthHandler) GetAdminLogin(w http.ResponseWriter, r *http.Request) {
	if p, _, ok := h.LoadSession(r); ok && p.UserID != [16]byte{} {
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, ""), http.StatusSeeOther)
		return
	}
	Render(w, r, templates.LoginLayout(h.Base(r, "Admin sign-in"), templates.LoginView{
		AdminForce: true,
		LocalAuth:  h.localAuthEnabled(r.Context()),
	}))
}

// PostLogin verifies local credentials and mints a session on success.
func (h *AuthHandler) PostLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	email := strings.TrimSpace(r.PostForm.Get("email"))
	password := r.PostForm.Get("password")
	admin := r.PostForm.Get("admin") == "1"

	principal, err := h.Auth.LoginLocal(r.Context(), auth.Credentials{Email: email, Password: password})
	if err != nil {
		msg := "Invalid credentials."
		if !auth.IsInvalidCredentialsError(err) {
			h.LogErr("web auth: local login", err)
			msg = "Sign-in failed."
		}
		Render(w, r, templates.LoginLayout(h.Base(r, "Sign in"), templates.LoginView{
			Error:      msg,
			AdminForce: admin,
			ReturnTo:   SanitizeReturnTo(r.PostForm.Get("return_to")),
			LocalAuth:  h.localAuthEnabled(r.Context()),
		}))
		return
	}
	principal = h.reconcileGenesis(r.Context(), principal)
	principal = h.resolveActiveTenant(r.Context(), principal)
	if err := h.WriteSession(w, r, principal); err != nil {
		h.ErrorPage(w, r, http.StatusInternalServerError, "Sign-in failed", "Could not persist session.")
		return
	}
	if dest := h.pendingInviteDest(r); dest != "" {
		http.Redirect(w, r, dest, http.StatusSeeOther) //nolint:gosec // G710: dest is a server-built relative path; token comes from an HMAC-verified cookie via pendingInviteDest.
		return
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, r.PostForm.Get("return_to")), http.StatusSeeOther) //nolint:gosec // G710: return_to sanitized via ResolveReturnTo → SanitizeReturnTo
}

func (h *AuthHandler) pendingInviteDest(r *http.Request) string {
	c, err := r.Cookie(web.InviteCookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	token, err := web.VerifyCookie(h.Key(web.PurposeInvite), c.Value)
	if err != nil || token == "" {
		return ""
	}
	return web.Path(h.Cfg.HTTP.BasePath, "/invites/accept") + "?token=" + url.QueryEscape(token)
}

// OIDCComplete JIT-provisions the user of a validated ID token and writes the session.
func (h *AuthHandler) OIDCComplete(ctx context.Context, w http.ResponseWriter, r *http.Request, tok *oidcrp.IDToken, returnTo string) {
	if h.Auth == nil {
		h.LogErr("web auth: oidc callback", errors.New("nil auth service"))
		h.signInFailed(w, r, http.StatusBadGateway, signInUnavailable)
		return
	}
	principal, err := h.Auth.LoginOIDC(ctx, auth.OIDCClaims{
		Issuer:        h.Cfg.OIDC.Issuer,
		Subject:       tok.Subject,
		Email:         tok.Email,
		EmailVerified: tok.EmailVerified,
		Name:          tok.Name,
	})
	if err != nil {
		switch {
		case user.IsNotInvitedError(err) || auth.IsNotInvitedError(err) || isNotInvitedApp(err):
			h.renderNotInvited(w, r)
		case user.IsEmailUnverifiedError(err):
			h.signInFailed(w, r, http.StatusForbidden, signInEmailUnverified, err)
		case user.IsIdentityConflictError(err):
			h.signInFailed(w, r, http.StatusForbidden, signInIdentityConflict, err)
		case isRefusal(err):
			h.signInFailed(w, r, http.StatusForbidden, signInNotPermitted, err)
		default:
			h.LogErr("web auth: oidc onboard", err)
			h.signInFailed(w, r, http.StatusInternalServerError, signInUnavailable, err)
		}
		return
	}
	principal.IDToken = tok.Raw
	principal = h.reconcileGenesis(ctx, principal)
	principal = h.resolveActiveTenant(ctx, principal)
	if err := h.WriteSession(w, r, principal); err != nil {
		h.LogErr("web auth: oidc write session", err)
		h.ErrorPage(w, r, http.StatusInternalServerError, "Sign-in failed", "Could not persist session.", err)
		return
	}
	h.setLastUser(w, r, tok.Email)
	if h.Required != nil && h.Required.Load() {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/onboard/complete"), http.StatusSeeOther)
		return
	}
	if dest := h.pendingInviteDest(r); dest != "" {
		http.Redirect(w, r, dest, http.StatusSeeOther) //nolint:gosec // G710: dest is a server-built relative path; token comes from an HMAC-verified cookie via pendingInviteDest.
		return
	}
	if h.Cfg.Mode == config.ModeCloud && principal.ActiveOrgID == uuid.Nil {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/signup/complete"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, returnTo), http.StatusSeeOther) //nolint:gosec // G710: return_to sanitized via ResolveReturnTo → SanitizeReturnTo
}

func isRefusal(err error) bool {
	ae, ok := apperror.AsAppError(err)
	if !ok {
		return false
	}
	switch ae.GRPCCode() {
	case codes.PermissionDenied, codes.FailedPrecondition, codes.InvalidArgument, codes.Unauthenticated:
		return true
	}
	return false
}

func isNotInvitedApp(err error) bool {
	ae, ok := apperror.AsAppError(err)
	if !ok {
		return false
	}
	return ae.Code() == apperror.CodeUserNotInvited
}

func (h *AuthHandler) renderNotInvited(w http.ResponseWriter, r *http.Request) {
	base := h.Base(r, "Not invited")
	tr := base.Translator
	title := "You haven't been invited"
	body := "This deployment is invite-only. Please contact an administrator to request an invitation."
	if tr != nil {
		title = tr.T("errors.not_invited.title")
		body = tr.T("errors.not_invited.body")
	}
	RenderStatus(w, r, http.StatusForbidden, templates.ErrorLayout(base, templates.ErrorView{
		Status: http.StatusForbidden, Title: title, Message: body,
	}))
}

// PostLogout clears the local session and, when cfg.OIDC.EndSessionOnLogout is set (the default), also ends the issuer session through RP-Initiated Logout.
func (h *AuthHandler) PostLogout(w http.ResponseWriter, r *http.Request) {
	var idToken string
	if p, _, ok := h.LoadSession(r); ok && p.Source == session.SourceOIDC {
		idToken = p.IDToken
	}
	h.ClearSession(w, r)
	if h.Cfg.OIDC.EndSessionOnLogout && h.AltAuth != nil && idToken != "" {
		endURL, err := h.AltAuth.EndSessionURL(r.Context(), idToken, h.postLogoutRedirectURL(), "")
		if err != nil {
			h.LogErr("web auth: end session url", err)
		}
		switch {
		case endURL == "":
		case !sameOrigin(endURL, h.Cfg.OIDC.Issuer):
			h.LogErr("web auth: end session url", errors.New("end_session_endpoint is not on the issuer's origin"))
		default:
			http.Redirect(w, r, endURL, http.StatusSeeOther) //nolint:gosec // G710: the end_session_endpoint of the issuer's own discovery.
			return
		}
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, ""), http.StatusSeeOther)
}

// NOTE: must match a `post_logout_redirect_uri` registered on the OIDC client.
func (h *AuthHandler) postLogoutRedirectURL() string {
	if h.Cfg.HTTP.BaseURL == "" {
		return ""
	}
	return strings.TrimRight(h.Cfg.HTTP.BaseURL, "/") + h.Cfg.HTTP.BasePath + "/"
}

// Register wires all routes onto mux; OIDC routes mount only when the relying party is configured.
func (h *AuthHandler) Register(mux web.Mux) {
	mux.HandleFunc("GET /login", h.GetLogin)
	mux.HandleFunc("POST /login", h.PostLogin)
	mux.HandleFunc("GET /admin-login", h.GetAdminLogin)
	mux.HandleFunc("POST /logout", h.PostLogout)
	if h.AltAuth != nil {
		mux.HandleFunc("GET /login/oidc", h.GetOIDCStart)
		mux.HandleFunc("GET /oauth/callback", h.GetOIDCCallback)
	}
}

func sameOrigin(a, b string) bool {
	o := web.Origin(a)
	return o != "" && o == web.Origin(b)
}
