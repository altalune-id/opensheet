package postgres

import "github.com/go-jet/jet/v2/postgres"

// Orgs is the jet binding for the orgs table.
type Orgs struct {
	postgres.Table

	ID        postgres.ColumnString
	Slug      postgres.ColumnString
	Name      postgres.ColumnString
	CreatedBy postgres.ColumnString
	CreatedAt postgres.ColumnTimestampz
	UpdatedAt postgres.ColumnTimestampz
	System    postgres.ColumnBool

	AllColumns     postgres.ColumnList
	MutableColumns postgres.ColumnList
}

// NewOrgs builds the orgs binding.
func NewOrgs(schema, tablePrefix string) *Orgs {
	if schema == "" {
		schema = "public"
	}
	var (
		id        = postgres.StringColumn("id")
		slug      = postgres.StringColumn("slug")
		name      = postgres.StringColumn("name")
		createdBy = postgres.StringColumn("created_by")
		createdAt = postgres.TimestampzColumn("created_at")
		updatedAt = postgres.TimestampzColumn("updated_at")
		system    = postgres.BoolColumn("system")
		all       = postgres.ColumnList{id, slug, name, createdBy, createdAt, updatedAt, system}
		mutable   = postgres.ColumnList{slug, name, createdBy, updatedAt, system}
	)
	return &Orgs{
		Table:          postgres.NewTable(schema, tablePrefix+"orgs", "orgs", all...),
		ID:             id,
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
