// Package gworkspace builds the shared transport Google Workspace service clients ride on and translates provider failures into typed errors.
package gworkspace

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/option"

	"altalune.id/opensheet/httpclient"
)

// Defaults applied to every Google Workspace client.
const (
	DefaultTimeout           = 30 * time.Second
	DefaultResponseBodyLimit = 64 << 20
)

// Option configures the transport a Google Workspace service client is built on.
type Option func(*settings)

type settings struct {
	baseURL string
	timeout time.Duration
}

// WithBaseURL overrides the Google API endpoint.
// SECURITY: WithBaseURL relaxes the private-host filter, so it must never be wired from config or any request-derived value; it exists for tests and for a code-supplied endpoint.
func WithBaseURL(u string) Option {
	return func(s *settings) { s.baseURL = strings.TrimSpace(u) }
}

// WithTimeout bounds a single Google call. Zero or negative keeps DefaultTimeout.
func WithTimeout(d time.Duration) Option {
	return func(s *settings) {
		if d > 0 {
			s.timeout = d
		}
	}
}

// ClientOptions returns the google.golang.org/api options a service client for scope is built from.
func ClientOptions(ts oauth2.TokenSource, scope string, opts ...Option) ([]option.ClientOption, error) {
	if ts == nil {
		return nil, errors.New("gworkspace: token source: is nil")
	}
	s := settings{timeout: DefaultTimeout}
	for _, opt := range opts {
		opt(&s)
	}

	out := []option.ClientOption{option.WithHTTPClient(s.httpClient(ts)), option.WithScopes(scope)}
	if endpoint := s.endpoint(); endpoint != "" {
		out = append(out, option.WithEndpoint(endpoint))
	}
	return out, nil
}

func (s settings) httpClient(ts oauth2.TokenSource) *http.Client {
	base := httpclient.New(
		httpclient.WithTimeout(s.timeout),
		httpclient.WithResponseBodyLimit(DefaultResponseBodyLimit),
		httpclient.WithOtel(true),
		httpclient.WithAllowPrivateHosts(s.baseURL != ""),
	)
	// NOTE: option.WithTokenSource is rejected alongside option.WithHTTPClient, so the token source rides the transport.
	return &http.Client{
		Timeout:   base.Timeout,
		Transport: &oauth2.Transport{Source: ts, Base: base.Transport},
	}
}

func (s settings) endpoint() string {
	if s.baseURL == "" {
		return ""
	}
	return strings.TrimSuffix(s.baseURL, "/") + "/"
}
