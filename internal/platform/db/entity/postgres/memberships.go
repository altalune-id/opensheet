package postgres

import "github.com/go-jet/jet/v2/postgres"

// Memberships is the jet binding for the memberships table.
type Memberships struct {
	postgres.Table

	ID        postgres.ColumnString
	OrgID     postgres.ColumnString
	UserID    postgres.ColumnString
	Role      postgres.ColumnString
	CreatedAt postgres.ColumnTimestampz
	System    postgres.ColumnBool

	AllColumns     postgres.ColumnList
	MutableColumns postgres.ColumnList
}

// NewMemberships builds the memberships binding.
func NewMemberships(schema, tablePrefix string) *Memberships {
	if schema == "" {
		schema = "public"
	}
	var (
		id        = postgres.StringColumn("id")
		orgID     = postgres.StringColumn("org_id")
		userID    = postgres.StringColumn("user_id")
		role      = postgres.StringColumn("role")
		createdAt = postgres.TimestampzColumn("created_at")
		system    = postgres.BoolColumn("system")
		all       = postgres.ColumnList{id, orgID, userID, role, createdAt, system}
		mutable   = postgres.ColumnList{orgID, userID, role, system}
	)
	return &Memberships{
		Table:          postgres.NewTable(schema, tablePrefix+"memberships", "memberships", all...),
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
