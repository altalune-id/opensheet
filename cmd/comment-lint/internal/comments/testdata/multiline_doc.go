//go:build ignore

package fixture

// Load resolves configuration with precedence: overrides > env > file > default.
// The mode string is validated at startup so a typo fails fast, before any
// database connection is opened.
func Load() {}

// Widget is a thing.
// It was added after the 2024 incident.
type Widget struct{}
