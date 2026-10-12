package postgres

import "github.com/go-jet/jet/v2/postgres"

// Todos is the jet binding for the todos table.
type Todos struct {
	postgres.Table

	ID             postgres.ColumnString
	OrgID          postgres.ColumnString
	ProjectID      postgres.ColumnString
	UserID         postgres.ColumnString
	CreatedByKeyID postgres.ColumnString
	Title          postgres.ColumnString
	Done           postgres.ColumnBool
	CreatedAt      postgres.ColumnTimestampz
	UpdatedAt      postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewTodos builds the todos binding.
func NewTodos(schema, tablePrefix string) *Todos {
	if schema == "" {
		schema = "public"
	}
	var (
		id             = postgres.StringColumn("id")
		orgID          = postgres.StringColumn("org_id")
		projectID      = postgres.StringColumn("project_id")
		userID         = postgres.StringColumn("user_id")
		createdByKeyID = postgres.StringColumn("created_by_key_id")
		title          = postgres.StringColumn("title")
		done           = postgres.BoolColumn("done")
		createdAt      = postgres.TimestampzColumn("created_at")
		updatedAt      = postgres.TimestampzColumn("updated_at")
		all            = postgres.ColumnList{id, orgID, projectID, userID, createdByKeyID, title, done, createdAt, updatedAt}
	)
	return &Todos{
		Table:          postgres.NewTable(schema, tablePrefix+"todos", "todos", all...),
		ID:             id,
		OrgID:          orgID,
		ProjectID:      projectID,
		UserID:         userID,
		CreatedByKeyID: createdByKeyID,
		Title:          title,
		Done:           done,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		AllColumns:     all,
	}
}
