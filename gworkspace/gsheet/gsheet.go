// Package gsheet reads Google Sheets through a caller-supplied token source and reports typed failures.
package gsheet

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/oauth2"
	sheetsapi "google.golang.org/api/sheets/v4"

	"altalune.id/opensheet/gworkspace"
)

// ScopeReadOnly is the OAuth2 scope a credential needs for every method on Client.
const ScopeReadOnly = sheetsapi.SpreadsheetsReadonlyScope

const metaFields = "properties.title,sheets.properties.title"

// Row is one spreadsheet row keyed by its normalized column header.
type Row map[string]string

// Table is a tab's raw header row and its data rows, in sheet order.
type Table struct {
	Headers []string
	Rows    [][]string
}

// Factory builds a Client for one credential.
type Factory func(ctx context.Context, ts oauth2.TokenSource) (*Client, error)

// Client reads spreadsheets on behalf of one credential.
type Client struct {
	svc *sheetsapi.Service
}

// New builds a Client whose every request carries a token from ts.
func New(ctx context.Context, ts oauth2.TokenSource, opts ...gworkspace.Option) (*Client, error) {
	apiOpts, err := gworkspace.ClientOptions(ts, ScopeReadOnly, opts...)
	if err != nil {
		return nil, err
	}
	svc, err := sheetsapi.NewService(ctx, apiOpts...)
	if err != nil {
		return nil, fmt.Errorf("gsheet: build service: %w", err)
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

// Table reads tab as its raw header row plus its data rows, each in sheet order.
func (c *Client) Table(ctx context.Context, fileID, tab string) (Table, error) {
	tab = strings.TrimSpace(tab)
	if tab == "" {
		return Table{}, &TabNotFoundError{Tab: tab}
	}
	resp, err := c.svc.Spreadsheets.Values.Get(fileID, quoteRange(tab)).Context(ctx).Do()
	if err != nil {
		return Table{}, translateRange(err, fileID, tab)
	}
	if len(resp.Values) == 0 {
		return Table{}, nil
	}

	out := Table{
		Headers: make([]string, len(resp.Values[0])),
		Rows:    make([][]string, 0, len(resp.Values)-1),
	}
	for i, cell := range resp.Values[0] {
		out.Headers[i] = cellString(cell)
	}
	for _, cells := range resp.Values[1:] {
		row := make([]string, len(cells))
		for i, cell := range cells {
			row[i] = cellString(cell)
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// Rows returns a tab's data rows keyed by its header row, plus one warning per renamed header.
func (c *Client) Rows(ctx context.Context, fileID, tab string) ([]Row, []string, error) {
	tbl, err := c.Table(ctx, fileID, tab)
	if err != nil {
		return nil, nil, err
	}
	if tbl.Headers == nil {
		return nil, nil, nil
	}

	names, warnings := normalizeHeaders(tbl.Headers)
	rows := make([]Row, 0, len(tbl.Rows))
	for _, cells := range tbl.Rows {
		row := make(Row, len(names))
		for i, cell := range cells {
			if i >= len(names) {
				break
			}
			row[names[i]] = cell
		}
		rows = append(rows, row)
	}
	return rows, warnings, nil
}

func (c *Client) meta(ctx context.Context, fileID string) (*sheetsapi.Spreadsheet, error) {
	resp, err := c.svc.Spreadsheets.Get(fileID).Fields(metaFields).Context(ctx).Do()
	if err != nil {
		return nil, gworkspace.Translate(err, fileID)
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
