package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"

	"altalune.id/template/authl"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/auth"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/user"
	"altalune.id/template/internal/web"
	"altalune.id/template/internal/web/templates"
)

// AuthHandler bundles the login/logout/callback routes into a single receiver.
type AuthHandler struct {
	Deps
	Auth     *auth.Service
	Users    *user.Service
	Orgs     *org.Service
	Projects *project.Service
	AltAuth  *authl.Client
	Required *atomic.Bool
}

// NewAuthHandler wires the handler.
func NewAuthHandler(d Deps, authSvc *auth.Service, users *user.Service, orgs *org.Service, projects *project.Service, ac *authl.Client, required *atomic.Bool) *AuthHandler {
	return &AuthHandler{Deps: d, Auth: authSvc, Users: users, Orgs: orgs, Projects: projects, AltAuth: ac, Required: required}
}

// resolveActiveTenant populates ActiveOrgID / ActiveProjectID on the principal from the user's first membership + project.
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

// localAuthEnabled reports whether the login page should render the local email+password form.
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
		lastEmail = h.AltAuth.LastKnownUser(r)
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

// pendingInviteDest verifies the invite cookie set by GetAccept before the login round-trip and returns the /invites/accept URL to resume the flow; returns "" when no valid invite is pending.
func (h *AuthHandler) pendingInviteDest(r *http.Request) string {
	c, err := r.Cookie(web.InviteCookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	token, err := web.VerifyCookie(h.SecretBytes(), c.Value)
	if err != nil || token == "" {
		return ""
	}
	return web.Path(h.Cfg.HTTP.BasePath, "/invites/accept") + "?token=" + url.QueryEscape(token)
}

// OIDCComplete is the authl OnComplete callback — JIT-provisions + writes the session.
func (h *AuthHandler) OIDCComplete(ctx context.Context, w http.ResponseWriter, r *http.Request, ident *authl.Identity) error {
	if h.Auth == nil {
		http.Error(w, "OIDC not configured", http.StatusInternalServerError)
		return errors.New("web auth: OIDC callback with nil auth service")
	}
	principal, err := h.Auth.LoginOIDC(ctx, auth.OIDCClaims{
		Issuer:  h.Cfg.OIDC.Issuer,
		Subject: ident.Subject,
		Email:   ident.Email,
		Name:    ident.Name,
	})
	if err != nil {
		if user.IsNotInvitedError(err) || auth.IsNotInvitedError(err) || isNotInvitedApp(err) {
			h.renderNotInvited(w, r)
			return nil
		}
		h.LogErr("web auth: oidc onboard", err)
		h.ErrorPage(w, r, http.StatusForbidden, "Not permitted", err.Error(), err)
		return nil
	}
	principal.IDToken = ident.IDToken
	principal = h.reconcileGenesis(ctx, principal)
	principal = h.resolveActiveTenant(ctx, principal)
	if err := h.WriteSession(w, r, principal); err != nil {
		h.LogErr("web auth: oidc write session", err)
		h.ErrorPage(w, r, http.StatusInternalServerError, "Sign-in failed", "Could not persist session.", err)
		return nil //nolint:nilerr // response already written via ErrorPage; returning err would double-write via authl.writeErr
	}
	if h.Required != nil && h.Required.Load() {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/onboard/complete"), http.StatusSeeOther)
		return nil
	}
	if dest := h.pendingInviteDest(r); dest != "" {
		http.Redirect(w, r, dest, http.StatusSeeOther) //nolint:gosec // G710: dest is a server-built relative path; token comes from an HMAC-verified cookie via pendingInviteDest.
		return nil
	}
	if h.Cfg.Mode == config.ModeCloud && principal.ActiveOrgID == uuid.Nil {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/signup/complete"), http.StatusSeeOther)
		return nil
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, ""), http.StatusSeeOther)
	return nil
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

// PostLogout clears the local session and, when cfg.OIDC.EndSessionOnLogout is set, also terminates the IdP session via RP-Initiated Logout. Off by default so the "Continue with X" hint stays seamless — subsequent OIDC logins reuse the OP session instead of prompting for credentials.
func (h *AuthHandler) PostLogout(w http.ResponseWriter, r *http.Request) {
	var idToken string
	if p, _, ok := h.LoadSession(r); ok && p.Source == session.SourceOIDC {
		idToken = p.IDToken
	}
	h.ClearSession(w, r)
	if h.Cfg.OIDC.EndSessionOnLogout && h.AltAuth != nil && idToken != "" {
		if endURL := h.AltAuth.EndSessionURL(idToken, h.postLogoutRedirectURL(), ""); endURL != "" {
			http.Redirect(w, r, endURL, http.StatusSeeOther)
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

// Register wires all routes onto mux; OIDC routes mount only when authl.Client is non-nil.
func (h *AuthHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", h.GetLogin)
	mux.HandleFunc("POST /login", h.PostLogin)
	mux.HandleFunc("GET /admin-login", h.GetAdminLogin)
	mux.HandleFunc("POST /logout", h.PostLogout)
	if h.AltAuth != nil {
		mux.Handle("GET /login/oidc", h.AltAuth.StartHandler())
		mux.Handle("GET /oauth/callback", h.AltAuth.CallbackHandler(h.OIDCComplete))
	}
}
