package postgres

import "github.com/go-jet/jet/v2/postgres"

// APIKeyProjects is the jet binding for the api_key_projects table.
type APIKeyProjects struct {
	postgres.Table

	OrgID     postgres.ColumnString
	KeyID     postgres.ColumnString
	ProjectID postgres.ColumnString
	CreatedAt postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewAPIKeyProjects builds the api_key_projects binding.
func NewAPIKeyProjects(schema, tablePrefix string) *APIKeyProjects {
	if schema == "" {
		schema = "public"
	}
	var (
		orgID     = postgres.StringColumn("org_id")
		keyID     = postgres.StringColumn("key_id")
		projectID = postgres.StringColumn("project_id")
		createdAt = postgres.TimestampzColumn("created_at")
		all       = postgres.ColumnList{orgID, keyID, projectID, createdAt}
	)
	return &APIKeyProjects{
		Table:      postgres.NewTable(schema, tablePrefix+"api_key_projects", "api_key_projects", all...),
		OrgID:      orgID,
		KeyID:      keyID,
		ProjectID:  projectID,
		CreatedAt:  createdAt,
		AllColumns: all,
	}
}
