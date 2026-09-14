package gworkspace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// NOTE: drive.file is non-sensitive, so no verification review and no 7-day refresh-token expiry; email is the only way a consumer learns the account.
// https://developers.google.com/workspace/sheets/api/scopes
const (
	scopeDriveFile = "https://www.googleapis.com/auth/drive.file"
	scopeEmail     = "email"
)

const exchangeTimeout = 20 * time.Second

// Scopes returns the default scope set the connect flow requests.
func Scopes() []string { return []string{scopeDriveFile, scopeEmail} }

// Connector runs the Google OAuth authorization-code flow for one OAuth client.
type Connector struct {
	cfg     *oauth2.Config
	scopes  []string
	timeout time.Duration
}

// NewConnector binds the flow to an OAuth client; an empty scopes keeps Scopes().
// NOTE: only WithTimeout applies here; WithBaseURL is a transport option and is ignored — the token
// endpoint comes from cfg.Endpoint.
func NewConnector(cfg *oauth2.Config, scopes []string, opts ...Option) (*Connector, error) {
	if cfg == nil {
		return nil, errors.New("gworkspace: oauth config: is nil")
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("gworkspace: oauth config: no client id or secret")
	}
	s := settings{timeout: exchangeTimeout}
	for _, opt := range opts {
		opt(&s)
	}
	if len(scopes) == 0 {
		scopes = Scopes()
	}
	return &Connector{cfg: cfg, scopes: slices.Clone(scopes), timeout: s.timeout}, nil
}

// AuthCodeURL returns the Google consent URL carrying state verbatim.
func (c *Connector) AuthCodeURL(state string) string {
	// SECURITY: the scope set is pinned via SetAuthURLParam rather than read off the injected config, because AuthCodeURL applies options after c.Scopes, so a mis-wired client cannot widen it.
	// NOTE: prompt=consent is what makes access_type=offline return a refresh token on every reconnect, not only the first.
	return c.cfg.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("scope", strings.Join(c.scopes, " ")),
		oauth2.SetAuthURLParam("prompt", "consent"),
	)
}

// Grant is what Google returned for one authorization code.
type Grant struct {
	RefreshToken string
	AccessToken  string
	Expiry       time.Time
	// AccountEmail is the id_token email claim; it is empty when Google sent none.
	AccountEmail string
	// GrantedScopes is what the user actually consented to, not what was requested.
	GrantedScopes []string
}

// Exchange trades an authorization code for a Grant.
func (c *Connector) Exchange(ctx context.Context, code string) (*Grant, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	tok, err := c.cfg.Exchange(ctx, code)
	if err != nil {
		return nil, &UnavailableError{Cause: err}
	}
	return &Grant{
		RefreshToken:  strings.TrimSpace(tok.RefreshToken),
		AccessToken:   tok.AccessToken,
		Expiry:        tok.Expiry,
		AccountEmail:  accountEmail(tok),
		GrantedScopes: grantedScopes(tok),
	}, nil
}

func grantedScopes(tok *oauth2.Token) []string {
	raw, ok := tok.Extra("scope").(string)
	if !ok {
		return nil
	}
	return strings.Fields(raw)
}

// SECURITY: the id_token arrives inside the token-endpoint response over TLS, so the channel authenticates it;
// only the email claim is read and it is display-only, never an authorization input.
func accountEmail(tok *oauth2.Token) string {
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return ""
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return strings.TrimSpace(claims.Email)
}

// ProjectNumber returns the Cloud project number a Google OAuth client ID was issued under, or ""
// when clientID does not carry one. The Picker needs it as the Drive App ID for the drive.file scope.
// https://developers.google.com/workspace/drive/picker/reference/picker.pickerbuilder.setappid
func ProjectNumber(clientID string) string {
	num, _, ok := strings.Cut(strings.TrimSpace(clientID), "-")
	if !ok || num == "" {
		return ""
	}
	for _, r := range num {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return num
}
