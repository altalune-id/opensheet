//go:build ignore

package fixture

func Build() {
	// The store builds jet expressions lazily so the caller can compose them.
	// Composing them eagerly would allocate on every request, which showed up
	// in the profile as the hottest path in the whole handler.
	_ = 1
}
