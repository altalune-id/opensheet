package render

import (
	"encoding/json"
	"io"
)

// JSON writes v under a single top-level "data" envelope, indented for humans.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(map[string]any{"data": v})
}
