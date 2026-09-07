package postgres

import "github.com/go-jet/jet/v2/postgres"

// APIKeys is the jet binding for the api_keys table.
type APIKeys struct {
	postgres.Table

	ID         postgres.ColumnString
	OrgID      postgres.ColumnString
	ProjectID  postgres.ColumnString
	Name       postgres.ColumnString
	KeyPrefix  postgres.ColumnString
	SecretHash postgres.ColumnBytea
	// NOTE: scopes is text[]; go-jet v2 has no array column type, so it binds as a string column.
	Scopes     postgres.ColumnString
	ExpiresAt  postgres.ColumnTimestampz
	LastUsedAt postgres.ColumnTimestampz
	RevokedAt  postgres.ColumnTimestampz
	CreatedAt  postgres.ColumnTimestampz

	AllColumns     postgres.ColumnList
	MutableColumns postgres.ColumnList
	// PublicColumns omits secret_hash. SECURITY: select through it on every path that is not verifying a key.
	PublicColumns postgres.ColumnList
}

// NewAPIKeys builds the api_keys binding.
func NewAPIKeys(schema, tablePrefix string) *APIKeys {
	if schema == "" {
		schema = "public"
	}
	var (
		id         = postgres.StringColumn("id")
		orgID      = postgres.StringColumn("org_id")
		projectID  = postgres.StringColumn("project_id")
		name       = postgres.StringColumn("name")
		keyPrefix  = postgres.StringColumn("key_prefix")
		secretHash = postgres.ByteaColumn("secret_hash")
		scopes     = postgres.StringColumn("scopes")
		expiresAt  = postgres.TimestampzColumn("expires_at")
		lastUsedAt = postgres.TimestampzColumn("last_used_at")
		revokedAt  = postgres.TimestampzColumn("revoked_at")
		createdAt  = postgres.TimestampzColumn("created_at")
		all        = postgres.ColumnList{
			id, orgID, projectID, name, keyPrefix, secretHash, scopes,
			expiresAt, lastUsedAt, revokedAt, createdAt,
		}
		mutable = postgres.ColumnList{
			orgID, projectID, name, keyPrefix, secretHash, scopes,
			expiresAt, lastUsedAt, revokedAt,
		}
		public = postgres.ColumnList{
			id, orgID, projectID, name, keyPrefix, scopes,
			expiresAt, lastUsedAt, revokedAt, createdAt,
		}
	)
	return &APIKeys{
		Table:          postgres.NewTable(schema, tablePrefix+"api_keys", "api_keys", all...),
		ID:             id,
		OrgID:          orgID,
		ProjectID:      projectID,
		Name:           name,
		KeyPrefix:      keyPrefix,
		SecretHash:     secretHash,
		Scopes:         scopes,
		ExpiresAt:      expiresAt,
		LastUsedAt:     lastUsedAt,
		RevokedAt:      revokedAt,
		CreatedAt:      createdAt,
		AllColumns:     all,
		MutableColumns: mutable,
		PublicColumns:  public,
	}
}
