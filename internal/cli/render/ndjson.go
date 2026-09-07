package render

import (
	"encoding/json"
	"io"
	"iter"
)

// NDJSON writes one compact JSON object per line from seq.
func NDJSON(w io.Writer, seq iter.Seq[any]) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for v := range seq {
		if err := enc.Encode(v); err != nil {
			return err
		}
	}
	return nil
}
