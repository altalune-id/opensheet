package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// APIKeyProjects is the jet binding for the api_key_projects table.
type APIKeyProjects struct {
	sqlite.Table

	OrgID     sqlite.ColumnString
	KeyID     sqlite.ColumnString
	ProjectID sqlite.ColumnString
	CreatedAt sqlite.ColumnString

	AllColumns sqlite.ColumnList
}

// NewAPIKeyProjects builds the api_key_projects binding.
func NewAPIKeyProjects(tablePrefix string) *APIKeyProjects {
	var (
		orgID     = sqlite.StringColumn("org_id")
		keyID     = sqlite.StringColumn("key_id")
		projectID = sqlite.StringColumn("project_id")
		createdAt = sqlite.StringColumn("created_at")
		all       = sqlite.ColumnList{orgID, keyID, projectID, createdAt}
	)
	return &APIKeyProjects{
		Table:      sqlite.NewTable("", tablePrefix+"api_key_projects", "api_key_projects", all...),
		OrgID:      orgID,
		KeyID:      keyID,
		ProjectID:  projectID,
		CreatedAt:  createdAt,
		AllColumns: all,
	}
}
