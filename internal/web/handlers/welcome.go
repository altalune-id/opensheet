package handlers

import (
	"cmp"
	"net/http"
	"strings"
	"time"

	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/user"
	"altalune.id/template/internal/web"
	"altalune.id/template/internal/web/templates"
)

// WelcomeHandler renders the per-user welcome page (T&C accept + display name fixup).
type WelcomeHandler struct {
	Deps
	Users *user.Service
}

// NewWelcomeHandler wires the handler.
func NewWelcomeHandler(d Deps, users *user.Service) *WelcomeHandler {
	return &WelcomeHandler{Deps: d, Users: users}
}

// Register wires the /welcome routes onto mux.
func (h *WelcomeHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /welcome", h.GetWelcome)
	mux.HandleFunc("POST /welcome", h.PostWelcome)
}

// GetWelcome renders the welcome page; skips if the user already accepted and has a name.
func (h *WelcomeHandler) GetWelcome(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.LoadSession(r)
	if !ok || p.UserID == [16]byte{} {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	retTo := SanitizeReturnTo(r.URL.Query().Get("return_to"))
	if !h.needsWelcome(p) {
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, retTo), http.StatusSeeOther) //nolint:gosec // G710: return_to sanitized via ResolveReturnTo → SanitizeReturnTo
		return
	}
	Render(w, r, templates.WelcomeLayout(h.Base(r, "Welcome"), h.view(p, retTo, "")))
}

// PostWelcome stamps TermsAcceptedAt and updates the display name.
func (h *WelcomeHandler) PostWelcome(w http.ResponseWriter, r *http.Request) {
	p, sid, ok := h.LoadSession(r)
	if !ok || p.UserID == [16]byte{} {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, r, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	retTo := SanitizeReturnTo(r.PostForm.Get("return_to"))
	askAccept := h.Cfg.Compliance.RequireAcceptance
	askName := strings.TrimSpace(p.Name) == ""

	if askAccept && r.PostForm.Get("accept_terms") != "1" {
		Render(w, r, templates.WelcomeLayout(h.Base(r, "Welcome"), h.view(p, retTo, "Please tick the box to continue.")))
		return
	}
	if askName {
		name := strings.TrimSpace(r.PostForm.Get("name"))
		if name == "" {
			Render(w, r, templates.WelcomeLayout(h.Base(r, "Welcome"), h.view(p, retTo, "Enter your display name.")))
			return
		}
		if err := h.Users.Rename(r.Context(), p.UserID, name); err != nil {
			h.LogErr("welcome: rename", err)
			Render(w, r, templates.WelcomeLayout(h.Base(r, "Welcome"), h.view(p, retTo, "Could not save your name.")))
			return
		}
		p.Name = name
	}
	if askAccept {
		if err := h.Users.AcceptTerms(r.Context(), p.UserID); err != nil {
			h.LogErr("welcome: accept terms", err)
			Render(w, r, templates.WelcomeLayout(h.Base(r, "Welcome"), h.view(p, retTo, "Could not record your acceptance.")))
			return
		}
		p.TermsAcceptedAt = time.Now().UTC()
	}
	if err := h.UpdateSession(r, sid, p); err != nil {
		h.LogErr("welcome: update session", err)
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, retTo), http.StatusSeeOther) //nolint:gosec // G710: return_to sanitized via ResolveReturnTo → SanitizeReturnTo
}

func (h *WelcomeHandler) needsWelcome(p session.Principal) bool {
	if h.Cfg.Compliance.RequireAcceptance && p.TermsAcceptedAt.IsZero() {
		return true
	}
	return strings.TrimSpace(p.Name) == ""
}

func (h *WelcomeHandler) view(p session.Principal, retTo, errMsg string) templates.WelcomeView {
	return templates.WelcomeView{
		Email:          p.Email,
		Name:           p.Name,
		AskDisplayName: strings.TrimSpace(p.Name) == "",
		AskAccept:      h.Cfg.Compliance.RequireAcceptance && p.TermsAcceptedAt.IsZero(),
		TermsURL:       h.termsURL(),
		PrivacyURL:     h.privacyURL(),
		ReturnTo:       retTo,
		Error:          errMsg,
	}
}

func (h *WelcomeHandler) termsURL() string {
	return cmp.Or(strings.TrimSpace(h.Cfg.Compliance.TermsURL), web.Path(h.Cfg.HTTP.BasePath, "/terms"))
}

func (h *WelcomeHandler) privacyURL() string {
	return cmp.Or(strings.TrimSpace(h.Cfg.Compliance.PrivacyURL), web.Path(h.Cfg.HTTP.BasePath, "/privacy"))
}

// WelcomeGate redirects signed-in users to /welcome when compliance requires acceptance and their session's TermsAcceptedAt is zero.
func WelcomeGate(basePath string, requireAcceptance bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !requireAcceptance {
				next.ServeHTTP(w, r)
				return
			}
			if isWelcomeSkipped(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			p := session.PrincipalFrom(r.Context())
			if p.UserID == [16]byte{} {
				next.ServeHTTP(w, r)
				return
			}
			if !p.TermsAcceptedAt.IsZero() {
				next.ServeHTTP(w, r)
				return
			}
			target := web.Path(basePath, "/welcome") + "?return_to=" + r.URL.RequestURI()
			http.Redirect(w, r, target, http.StatusSeeOther) //nolint:gosec // G710: target is derived from configured basePath + literal '/welcome'
		})
	}
}

func isWelcomeSkipped(path string) bool {
	switch {
	case path == "/welcome",
		path == "/login",
		path == "/logout",
		path == "/onboard",
		path == "/signup/complete",
		path == "/terms",
		path == "/privacy",
		strings.HasPrefix(path, "/onboard/"),
		strings.HasPrefix(path, "/oauth/"),
		strings.HasPrefix(path, "/static/"),
		path == "/healthz",
		path == "/robots.txt":
		return true
	}
	return false
}
