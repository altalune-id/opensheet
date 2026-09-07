package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// Memberships is the jet binding for the memberships table.
type Memberships struct {
	sqlite.Table

	ID        sqlite.ColumnString
	OrgID     sqlite.ColumnString
	UserID    sqlite.ColumnString
	Role      sqlite.ColumnString
	CreatedAt sqlite.ColumnString
	System    sqlite.ColumnInteger

	AllColumns     sqlite.ColumnList
	MutableColumns sqlite.ColumnList
}

// NewMemberships builds the memberships binding.
func NewMemberships(tablePrefix string) *Memberships {
	var (
		id        = sqlite.StringColumn("id")
		orgID     = sqlite.StringColumn("org_id")
		userID    = sqlite.StringColumn("user_id")
		role      = sqlite.StringColumn("role")
		createdAt = sqlite.StringColumn("created_at")
		system    = sqlite.IntegerColumn("system")
		all       = sqlite.ColumnList{id, orgID, userID, role, createdAt, system}
		mutable   = sqlite.ColumnList{orgID, userID, role, system}
	)
	return &Memberships{
		Table:          sqlite.NewTable("", tablePrefix+"memberships", "memberships", all...),
		ID:             id,
		OrgID:          orgID,
		UserID:         userID,
		Role:           role,
		CreatedAt:      createdAt,
		System:         system,
		AllColumns:     all,
		MutableColumns: mutable,
	}
}
