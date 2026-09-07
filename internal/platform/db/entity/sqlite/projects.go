package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// Projects is the jet binding for the projects table.
type Projects struct {
	sqlite.Table

	ID        sqlite.ColumnString
	OrgID     sqlite.ColumnString
	Slug      sqlite.ColumnString
	Name      sqlite.ColumnString
	CreatedBy sqlite.ColumnString
	CreatedAt sqlite.ColumnString
	UpdatedAt sqlite.ColumnString
	System    sqlite.ColumnInteger

	AllColumns     sqlite.ColumnList
	MutableColumns sqlite.ColumnList
}

// NewProjects builds the projects binding.
func NewProjects(tablePrefix string) *Projects {
	var (
		id        = sqlite.StringColumn("id")
		orgID     = sqlite.StringColumn("org_id")
		slug      = sqlite.StringColumn("slug")
		name      = sqlite.StringColumn("name")
		createdBy = sqlite.StringColumn("created_by")
		createdAt = sqlite.StringColumn("created_at")
		updatedAt = sqlite.StringColumn("updated_at")
		system    = sqlite.IntegerColumn("system")
		all       = sqlite.ColumnList{id, orgID, slug, name, createdBy, createdAt, updatedAt, system}
		mutable   = sqlite.ColumnList{orgID, slug, name, createdBy, updatedAt, system}
	)
	return &Projects{
		Table:          sqlite.NewTable("", tablePrefix+"projects", "projects", all...),
		ID:             id,
		OrgID:          orgID,
		Slug:           slug,
		Name:           name,
		CreatedBy:      createdBy,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		System:         system,
		AllColumns:     all,
		MutableColumns: mutable,
	}
}
