//go:build !integration

package onboard_test

import (
	"os"
	"testing"
)

func runTests(m *testing.M) {
	os.Exit(m.Run())
}
