//go:build integration

package spreadsheet_test

import (
	"testing"

	"altalune.id/opensheet/internal/testutil/pgtest"
)

func TestMain(m *testing.M) {
	pgtest.Main(m)
}
