// Package surfaces names the domain verbs each machine plane exposes, so R1 of ../../../docs/surfaces/README.md can be tested.
package surfaces

// Verb identifies one domain operation on an aggregate, independent of its wire spelling.
type Verb struct {
	Module    string
	Aggregate string
	Operation string
}
