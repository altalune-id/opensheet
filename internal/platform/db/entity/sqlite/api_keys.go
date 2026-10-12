package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// APIKeys is the jet binding for the api_keys table.
type APIKeys struct {
	sqlite.Table

	ID          sqlite.ColumnString
	OrgID       sqlite.ColumnString
	ProjectID   sqlite.ColumnString
	Kind        sqlite.ColumnString
	AllProjects sqlite.ColumnBool
	Name        sqlite.ColumnString
	SecretHash  sqlite.ColumnBlob
	SecretHint  sqlite.ColumnString
	Scopes      sqlite.ColumnString
	ResourceIDs sqlite.ColumnString
	CreatedBy   sqlite.ColumnString
	CreatedAt   sqlite.ColumnString
	ExpiresAt   sqlite.ColumnString
	RevokedAt   sqlite.ColumnString
	LastUsedAt  sqlite.ColumnString

	AllColumns sqlite.ColumnList
}

// NewAPIKeys builds the api_keys binding.
func NewAPIKeys(tablePrefix string) *APIKeys {
	var (
		iD          = sqlite.StringColumn("id")
		orgID       = sqlite.StringColumn("org_id")
		projectID   = sqlite.StringColumn("project_id")
		kind        = sqlite.StringColumn("kind")
		allProjects = sqlite.BoolColumn("all_projects")
		name        = sqlite.StringColumn("name")
		secretHash  = sqlite.BlobColumn("secret_hash")
		secretHint  = sqlite.StringColumn("secret_hint")
		scopes      = sqlite.StringColumn("scopes")
		resourceIDs = sqlite.StringColumn("resource_ids")
		createdBy   = sqlite.StringColumn("created_by")
		createdAt   = sqlite.StringColumn("created_at")
		expiresAt   = sqlite.StringColumn("expires_at")
		revokedAt   = sqlite.StringColumn("revoked_at")
		lastUsedAt  = sqlite.StringColumn("last_used_at")
		all         = sqlite.ColumnList{iD, orgID, projectID, kind, allProjects, name, secretHash, secretHint, scopes, resourceIDs, createdBy, createdAt, expiresAt, revokedAt, lastUsedAt}
	)
	return &APIKeys{
		Table:       sqlite.NewTable("", tablePrefix+"api_keys", "api_keys", all...),
		ID:          iD,
		OrgID:       orgID,
		ProjectID:   projectID,
		Kind:        kind,
		AllProjects: allProjects,
		Name:        name,
		SecretHash:  secretHash,
		SecretHint:  secretHint,
		Scopes:      scopes,
		ResourceIDs: resourceIDs,
		CreatedBy:   createdBy,
		CreatedAt:   createdAt,
		ExpiresAt:   expiresAt,
		RevokedAt:   revokedAt,
		LastUsedAt:  lastUsedAt,
		AllColumns:  all,
	}
}
