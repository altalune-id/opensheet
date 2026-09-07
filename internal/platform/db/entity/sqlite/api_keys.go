package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// APIKeys is the jet binding for the api_keys table.
type APIKeys struct {
	sqlite.Table

	ID         sqlite.ColumnString
	OrgID      sqlite.ColumnString
	ProjectID  sqlite.ColumnString
	Name       sqlite.ColumnString
	KeyPrefix  sqlite.ColumnString
	SecretHash sqlite.ColumnBlob
	// NOTE: scopes holds a JSON array; SQLite has no array type.
	Scopes     sqlite.ColumnString
	ExpiresAt  sqlite.ColumnString
	LastUsedAt sqlite.ColumnString
	RevokedAt  sqlite.ColumnString
	CreatedAt  sqlite.ColumnString

	AllColumns     sqlite.ColumnList
	MutableColumns sqlite.ColumnList
	// PublicColumns omits secret_hash. SECURITY: select through it on every path that is not verifying a key.
	PublicColumns sqlite.ColumnList
}

// NewAPIKeys builds the api_keys binding.
func NewAPIKeys(tablePrefix string) *APIKeys {
	var (
		id         = sqlite.StringColumn("id")
		orgID      = sqlite.StringColumn("org_id")
		projectID  = sqlite.StringColumn("project_id")
		name       = sqlite.StringColumn("name")
		keyPrefix  = sqlite.StringColumn("key_prefix")
		secretHash = sqlite.BlobColumn("secret_hash")
		scopes     = sqlite.StringColumn("scopes")
		expiresAt  = sqlite.StringColumn("expires_at")
		lastUsedAt = sqlite.StringColumn("last_used_at")
		revokedAt  = sqlite.StringColumn("revoked_at")
		createdAt  = sqlite.StringColumn("created_at")
		all        = sqlite.ColumnList{
			id, orgID, projectID, name, keyPrefix, secretHash, scopes,
			expiresAt, lastUsedAt, revokedAt, createdAt,
		}
		mutable = sqlite.ColumnList{
			orgID, projectID, name, keyPrefix, secretHash, scopes,
			expiresAt, lastUsedAt, revokedAt,
		}
		public = sqlite.ColumnList{
			id, orgID, projectID, name, keyPrefix, scopes,
			expiresAt, lastUsedAt, revokedAt, createdAt,
		}
	)
	return &APIKeys{
		Table:          sqlite.NewTable("", tablePrefix+"api_keys", "api_keys", all...),
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
