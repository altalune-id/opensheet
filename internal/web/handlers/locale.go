package handlers

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"altalune.id/template/internal/i18n"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/user"
)

// LocaleHandler serves POST /locale, persisting the locale choice for signed-in users.
type LocaleHandler struct {
	Deps
	Users *user.Service
}

// NewLocaleHandler wires the handler.
func NewLocaleHandler(d Deps, users *user.Service) *LocaleHandler {
	return &LocaleHandler{Deps: d, Users: users}
}

// Register mounts POST /locale on mux.
func (h *LocaleHandler) Register(mux *http.ServeMux) {
	if h.I18n == nil {
		return
	}
	mux.HandleFunc("POST /locale", i18n.Switcher(i18n.SwitcherOpts{
		Bundle:       h.I18n,
		CookieSecure: h.Cfg.HTTP.CookieSecure,
		CookiePath:   h.cookiePath(),
		Persist:      h.persister(),
		Fallback:     h.Cfg.HTTP.BasePath + "/",
	}))
}

func (h *LocaleHandler) cookiePath() string {
	if h.Cfg.HTTP.BasePath == "" {
		return "/"
	}
	return h.Cfg.HTTP.BasePath + "/"
}

func (h *LocaleHandler) persister() i18n.LocalePersister {
	if h.Users == nil {
		return nil
	}
	return func(ctx context.Context, tag string) error {
		p := session.PrincipalFrom(ctx)
		if p.UserID == uuid.Nil {
			return nil
		}
		return h.Users.UpdateLocale(ctx, p.UserID, tag)
	}
}
