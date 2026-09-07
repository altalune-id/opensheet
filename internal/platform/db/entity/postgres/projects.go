package postgres

import "github.com/go-jet/jet/v2/postgres"

// Projects is the jet binding for the projects table.
type Projects struct {
	postgres.Table

	ID        postgres.ColumnString
	OrgID     postgres.ColumnString
	Slug      postgres.ColumnString
	Name      postgres.ColumnString
	CreatedBy postgres.ColumnString
	CreatedAt postgres.ColumnTimestampz
	UpdatedAt postgres.ColumnTimestampz
	System    postgres.ColumnBool

	AllColumns     postgres.ColumnList
	MutableColumns postgres.ColumnList
}

// NewProjects builds the projects binding.
func NewProjects(schema, tablePrefix string) *Projects {
	if schema == "" {
		schema = "public"
	}
	var (
		id        = postgres.StringColumn("id")
		orgID     = postgres.StringColumn("org_id")
		slug      = postgres.StringColumn("slug")
		name      = postgres.StringColumn("name")
		createdBy = postgres.StringColumn("created_by")
		createdAt = postgres.TimestampzColumn("created_at")
		updatedAt = postgres.TimestampzColumn("updated_at")
		system    = postgres.BoolColumn("system")
		all       = postgres.ColumnList{id, orgID, slug, name, createdBy, createdAt, updatedAt, system}
		mutable   = postgres.ColumnList{orgID, slug, name, createdBy, updatedAt, system}
	)
	return &Projects{
		Table:          postgres.NewTable(schema, tablePrefix+"projects", "projects", all...),
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
