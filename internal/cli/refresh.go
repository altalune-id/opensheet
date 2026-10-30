package cli

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/altalune-id/oidcrp"
)

const (
	refreshSkew    = time.Minute
	refreshTimeout = 30 * time.Second
)

type tokenSource struct {
	store      profileStore
	name       string
	httpClient *http.Client
	now        func() time.Time
}

// SECURITY: the refresh runs under the session file lock and re-reads the profile, so two processes never spend one refresh token.
func (s tokenSource) accessToken(ctx context.Context, url string) (string, error) {
	p, err := s.store.load(s.name)
	if err != nil {
		return "", err
	}
	if err := s.usable(p, url); err != nil {
		return "", err
	}
	if s.fresh(*p) {
		return p.Token.Access, nil
	}
	var access string
	err = s.store.edit(ctx, func(profiles map[string]profile) (bool, error) {
		cur, ok := profiles[s.name]
		if !ok {
			return false, &NotSignedInError{Profile: s.name, URL: url}
		}
		if err := s.usable(&cur, url); err != nil {
			return false, err
		}
		if s.fresh(cur) {
			access = cur.Token.Access
			return false, nil
		}
		next, changed, err := s.refresh(ctx, cur)
		profiles[s.name] = next
		if err != nil {
			return changed, err
		}
		access = next.Token.Access
		return true, nil
	})
	return access, err
}

func (s tokenSource) usable(p *profile, url string) error {
	switch {
	case p == nil || p.URL == "":
		return &NotSignedInError{Profile: s.name, URL: url}
	case !sameURL(p.URL, url):
		return &ProfileHostMismatchError{Profile: s.name, ProfileURL: p.URL, URL: url}
	case p.Token == nil:
		return &SessionExpiredError{Profile: s.name, URL: p.URL}
	}
	return nil
}

// NOTE: with no expires_in, a refresh token makes every call refresh, and without one the token is used until the server refuses it.
func (s tokenSource) fresh(p profile) bool {
	if p.ExpiresAt.IsZero() {
		return p.Token.Refresh == ""
	}
	return s.now().Before(p.ExpiresAt.Add(-refreshSkew))
}

// SECURITY: invalid_grant drops the token and keeps the URL; any other failure keeps it, so an issuer outage signs no one out.
func (s tokenSource) refresh(ctx context.Context, p profile) (profile, bool, error) {
	t := *p.Token
	expired := func(cause error) (profile, bool, error) {
		p.Token, p.ExpiresAt = nil, time.Time{}
		return p, true, &SessionExpiredError{Profile: s.name, URL: p.URL, Err: cause}
	}
	if t.Refresh == "" {
		return expired(nil)
	}
	rp := oidcrp.New(oidcrp.Config{Issuer: t.Issuer, ClientID: t.ClientID, Resource: t.Resource, HTTPClient: s.httpClient})
	// SECURITY: the issuer spends the old refresh token on receipt, so an interrupt must not drop the rotated one before it is stored.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
	defer cancel()
	set, err := rp.Refresh(ctx, oidcrp.TokenSet{AccessToken: t.Access, RefreshToken: t.Refresh, Scopes: t.Scopes})
	switch {
	case err == nil:
		t.Access, t.Refresh, t.Scopes = set.AccessToken, set.RefreshToken, set.Scopes
		p.Token, p.ExpiresAt = &t, set.ExpiresAt
		return p, true, nil
	case set != nil:
		t.Refresh = set.RefreshToken
		p.Token = &t
		return p, true, fmt.Errorf("refresh the session of profile %s: %w", s.name, err)
	case oidcrp.OAuthErrorOf(err) == "invalid_grant":
		return expired(err)
	}
	return p, false, fmt.Errorf("refresh the session of profile %s: %w", s.name, err)
}
