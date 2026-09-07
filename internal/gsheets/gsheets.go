// Package gsheets reads Google Sheets through a caller-supplied token source and reports typed failures.
package gsheets

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	sheetsapi "google.golang.org/api/sheets/v4"

	"altalune.id/opensheet/httpclient"
)

// Defaults applied by New.
const (
	DefaultTimeout           = 30 * time.Second
	DefaultResponseBodyLimit = 64 << 20
)

// ScopeReadOnly is the OAuth2 scope a credential needs for every method on Client.
const ScopeReadOnly = sheetsapi.SpreadsheetsReadonlyScope

const metaFields = "properties.title,sheets.properties.title"

// Row is one spreadsheet row keyed by its normalized column header.
type Row map[string]string

// TokenSource supplies bearer tokens for the Sheets API; satisfied by oauth2.TokenSource.
type TokenSource interface {
	Token() (*oauth2.Token, error)
}

// Factory builds a Client for one credential.
type Factory func(ctx context.Context, ts TokenSource) (*Client, error)

// Client reads spreadsheets on behalf of one credential.
type Client struct {
	svc *sheetsapi.Service
}

// Option configures New.
type Option func(*settings)

type settings struct {
	baseURL string
	timeout time.Duration
}

// WithBaseURL overrides the Sheets API endpoint.
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

// New builds a Client whose every request carries a token from ts.
func New(ctx context.Context, ts TokenSource, opts ...Option) (*Client, error) {
	if ts == nil {
		return nil, fmt.Errorf("gsheets: token source: is nil")
	}
	s := settings{timeout: DefaultTimeout}
	for _, opt := range opts {
		opt(&s)
	}

	// SECURITY: WithBaseURL relaxes the private-host filter, so it must never be wired from config or any request-derived value; it exists for tests and for a code-supplied endpoint.
	base := httpclient.New(
		httpclient.WithTimeout(s.timeout),
		httpclient.WithResponseBodyLimit(DefaultResponseBodyLimit),
		httpclient.WithOtel(true),
		httpclient.WithAllowPrivateHosts(s.baseURL != ""),
	)
	// NOTE: option.WithTokenSource is rejected alongside option.WithHTTPClient, so the token source rides the transport.
	hc := &http.Client{
		Timeout:   base.Timeout,
		Transport: &oauth2.Transport{Source: oauth2.TokenSource(ts), Base: base.Transport},
	}

	apiOpts := []option.ClientOption{option.WithHTTPClient(hc), option.WithScopes(ScopeReadOnly)}
	if s.baseURL != "" {
		apiOpts = append(apiOpts, option.WithEndpoint(strings.TrimSuffix(s.baseURL, "/")+"/"))
	}
	svc, err := sheetsapi.NewService(ctx, apiOpts...)
	if err != nil {
		return nil, fmt.Errorf("gsheets: build service: %w", err)
	}
	return &Client{svc: svc}, nil
}

// Tabs lists a spreadsheet's tab titles in sheet order.
func (c *Client) Tabs(ctx context.Context, fileID string) ([]string, error) {
	meta, err := c.meta(ctx, fileID)
	if err != nil {
		return nil, err
	}
	return tabTitles(meta), nil
}

// Title returns a spreadsheet's document title.
func (c *Client) Title(ctx context.Context, fileID string) (string, error) {
	meta, err := c.meta(ctx, fileID)
	if err != nil {
		return "", err
	}
	if meta.Properties == nil {
		return "", nil
	}
	return meta.Properties.Title, nil
}

// FirstTab returns the title of a spreadsheet's leftmost tab.
func (c *Client) FirstTab(ctx context.Context, fileID string) (string, error) {
	meta, err := c.meta(ctx, fileID)
	if err != nil {
		return "", err
	}
	titles := tabTitles(meta)
	if len(titles) == 0 {
		return "", &TabNotFoundError{}
	}
	return titles[0], nil
}

// Rows returns a tab's data rows keyed by its header row, plus one warning per renamed header.
func (c *Client) Rows(ctx context.Context, fileID, tab string) ([]Row, []string, error) {
	tab = strings.TrimSpace(tab)
	if tab == "" {
		return nil, nil, &TabNotFoundError{Tab: tab}
	}
	resp, err := c.svc.Spreadsheets.Values.Get(fileID, quoteRange(tab)).Context(ctx).Do()
	if err != nil {
		return nil, nil, translateRange(err, fileID, tab)
	}
	if len(resp.Values) == 0 {
		return nil, nil, nil
	}

	raw := make([]string, len(resp.Values[0]))
	for i, cell := range resp.Values[0] {
		raw[i] = cellString(cell)
	}
	names, warnings := normalizeHeaders(raw)

	rows := make([]Row, 0, len(resp.Values)-1)
	for _, cells := range resp.Values[1:] {
		row := make(Row, len(names))
		for i, cell := range cells {
			if i >= len(names) {
				break
			}
			row[names[i]] = cellString(cell)
		}
		rows = append(rows, row)
	}
	return rows, warnings, nil
}

func (c *Client) meta(ctx context.Context, fileID string) (*sheetsapi.Spreadsheet, error) {
	resp, err := c.svc.Spreadsheets.Get(fileID).Fields(metaFields).Context(ctx).Do()
	if err != nil {
		return nil, translate(err, fileID)
	}
	return resp, nil
}

func tabTitles(meta *sheetsapi.Spreadsheet) []string {
	out := make([]string, 0, len(meta.Sheets))
	for _, s := range meta.Sheets {
		if s.Properties == nil {
			continue
		}
		out = append(out, s.Properties.Title)
	}
	return out
}

func normalizeHeaders(raw []string) (names, warnings []string) {
	names = make([]string, len(raw))
	seen := map[string]int{}
	for i, h := range raw {
		name := strings.TrimSpace(h)
		if name == "" {
			name = "col_" + strconv.Itoa(i+1)
			warnings = append(warnings, fmt.Sprintf("column %d has a blank header; exposed as %q", i+1, name))
		}
		seen[name]++
		if n := seen[name]; n > 1 {
			deduped := name + "_" + strconv.Itoa(n)
			warnings = append(warnings, fmt.Sprintf("duplicate header %q in column %d; exposed as %q", name, i+1, deduped))
			name = deduped
		}
		names[i] = name
	}
	return names, warnings
}

// SECURITY: single-quoting and doubling embedded quotes stops a tab title from breaking out of the A1 range expression.
func quoteRange(title string) string {
	return "'" + strings.ReplaceAll(title, "'", "''") + "'"
}

func cellString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(t)
	}
}
