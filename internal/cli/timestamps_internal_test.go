package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/project"
)

func TestListPayloads_PrintUTCTimestamps(t *testing.T) {
	at := time.Date(2026, 10, 2, 5, 30, 0, 0, time.FixedZone("WIB", 7*3600))
	want := "2026-10-01T22:30:00Z"

	require.Equal(t, want, orgToMap(&org.Org{CreatedAt: at})["created_at"])
	require.Equal(t, want, projectToMap(&project.Project{CreatedAt: at})["created_at"])
	inv := inviteToMap(&invite.Invite{ExpiresAt: at, CreatedAt: at})
	require.Equal(t, want, inv["expires_at"])
	require.Equal(t, want, inv["created_at"])
	require.Equal(t, "2026-10-01", tableDay(at))
}
