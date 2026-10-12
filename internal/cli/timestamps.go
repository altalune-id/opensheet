package cli

import "time"

func rfc3339UTC(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func tableDay(t time.Time) string { return t.UTC().Format(time.DateOnly) }
