//go:build ignore

// Package fixture is compliant with the comment discipline.
package fixture

import "fmt"

// Widget is a thing.
type Widget struct{ Name string }

// DefaultName is the fallback.
const DefaultName = "widget"

// Render prints the widget.
func (w Widget) Render() { fmt.Println(w.Name) }

// TODO: drop once callers migrate.
func legacy() {}

// SECURITY: reports absent, not forbidden, so callers learn nothing about other rows.
func check() {}

// NOTE: mirrors https://example.com/spec#section-4 exactly.
func mirror() {}

// Wrapped keeps one sentence.
//
//nolint:gocyclo // linear flow reads more clearly as one function.
func Wrapped() {}

//go:generate echo hi
func generated() {}
