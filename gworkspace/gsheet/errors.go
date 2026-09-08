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
