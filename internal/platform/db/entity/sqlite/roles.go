package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// Roles is the jet binding for the roles table.
type Roles struct {
	sqlite.Table

	ID          sqlite.ColumnString
	OrgID       sqlite.ColumnString
	Name        sqlite.ColumnString
	Description sqlite.ColumnString
	Permissions sqlite.ColumnString
	CreatedAt   sqlite.ColumnString
	UpdatedAt   sqlite.ColumnString
	Version     sqlite.ColumnInteger

	AllColumns sqlite.ColumnList
}

// NewRoles builds the roles binding.
func NewRoles(tablePrefix string) *Roles {
	var (
		id          = sqlite.StringColumn("id")
		orgID       = sqlite.StringColumn("org_id")
		name        = sqlite.StringColumn("name")
		description = sqlite.StringColumn("description")
		permissions = sqlite.StringColumn("permissions")
		createdAt   = sqlite.StringColumn("created_at")
		updatedAt   = sqlite.StringColumn("updated_at")
		version     = sqlite.IntegerColumn("version")
		all         = sqlite.ColumnList{id, orgID, name, description, permissions, createdAt, updatedAt, version}
	)
	return &Roles{
		Table:       sqlite.NewTable("", tablePrefix+"roles", "roles", all...),
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
