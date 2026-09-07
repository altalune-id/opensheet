// Package sqlite holds jet table bindings for the SQLite dialect.
package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// Todos is the jet binding for the todos table.
type Todos struct {
	sqlite.Table

	ID        sqlite.ColumnString
	OrgID     sqlite.ColumnString
	ProjectID sqlite.ColumnString
	UserID    sqlite.ColumnString
	Title     sqlite.ColumnString
	Done      sqlite.ColumnInteger
	CreatedAt sqlite.ColumnString
	UpdatedAt sqlite.ColumnString

	AllColumns     sqlite.ColumnList
	MutableColumns sqlite.ColumnList
}

// NewTodos builds the todos binding.
func NewTodos(tablePrefix string) *Todos {
	var (
		id        = sqlite.StringColumn("id")
		orgID     = sqlite.StringColumn("org_id")
		projectID = sqlite.StringColumn("project_id")
		userID    = sqlite.StringColumn("user_id")
		title     = sqlite.StringColumn("title")
		done      = sqlite.IntegerColumn("done")
		createdAt = sqlite.StringColumn("created_at")
		updatedAt = sqlite.StringColumn("updated_at")
		all       = sqlite.ColumnList{id, orgID, projectID, userID, title, done, createdAt, updatedAt}
		mutable   = sqlite.ColumnList{orgID, projectID, userID, title, done, updatedAt}
	)
	return &Todos{
		Table:          sqlite.NewTable("", tablePrefix+"todos", "todos", all...),
		ID:             id,
		OrgID:          orgID,
		ProjectID:      projectID,
		UserID:         userID,
		Title:          title,
		Done:           done,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		AllColumns:     all,
		MutableColumns: mutable,
	}
}
