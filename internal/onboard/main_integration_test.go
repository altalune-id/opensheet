//go:build integration

package onboard_test

import (
	"testing"

	"altalune.id/opensheet/internal/testutil/pgtest"
)

func runTests(m *testing.M) {
	pgtest.Main(m)
}
