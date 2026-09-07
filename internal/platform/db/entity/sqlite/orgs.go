package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// Orgs is the jet binding for the orgs table.
type Orgs struct {
	sqlite.Table

	ID        sqlite.ColumnString
	Slug      sqlite.ColumnString
	Name      sqlite.ColumnString
	CreatedBy sqlite.ColumnString
	CreatedAt sqlite.ColumnString
	UpdatedAt sqlite.ColumnString
	System    sqlite.ColumnInteger

	AllColumns     sqlite.ColumnList
	MutableColumns sqlite.ColumnList
}

// NewOrgs builds the orgs binding.
func NewOrgs(tablePrefix string) *Orgs {
	var (
		id        = sqlite.StringColumn("id")
		slug      = sqlite.StringColumn("slug")
		name      = sqlite.StringColumn("name")
		createdBy = sqlite.StringColumn("created_by")
		createdAt = sqlite.StringColumn("created_at")
		updatedAt = sqlite.StringColumn("updated_at")
		system    = sqlite.IntegerColumn("system")
		all       = sqlite.ColumnList{id, slug, name, createdBy, createdAt, updatedAt, system}
		mutable   = sqlite.ColumnList{slug, name, createdBy, updatedAt, system}
	)
	return &Orgs{
		Table:          sqlite.NewTable("", tablePrefix+"orgs", "orgs", all...),
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
