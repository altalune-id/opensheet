package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/user"
	"altalune.id/template/internal/web"
	"altalune.id/template/internal/web/templates"
)

// OnboardingHandler wraps the user Service for the T&C + rename form.
type OnboardingHandler struct {
	Deps
	Users *user.Service
}

// NewOnboardingHandler wires the handler.
func NewOnboardingHandler(d Deps, users *user.Service) *OnboardingHandler {
	return &OnboardingHandler{Deps: d, Users: users}
}

// GetOnboarding renders the T&C + display name form.
func (h *OnboardingHandler) GetOnboarding(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.LoadSession(r)
	if !ok {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	Render(w, r, templates.OnboardingLayout(h.Base(r, "Welcome"), templates.OnboardingView{
		Email:       p.Email,
		DisplayName: p.Name,
		ReturnTo:    SanitizeReturnTo(r.URL.Query().Get("return_to")),
	}))
}

// PostOnboarding validates + accepts terms + renames + refreshes the session record.
func (h *OnboardingHandler) PostOnboarding(w http.ResponseWriter, r *http.Request) {
	p, sid, ok := h.LoadSession(r)
	if !ok {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	returnTo := SanitizeReturnTo(r.PostForm.Get("return_to"))
	name := strings.TrimSpace(r.PostForm.Get("display_name"))
	if name == "" {
		h.renderErr(w, r, p, "Display name is required.", name, returnTo)
		return
	}
	if r.PostForm.Get("accept_terms") != "1" {
		h.renderErr(w, r, p, "Please accept the Terms of Service to continue.", name, returnTo)
		return
	}
	if err := h.Users.Rename(r.Context(), p.UserID, name); err != nil {
		h.LogErr("web onboarding: rename", err)
		h.renderErr(w, r, p, "Could not save. Please try again.", name, returnTo)
		return
	}
	if err := h.Users.AcceptTerms(r.Context(), p.UserID); err != nil {
		h.LogErr("web onboarding: accept terms", err)
		h.renderErr(w, r, p, "Could not save. Please try again.", name, returnTo)
		return
	}
	u, err := h.Users.ByID(r.Context(), p.UserID)
	if err != nil {
		h.LogErr("web onboarding: reload", err)
		h.renderErr(w, r, p, "Could not save. Please try again.", name, returnTo)
		return
	}
	refreshed := p
	refreshed.Name = u.Name
	_ = h.Sessions.Save(r.Context(), sid, refreshed, time.Now().Add(web.SessionTTL))
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, returnTo), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
}

// UserLookup is the tiny surface RequireOnboarded needs.
type UserLookup interface {
	ByID(ctx context.Context, id uuid.UUID) (*user.User, error)
}

// RequireOnboarded is middleware that bounces users to /onboarding until they accept the ToS.
func RequireOnboarded(deps Deps, users UserLookup) func(http.Handler) http.Handler {
	basePath := deps.Cfg.HTTP.BasePath
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, _, ok := deps.LoadSession(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			path := r.URL.Path
			if strings.HasSuffix(path, "/onboarding") || strings.HasSuffix(path, "/logout") || strings.Contains(path, "/static/") {
				next.ServeHTTP(w, r)
				return
			}
			u, err := users.ByID(r.Context(), p.UserID)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			if u.TermsAcceptedAt == nil {
				http.Redirect(w, r, web.Path(basePath, "/onboarding"), http.StatusSeeOther)
				return
			}
			ctx := session.PrincipalInto(r.Context(), p)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Register wires the onboarding routes onto mux.
func (h *OnboardingHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /onboarding", h.GetOnboarding)
	mux.HandleFunc("POST /onboarding", h.PostOnboarding)
}

func (h *OnboardingHandler) renderErr(w http.ResponseWriter, r *http.Request, p session.Principal, msg, name, returnTo string) {
	Render(w, r, templates.OnboardingLayout(h.Base(r, "Welcome"), templates.OnboardingView{
		Email:       p.Email,
		DisplayName: name,
		ReturnTo:    returnTo,
		Error:       msg,
	}))
}
