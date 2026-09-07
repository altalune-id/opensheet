package handlers

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/invite"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/web"
	"altalune.id/template/internal/web/templates"
)

// InviteHandler owns /orgs/{slug}/invites and /invites/accept.
type InviteHandler struct {
	Deps
	Invites *invite.Service
}

// NewInviteHandler wires the handler.
func NewInviteHandler(d Deps, orgs *org.Service, invites *invite.Service) *InviteHandler {
	d.Orgs = orgs
	return &InviteHandler{Deps: d, Invites: invites}
}

// GetList renders /orgs/{slug}/invites.
func (h *InviteHandler) GetList(w http.ResponseWriter, r *http.Request) {
	p, _, authed := h.LoadSession(r)
	if !authed {
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	slug := r.PathValue("org")
	o, r, ok := h.OrgScopeFor(w, r, p, slug)
	if !ok {
		return
	}
	items, err := h.Invites.ListPending(r.Context())
	if err != nil {
		h.LogErr("web invite: list", err)
		h.ErrorPage(w, r, http.StatusInternalServerError, "List failed", "Could not load invites.", err)
		return
	}
	canManage, _ := h.isManager(r.Context(), o.ID, p.UserID)
	Render(w, r, templates.InvitesLayout(h.LayoutForOrg(r, "Invites", slug, "invites"), templates.InvitesView{
		OrgSlug: slug, Invites: inviteRows(items), CanManage: canManage, Disabled: !h.Caps.InvitesEnabled,
	}))
}

// PostSend handles POST /orgs/{slug}/invites.
func (h *InviteHandler) PostSend(w http.ResponseWriter, r *http.Request) {
	p, _, authed := h.LoadSession(r)
	if !authed {
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	slug := r.PathValue("org")
	o, r, ok := h.OrgScopeFor(w, r, p, slug)
	if !ok {
		return
	}
	canManage, _ := h.isManager(r.Context(), o.ID, p.UserID)
	if !canManage {
		h.ErrorPage(w, r, http.StatusForbidden, "Not allowed", "Only owners and admins can invite.")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	email := strings.TrimSpace(r.PostForm.Get("email"))
	roleStr := strings.TrimSpace(r.PostForm.Get("role"))
	role := invite.Role(roleStr)
	if !role.IsValid() {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad role", "Role must be admin or member.")
		return
	}
	if _, err := h.Invites.Send(r.Context(), invite.SendRequest{Email: email, Role: role}); err != nil {
		h.LogErr("web invite: send", err)
		if invite.IsInvitesDisabledError(err) {
			h.ErrorPage(w, r, http.StatusConflict, "Invites disabled", err.Error(), err)
			return
		}
		h.ErrorPage(w, r, http.StatusBadRequest, "Send failed", err.Error())
		return
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/orgs/"+slug+"/invites"), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
}

// PostRevoke handles POST /orgs/{slug}/invites/{id}/revoke.
func (h *InviteHandler) PostRevoke(w http.ResponseWriter, r *http.Request) {
	p, _, authed := h.LoadSession(r)
	if !authed {
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	slug := r.PathValue("org")
	o, r, ok := h.OrgScopeFor(w, r, p, slug)
	if !ok {
		return
	}
	canManage, _ := h.isManager(r.Context(), o.ID, p.UserID)
	if !canManage {
		h.ErrorPage(w, r, http.StatusForbidden, "Not allowed", "Only owners and admins can revoke invites.")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad id", "Malformed invite id.")
		return
	}
	if err := h.Invites.Revoke(r.Context(), id); err != nil {
		h.LogErr("web invite: revoke", err)
		if invite.IsNotFoundError(err) {
			h.ErrorPage(w, r, http.StatusNotFound, "Invite not found", "That invite no longer exists.", err)
			return
		}
		// SECURITY: err.Error() names internal ids, so it stays in the log and never reaches the page.
		h.ErrorPage(w, r, http.StatusInternalServerError, "Revoke failed", "Could not revoke that invite.")
		return
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/orgs/"+slug+"/invites"), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
}

// GetAccept handles GET /invites/accept?token=...
func (h *InviteHandler) GetAccept(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		h.ErrorPage(w, r, http.StatusBadRequest, "Missing token", "The invite link is malformed.")
		return
	}
	p, sid, authed := h.LoadSession(r)
	if !authed {
		web.SetCookie(w, web.CookieOpts{
			Name:         web.InviteCookieName,
			Value:        web.SignCookie(h.SecretBytes(), token),
			BasePath:     h.Cfg.HTTP.BasePath,
			CookieSecure: h.Cfg.HTTP.CookieSecure,
			MaxAge:       int((15 * time.Minute).Seconds()),
		})
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	res, err := h.Invites.Accept(r.Context(), invite.AcceptRequest{
		Token: token,
		Email: p.Email,
		Name:  p.Name,
	})
	if err != nil {
		switch {
		case invite.IsNotFoundError(err), invite.IsExpiredError(err), invite.IsAlreadyUsedError(err):
			h.ErrorPage(w, r, http.StatusGone, "Invite not usable", err.Error())
			return
		case invite.IsTokenMismatchError(err):
			h.ErrorPage(w, r, http.StatusForbidden, "Invite mismatch", "This invite is for a different email.")
			return
		default:
			h.LogErr("web invite: accept", err)
			// SECURITY: err.Error() names internal ids, so the page shows the code and request id instead.
			h.ErrorPage(w, r, http.StatusInternalServerError, "Accept failed", "Could not accept that invite.", err)
			return
		}
	}
	o, err := h.Orgs.ByID(r.Context(), res.Invite.OrgID)
	if err != nil {
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/orgs"), http.StatusSeeOther)
		return
	}
	web.ClearCookie(w, web.InviteCookieName, h.Cfg.HTTP.BasePath, h.Cfg.HTTP.CookieSecure)
	// Rehydrate the session with the just-joined org so the dashboard renders the new tenant immediately — the pre-invite Principal has ActiveOrgID = uuid.Nil.
	p.ActiveOrgID = o.ID
	if h.Projects != nil {
		tctx := tenant.Into(r.Context(), tenant.Context{OrgID: o.ID, UserID: p.UserID})
		if projects, plErr := h.Projects.List(tctx, o.ID); plErr == nil && len(projects) > 0 {
			p.ActiveProjectID = projects[0].ID
		}
	}
	if err := h.UpdateSession(r, sid, p); err != nil {
		h.LogErr("web invite: refresh session", err)
	}
	dest := "/orgs/" + o.Slug
	if h.Cfg.Compliance.RequireAcceptance && p.TermsAcceptedAt.IsZero() {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/welcome")+"?return_to="+url.QueryEscape(dest), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, dest), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
}

// SECURITY: ctx must already carry the tenant scope; MembershipOf is RLS-filtered by org.
func (h *InviteHandler) isManager(ctx context.Context, orgID, userID uuid.UUID) (bool, error) {
	m, err := h.Orgs.MembershipOf(ctx, orgID, userID)
	if err != nil {
		return false, err
	}
	return m.Role == org.RoleOwner || m.Role == org.RoleAdmin, nil
}

func inviteRows(items []*invite.Invite) []templates.InviteRow {
	out := make([]templates.InviteRow, 0, len(items))
	for _, i := range items {
		out = append(out, templates.InviteRow{
			ID:        i.ID.String(),
			Email:     i.Email,
			Role:      string(i.Role),
			ExpiresAt: i.ExpiresAt.Format(time.RFC3339),
		})
	}
	return out
}

// Register wires all invite routes onto the mux.
func (h *InviteHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /orgs/{org}/invites", h.GetList)
	mux.HandleFunc("POST /orgs/{org}/invites", h.PostSend)
	mux.HandleFunc("POST /orgs/{org}/invites/{id}/revoke", h.PostRevoke)
	mux.HandleFunc("GET /invites/accept", h.GetAccept)
}
