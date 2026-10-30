package handlers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/altalune-id/oidcrp"

	"altalune.id/opensheet/internal/user"
	"altalune.id/opensheet/internal/web"
)

const (
	oidcStateCookie = "opensheet_oidc_state"
	oidcStateTTL    = 10 * time.Minute
	lastUserCookie  = "opensheet_last_user"
	lastUserTTL     = 90 * 24 * time.Hour
)

const (
	signInExpired          = "errors.signin.expired"
	signInUnavailable      = "errors.signin.unavailable"
	signInEmailUnverified  = "errors.signin.email_unverified"
	signInIdentityConflict = "errors.signin.identity_conflict"
	signInNotPermitted     = "errors.signin.not_permitted"
)

type oidcState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	ReturnTo string `json:"r,omitempty"`
	Expires  int64  `json:"e"`
}

// GetOIDCStart sends the browser to the issuer with a fresh state, nonce and S256 verifier kept in a signed cookie. SECURITY: RFC 9700 Section 2.1.1.
func (h *AuthHandler) GetOIDCStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	var prompt, hint string
	if r.URL.Query().Get("switch") != "1" {
		hint = h.lastUser(r)
	}
	// SECURITY: OIDC Core Section 3.1.2.1: with no account in mind, the picker shows instead of a silent sign-in to whichever session is live.
	if hint == "" {
		prompt = "select_account"
	}
	if len(h.stateSecret) == 0 {
		h.LogErr("web auth: oidc start", errors.New("no state secret"))
		h.signInFailed(w, r, http.StatusInternalServerError, signInUnavailable)
		return
	}
	req, err := oidcrp.NewAuthRequest(prompt, hint)
	if err != nil {
		h.LogErr("web auth: oidc start", err)
		h.signInFailed(w, r, http.StatusInternalServerError, signInUnavailable)
		return
	}
	target, err := h.AltAuth.AuthCodeURL(r.Context(), req)
	if err != nil {
		h.LogErr("web auth: oidc start", err)
		h.signInFailed(w, r, http.StatusBadGateway, signInUnavailable)
		return
	}
	raw, err := json.Marshal(oidcState{
		State: req.State, Nonce: req.Nonce, Verifier: req.CodeVerifier,
		ReturnTo: SanitizeReturnTo(r.URL.Query().Get("return_to")), Expires: time.Now().Add(oidcStateTTL).Unix(),
	})
	if err != nil {
		h.LogErr("web auth: oidc start", err)
		h.signInFailed(w, r, http.StatusInternalServerError, signInUnavailable)
		return
	}
	web.SetCookie(w, web.CookieOpts{
		Name: oidcStateCookie, Value: web.SignCookie(h.stateSecret, base64.RawURLEncoding.EncodeToString(raw)),
		BasePath: h.Cfg.HTTP.BasePath, CookieSecure: h.cookieSecure(r), MaxAge: int(oidcStateTTL.Seconds()),
	})
	http.Redirect(w, r, target, http.StatusFound) //nolint:gosec // G710: the authorization endpoint of the issuer's own discovery.
}

// GetOIDCCallback checks the authorization response against the state cookie, redeems the code and signs the user in. SECURITY: the callback URL carries the code, so it sends no Referer.
func (h *AuthHandler) GetOIDCCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	st, ok := h.readOIDCState(r)
	web.ClearCookie(w, oidcStateCookie, h.Cfg.HTTP.BasePath, h.cookieSecure(r))
	if !ok {
		h.signInFailed(w, r, http.StatusBadRequest, signInExpired)
		return
	}
	req := oidcrp.AuthRequest{State: st.State, Nonce: st.Nonce, CodeVerifier: st.Verifier}
	tok, err := h.AltAuth.Exchange(r.Context(), oidcrp.ParseCallback(r.URL.Query()), req)
	if err != nil {
		h.LogErr("web auth: oidc callback", err)
		h.signInError(w, r, err)
		return
	}
	h.OIDCComplete(r.Context(), w, r, tok, st.ReturnTo)
}

func (h *AuthHandler) readOIDCState(r *http.Request) (oidcState, bool) {
	c, err := r.Cookie(oidcStateCookie)
	if err != nil {
		return oidcState{}, false
	}
	if len(h.stateSecret) == 0 {
		return oidcState{}, false
	}
	value, err := web.VerifyCookie(h.stateSecret, c.Value)
	if err != nil {
		return oidcState{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return oidcState{}, false
	}
	var st oidcState
	if json.Unmarshal(raw, &st) != nil || st.State == "" || time.Now().Unix() >= st.Expires {
		return oidcState{}, false
	}
	return st, true
}

// SECURITY: the page names a reason key, never upstream text; a forged, replayed or mixed-up callback is "start again".
func (h *AuthHandler) signInError(w http.ResponseWriter, r *http.Request, err error) {
	var rpErr *oidcrp.Error
	if !errors.As(err, &rpErr) {
		h.signInFailed(w, r, http.StatusBadGateway, signInUnavailable)
		return
	}
	switch rpErr.Reason {
	case oidcrp.ReasonCancelled:
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
	case oidcrp.ReasonState, oidcrp.ReasonIssuer, oidcrp.ReasonNonce, oidcrp.ReasonIDToken, oidcrp.ReasonError:
		h.signInFailed(w, r, http.StatusBadRequest, signInExpired)
	case oidcrp.ReasonTokenExchange:
		if rpErr.OAuthError == "invalid_grant" || rpErr.OAuthError == "invalid_request" {
			h.signInFailed(w, r, http.StatusBadRequest, signInExpired)
			return
		}
		h.signInFailed(w, r, http.StatusBadGateway, signInUnavailable)
	default:
		h.signInFailed(w, r, http.StatusBadGateway, signInUnavailable)
	}
}

func (h *AuthHandler) signInFailed(w http.ResponseWriter, r *http.Request, status int, key string, cause ...error) {
	title, body := "Sign-in failed", "Please try signing in again."
	if tr := h.Base(r, title).Translator; tr != nil {
		//i18n:use errors.signin.*
		title, body = tr.T(key+".title"), tr.T(key+".body")
	}
	h.ErrorPage(w, r, status, title, body, cause...)
}

func (h *AuthHandler) lastUser(r *http.Request) string {
	c, err := r.Cookie(lastUserCookie)
	if err != nil || len(h.stateSecret) == 0 {
		return ""
	}
	value, err := web.VerifyCookie(h.stateSecret, c.Value)
	if err != nil {
		return ""
	}
	email, err := user.NormalizeEmail(value)
	if err != nil {
		return ""
	}
	return email
}

func (h *AuthHandler) setLastUser(w http.ResponseWriter, r *http.Request, email string) {
	email, err := user.NormalizeEmail(email)
	if err != nil || len(h.stateSecret) == 0 {
		return
	}
	web.SetCookie(w, web.CookieOpts{
		Name: lastUserCookie, Value: web.SignCookie(h.stateSecret, email), BasePath: h.Cfg.HTTP.BasePath, CookieSecure: h.cookieSecure(r), MaxAge: int(lastUserTTL.Seconds()),
	})
}

func (h *AuthHandler) cookieSecure(r *http.Request) bool {
	return h.Cfg.HTTP.CookieSecure || r.TLS != nil
}
