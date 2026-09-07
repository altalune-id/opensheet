package render

import (
	"encoding/json"
	"fmt"
	"io"

	"altalune.id/template/internal/apperror"
)

// Error writes appErr to w: multi-line human under text; single-line "error" envelope under json/ndjson.
func Error(w io.Writer, format Format, appErr *apperror.AppError, exit int) error {
	if appErr == nil {
		return nil
	}
	switch format {
	case FormatJSON, FormatNDJSON:
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		return enc.Encode(map[string]any{
			"error": map[string]any{
				"code":    appErr.Code(),
				"message": appErr.Message(),
				"exit":    exit,
			},
		})
	default:
		if _, err := fmt.Fprintf(w, "error: %s\n", appErr.Message()); err != nil {
			return err
		}
		if code := appErr.Code(); code != "" {
			if _, err := fmt.Fprintf(w, "  code: %s\n", code); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "  exit: %d\n", exit); err != nil {
			return err
		}
		return nil
	}
}
