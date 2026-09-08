package gsheet

import (
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/api/googleapi"

	"altalune.id/opensheet/gworkspace"
)

// TabNotFoundError reports that the spreadsheet has no tab named Tab.
type TabNotFoundError struct{ Tab string }

func (e *TabNotFoundError) Error() string {
	return fmt.Sprintf("gsheet: tab %q: not found", e.Tab)
}

// IsTabNotFoundError reports whether err's chain contains a *TabNotFoundError.
func IsTabNotFoundError(err error) bool {
	_, ok := errors.AsType[*TabNotFoundError](err)
	return ok
}

func translateRange(err error, fileID, tab string) error {
	var g *googleapi.Error
	if errors.As(err, &g) && g.Code == http.StatusBadRequest {
		return &TabNotFoundError{Tab: tab}
	}
	return gworkspace.Translate(err, fileID)
}

// InvalidTabTitleError reports that Title is blank or longer than Google's 100-character tab-title limit.
type InvalidTabTitleError struct{ Title string }

func (e *InvalidTabTitleError) Error() string {
	return fmt.Sprintf("gsheet: tab title %q: must be 1 to %d characters", e.Title, maxTabTitleRunes)
}

// IsInvalidTabTitleError reports whether err's chain contains a *InvalidTabTitleError.
func IsInvalidTabTitleError(err error) bool {
	_, ok := errors.AsType[*InvalidTabTitleError](err)
	return ok
}

// InvalidRowIndexError reports that Row is not a 1-based spreadsheet row number.
type InvalidRowIndexError struct{ Row int }

func (e *InvalidRowIndexError) Error() string {
	return fmt.Sprintf("gsheet: row %d: must be a 1-based row number", e.Row)
}

// IsInvalidRowIndexError reports whether err's chain contains a *InvalidRowIndexError.
func IsInvalidRowIndexError(err error) bool {
	_, ok := errors.AsType[*InvalidRowIndexError](err)
	return ok
}
