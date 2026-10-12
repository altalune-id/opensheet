package org

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func wibInstant() time.Time {
	return time.Date(2026, 10, 1, 23, 30, 0, 0, time.FixedZone("WIB", 7*3600))
}

func requireUTC(t *testing.T, got time.Time) {
	t.Helper()
	require.Same(t, time.UTC, got.Location())
	require.Equal(t, time.Date(2026, 10, 1, 16, 30, 0, 0, time.UTC), got)
}

func TestPgRows_ReadInstantsInUTC(t *testing.T) {
	at := wibInstant()
	requireUTC(t, (&pgOrgRow{CreatedAt: at}).toOrg().CreatedAt)
	requireUTC(t, (&pgMembershipRow{CreatedAt: at}).toMembership().CreatedAt)
	requireUTC(t, (&pgMemberProfileRow{CreatedAt: at}).toProfile().CreatedAt)
}
