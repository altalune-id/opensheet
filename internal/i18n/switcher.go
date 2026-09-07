package i18n

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// CookieMaxAge is the lifetime applied to alt_locale cookies (one year).
const CookieMaxAge = 31536000

// LocalePersister persists a signed-in user's locale choice.
type LocalePersister func(ctx context.Context, tag string) error

// SwitcherOpts configures Switcher.
type SwitcherOpts struct {
	Bundle       *Bundle
	CookieSecure bool
	CookiePath   string
	Persist      LocalePersister
	Fallback     string
}

// Switcher returns an http.Handler that stores the chosen locale in a cookie and redirects.
func Switcher(opts SwitcherOpts) http.HandlerFunc {
	if opts.Bundle == nil {
		panic("i18n.Switcher: nil Bundle")
	}
	fallback := opts.Fallback
	if fallback == "" {
		fallback = "/"
	}
	cookiePath := opts.CookiePath
	if cookiePath == "" {
		cookiePath = "/"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		tag := strings.TrimSpace(r.PostFormValue("locale"))
		loc, err := opts.Bundle.Parse(tag)
		if err != nil {
			http.Error(w, "invalid locale", http.StatusBadRequest)
			return
		}
		http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: SameSite=Lax set below
			Name:     CookieName,
			Value:    string(loc),
			Path:     cookiePath,
			MaxAge:   CookieMaxAge,
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
			Secure:   opts.CookieSecure,
		})
		if opts.Persist != nil {
			_ = opts.Persist(r.Context(), string(loc))
		}
		target := SanitizeRedirect(r.PostFormValue("redirect"))
		if target == "" {
			target = fallback
		}
		http.Redirect(w, r, target, http.StatusSeeOther) //nolint:gosec // G710: SanitizeRedirect rejects any target that is not a same-origin absolute path
	}
}

// SanitizeRedirect rejects any redirect that leaves the same-origin.
func SanitizeRedirect(raw string) string {
	normalized := strings.ReplaceAll(raw, "\\", "/")
	u, err := url.Parse(normalized)
	if err != nil {
		return ""
	}
	if u.Scheme != "" || u.Host != "" || u.Opaque != "" {
		return ""
	}
	if !strings.HasPrefix(u.Path, "/") {
		return ""
	}
	if strings.HasPrefix(u.Path, "//") {
		return ""
	}
	if strings.ContainsRune(u.Path, ':') {
		return ""
	}
	out := u.EscapedPath()
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		out += "#" + u.EscapedFragment()
	}
	return out
}
