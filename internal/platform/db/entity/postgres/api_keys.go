package postgres

import "github.com/go-jet/jet/v2/postgres"

// APIKeys is the jet binding for the api_keys table.
type APIKeys struct {
	postgres.Table

	ID          postgres.ColumnString
	OrgID       postgres.ColumnString
	ProjectID   postgres.ColumnString
	Kind        postgres.ColumnString
	AllProjects postgres.ColumnBool
	Name        postgres.ColumnString
	SecretHash  postgres.ColumnBytea
	SecretHint  postgres.ColumnString
	Scopes      postgres.ColumnString
	ResourceIDs postgres.ColumnString
	CreatedBy   postgres.ColumnString
	CreatedAt   postgres.ColumnTimestampz
	ExpiresAt   postgres.ColumnTimestampz
	RevokedAt   postgres.ColumnTimestampz
	LastUsedAt  postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewAPIKeys builds the api_keys binding.
func NewAPIKeys(schema, tablePrefix string) *APIKeys {
	if schema == "" {
		schema = "public"
	}
	var (
		iD          = postgres.StringColumn("id")
		orgID       = postgres.StringColumn("org_id")
		projectID   = postgres.StringColumn("project_id")
		kind        = postgres.StringColumn("kind")
		allProjects = postgres.BoolColumn("all_projects")
		name        = postgres.StringColumn("name")
		secretHash  = postgres.ByteaColumn("secret_hash")
		secretHint  = postgres.StringColumn("secret_hint")
		scopes      = postgres.StringColumn("scopes")
		resourceIDs = postgres.StringColumn("resource_ids")
		createdBy   = postgres.StringColumn("created_by")
		createdAt   = postgres.TimestampzColumn("created_at")
		expiresAt   = postgres.TimestampzColumn("expires_at")
		revokedAt   = postgres.TimestampzColumn("revoked_at")
		lastUsedAt  = postgres.TimestampzColumn("last_used_at")
		all         = postgres.ColumnList{iD, orgID, projectID, kind, allProjects, name, secretHash, secretHint, scopes, resourceIDs, createdBy, createdAt, expiresAt, revokedAt, lastUsedAt}
	)
	return &APIKeys{
		Table:       postgres.NewTable(schema, tablePrefix+"api_keys", "api_keys", all...),
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
