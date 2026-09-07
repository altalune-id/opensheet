// Package version exposes build metadata stamped at link time via -ldflags.
package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var embedded string

// Version, Commit, BuildTime are stamped at link time via `go build -ldflags "-X ...=..."`. NOTE: keep as bare string literals — function-init breaks -X.
var (
	Version   = ""
	Commit    = "unknown"
	BuildTime = "unknown"
)

// Default returns Version if stamped, else the checked-in `version/VERSION` file.
func Default() string {
	if Version != "" {
		return Version
	}
	return strings.TrimSpace(embedded)
}

type Info struct {
	Version   string
	Commit    string
	BuildTime string
}

func Get() Info {
	return Info{Version: Default(), Commit: Commit, BuildTime: BuildTime}
}

func String() string {
	return "altempl " + Default() + " (commit " + Commit + ", built " + BuildTime + ")"
}
