package postgres

import "github.com/go-jet/jet/v2/postgres"

// Roles is the jet binding for the roles table.
type Roles struct {
	postgres.Table

	ID          postgres.ColumnString
	OrgID       postgres.ColumnString
	Name        postgres.ColumnString
	Description postgres.ColumnString
	Permissions postgres.ColumnString
	CreatedAt   postgres.ColumnTimestampz
	UpdatedAt   postgres.ColumnTimestampz
	Version     postgres.ColumnInteger

	AllColumns postgres.ColumnList
}

// NewRoles builds the roles binding.
func NewRoles(schema, tablePrefix string) *Roles {
	if schema == "" {
		schema = "public"
	}
	var (
		id          = postgres.StringColumn("id")
		orgID       = postgres.StringColumn("org_id")
		name        = postgres.StringColumn("name")
		description = postgres.StringColumn("description")
		permissions = postgres.StringColumn("permissions")
		createdAt   = postgres.TimestampzColumn("created_at")
		updatedAt   = postgres.TimestampzColumn("updated_at")
		version     = postgres.IntegerColumn("version")
		all         = postgres.ColumnList{id, orgID, name, description, permissions, createdAt, updatedAt, version}
	)
	return &Roles{
		Table:       postgres.NewTable(schema, tablePrefix+"roles", "roles", all...),
		ID:          id,
		OrgID:       orgID,
		Name:        name,
		Description: description,
		Permissions: permissions,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
		Version:     version,
		AllColumns:  all,
	}
}
