package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// WebhookEndpoints is the jet binding for the webhook_endpoints table.
type WebhookEndpoints struct {
	sqlite.Table

	ID              sqlite.ColumnString
	OrgID           sqlite.ColumnString
	ProjectID       sqlite.ColumnString
	URL             sqlite.ColumnString
	Description     sqlite.ColumnString
	EventTypes      sqlite.ColumnString
	SecretPrimary   sqlite.ColumnBlob
	SecretSecondary sqlite.ColumnBlob
	Active          sqlite.ColumnInteger
	CreatedAt       sqlite.ColumnString
	UpdatedAt       sqlite.ColumnString

	AllColumns sqlite.ColumnList
}

// NewWebhookEndpoints builds the webhook_endpoints binding.
func NewWebhookEndpoints(tablePrefix string) *WebhookEndpoints {
	var (
		id              = sqlite.StringColumn("id")
		orgID           = sqlite.StringColumn("org_id")
		projectID       = sqlite.StringColumn("project_id")
		url             = sqlite.StringColumn("url")
		description     = sqlite.StringColumn("description")
		eventTypes      = sqlite.StringColumn("event_types")
		secretPrimary   = sqlite.BlobColumn("secret_primary")
		secretSecondary = sqlite.BlobColumn("secret_secondary")
		active          = sqlite.IntegerColumn("active")
		createdAt       = sqlite.StringColumn("created_at")
		updatedAt       = sqlite.StringColumn("updated_at")
		all             = sqlite.ColumnList{id, orgID, projectID, url, description, eventTypes, secretPrimary, secretSecondary, active, createdAt, updatedAt}
	)
	return &WebhookEndpoints{
		Table:           sqlite.NewTable("", tablePrefix+"webhook_endpoints", "webhook_endpoints", all...),
		ID:              id,
		OrgID:           orgID,
		ProjectID:       projectID,
		URL:             url,
		Description:     description,
		EventTypes:      eventTypes,
		SecretPrimary:   secretPrimary,
		SecretSecondary: secretSecondary,
		Active:          active,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
		AllColumns:      all,
	}
}
