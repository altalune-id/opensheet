//go:build ignore

package fixture

// helper does the work and exists because the caller needed it.
func helper() {}

// tally counts things.
var tally int

// knob tunes the loop.
const knob = 3

// bucket holds rows.
type bucket struct{}
