package postgres

import "github.com/go-jet/jet/v2/postgres"

// Todos is the jet binding for the todos table.
type Todos struct {
	postgres.Table

	ID        postgres.ColumnString
	OrgID     postgres.ColumnString
	ProjectID postgres.ColumnString
	UserID    postgres.ColumnString
	Title     postgres.ColumnString
	Done      postgres.ColumnBool
	CreatedAt postgres.ColumnTimestampz
	UpdatedAt postgres.ColumnTimestampz

	AllColumns     postgres.ColumnList
	MutableColumns postgres.ColumnList
}

// NewTodos builds the todos binding.
func NewTodos(schema, tablePrefix string) *Todos {
	if schema == "" {
		schema = "public"
	}
	var (
		id        = postgres.StringColumn("id")
		orgID     = postgres.StringColumn("org_id")
		projectID = postgres.StringColumn("project_id")
		userID    = postgres.StringColumn("user_id")
		title     = postgres.StringColumn("title")
		done      = postgres.BoolColumn("done")
		createdAt = postgres.TimestampzColumn("created_at")
		updatedAt = postgres.TimestampzColumn("updated_at")
		all       = postgres.ColumnList{id, orgID, projectID, userID, title, done, createdAt, updatedAt}
		mutable   = postgres.ColumnList{orgID, projectID, userID, title, done, updatedAt}
	)
	return &Todos{
		Table:          postgres.NewTable(schema, tablePrefix+"todos", "todos", all...),
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
