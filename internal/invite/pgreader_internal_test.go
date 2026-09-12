package invite

import (
	"testing"

	"github.com/stretchr/testify/require"

	pdb "altalune.id/opensheet/internal/platform/db"
)

func TestPostgresStore_PendingByEmailOrdersOnTheOuterStatement(t *testing.T) {
	s := newPostgresStore(pdb.Pool{}, nil, "", "opensheet_")
	require.Contains(t, s.pendingByEmailStmt, "ORDER BY i.created_at ASC, i.id ASC",
		"Postgres may inline the LANGUAGE sql wrapper, so only the outer ORDER BY is a guarantee")
}
