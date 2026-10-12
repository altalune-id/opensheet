package postgres

import "github.com/go-jet/jet/v2/postgres"

// WebhookEndpoints is the jet binding for the webhook_endpoints table.
type WebhookEndpoints struct {
	postgres.Table

	ID              postgres.ColumnString
	OrgID           postgres.ColumnString
	ProjectID       postgres.ColumnString
	URL             postgres.ColumnString
	Description     postgres.ColumnString
	EventTypes      postgres.ColumnString
	SecretPrimary   postgres.ColumnBytea
	SecretSecondary postgres.ColumnBytea
	Active          postgres.ColumnBool
	CreatedAt       postgres.ColumnTimestampz
	UpdatedAt       postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewWebhookEndpoints builds the webhook_endpoints binding.
func NewWebhookEndpoints(schema, tablePrefix string) *WebhookEndpoints {
	if schema == "" {
		schema = "public"
	}
	var (
		id              = postgres.StringColumn("id")
		orgID           = postgres.StringColumn("org_id")
		projectID       = postgres.StringColumn("project_id")
		url             = postgres.StringColumn("url")
		description     = postgres.StringColumn("description")
		eventTypes      = postgres.StringColumn("event_types")
		secretPrimary   = postgres.ByteaColumn("secret_primary")
		secretSecondary = postgres.ByteaColumn("secret_secondary")
		active          = postgres.BoolColumn("active")
		createdAt       = postgres.TimestampzColumn("created_at")
		updatedAt       = postgres.TimestampzColumn("updated_at")
		all             = postgres.ColumnList{id, orgID, projectID, url, description, eventTypes, secretPrimary, secretSecondary, active, createdAt, updatedAt}
	)
	return &WebhookEndpoints{
		Table:           postgres.NewTable(schema, tablePrefix+"webhook_endpoints", "webhook_endpoints", all...),
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
