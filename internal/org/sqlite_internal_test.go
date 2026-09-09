package org

import (
	"testing"

	"github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSQLiteStore_MemberListStatementsCarryTheUserIDTiebreak(t *testing.T) {
	s := newSQLiteStore(nil, "opensheet_")
	orgID := uuid.New()

	for name, stmt := range map[string]sqlite.SelectStatement{
		"listMembers":        s.listMembersStmt(orgID),
		"listMemberProfiles": s.listMemberProfilesStmt(orgID),
	} {
		query, _ := stmt.Sql()
		require.Contains(t, query, "ORDER BY memberships.created_at ASC, memberships.user_id ASC",
			"%s must emit the tiebreak; a row-order test alone passes on plan luck", name)
	}
}
