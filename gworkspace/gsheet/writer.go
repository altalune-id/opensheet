package gsheet

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/oauth2"
	sheetsapi "google.golang.org/api/sheets/v4"

	"altalune.id/opensheet/gworkspace"
)

// ScopeReadWrite is the OAuth2 scope a credential needs for every method on Writer.
const ScopeReadWrite = sheetsapi.SpreadsheetsScope

// SECURITY: every write is RAW, with no override — under USER_ENTERED a caller could write =Payroll!A1 and read
// the evaluated value back through the read path, or =IMPORTXML to exfiltrate through Google's servers.
const valueInputRaw = "RAW"

const insertDataRows = "INSERT_ROWS"

// MaxTabTitleRunes is Google's limit on the length of a tab title.
// https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets/sheets#SheetProperties
const MaxTabTitleRunes = 100

// WriterFactory builds a Writer for one credential.
type WriterFactory func(ctx context.Context, ts oauth2.TokenSource) (*Writer, error)

// Writer mutates spreadsheets on behalf of one credential.
// NOTE: the embedded *Client is method-set separation — a plain *Client carries no write methods — not proof of the token's scope.
type Writer struct {
	*Client
}

// NewWriter builds a Writer whose every request carries a token from ts.
func NewWriter(ctx context.Context, ts oauth2.TokenSource, opts ...gworkspace.Option) (*Writer, error) {
	svc, err := newService(ctx, ts, ScopeReadWrite, opts...)
	if err != nil {
		return nil, err
	}
	return &Writer{Client: &Client{svc: svc}}, nil
}

// Append adds cells as one row at the end of tab and reports how many rows Google wrote.
func (w *Writer) Append(ctx context.Context, fileID, tab string, cells []any) (int, error) {
	tab = strings.TrimSpace(tab)
	if tab == "" {
		return 0, &TabNotFoundError{Tab: tab}
	}

	resp, err := w.svc.Spreadsheets.Values.
		Append(fileID, quoteRange(tab), &sheetsapi.ValueRange{Values: [][]any{cells}}).
		ValueInputOption(valueInputRaw).
		InsertDataOption(insertDataRows).
		Context(ctx).Do()
	if err != nil {
		return 0, translateRange(err, fileID, tab)
	}
	if resp.Updates == nil {
		return 0, nil
	}
	return int(resp.Updates.UpdatedRows), nil
}

// UpdateRow overwrites the cells of one 1-based row of tab.
func (w *Writer) UpdateRow(ctx context.Context, fileID, tab string, rowIndex int, cells []any) error {
	tab = strings.TrimSpace(tab)
	if tab == "" {
		return &TabNotFoundError{Tab: tab}
	}
	if rowIndex < 1 {
		return &InvalidRowIndexError{Row: rowIndex}
	}

	// NOTE: the end column makes the write's extent part of the request, so Google rejects a mis-sized cells slice.
	rng := fmt.Sprintf("%s!A%d:%s%d", quoteRange(tab), rowIndex, columnLetter(len(cells)), rowIndex)
	_, err := w.svc.Spreadsheets.Values.
		Update(fileID, rng, &sheetsapi.ValueRange{Values: [][]any{cells}}).
		ValueInputOption(valueInputRaw).
		Context(ctx).Do()
	if err != nil {
		return translateRange(err, fileID, tab)
	}
	return nil
}

// AddTab creates a tab titled title, trimmed of surrounding space.
func (w *Writer) AddTab(ctx context.Context, fileID, title string) error {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > MaxTabTitleRunes {
		return &InvalidTabTitleError{Title: title}
	}

	req := &sheetsapi.BatchUpdateSpreadsheetRequest{
		Requests: []*sheetsapi.Request{{
			AddSheet: &sheetsapi.AddSheetRequest{
				Properties: &sheetsapi.SheetProperties{Title: title},
			},
		}},
	}
	if _, err := w.svc.Spreadsheets.BatchUpdate(fileID, req).Context(ctx).Do(); err != nil {
		return gworkspace.Translate(err, fileID)
	}
	return nil
}
