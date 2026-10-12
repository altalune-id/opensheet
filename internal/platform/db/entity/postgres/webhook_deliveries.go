package postgres

import "github.com/go-jet/jet/v2/postgres"

// WebhookDeliveries is the jet binding for the webhook_deliveries table.
type WebhookDeliveries struct {
	postgres.Table

	ID                postgres.ColumnString
	OrgID             postgres.ColumnString
	ProjectID         postgres.ColumnString
	EndpointID        postgres.ColumnString
	DeliveryID        postgres.ColumnString
	EventID           postgres.ColumnString
	EventType         postgres.ColumnString
	Attempt           postgres.ColumnInteger
	StatusCode        postgres.ColumnInteger
	Error             postgres.ColumnString
	ResponseBody      postgres.ColumnString
	ResponseTruncated postgres.ColumnBool
	ResponseHeaders   postgres.ColumnString
	DurationMs        postgres.ColumnInteger
	CreatedAt         postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewWebhookDeliveries builds the webhook_deliveries binding.
func NewWebhookDeliveries(schema, tablePrefix string) *WebhookDeliveries {
	if schema == "" {
		schema = "public"
	}
	var (
		id            = postgres.StringColumn("id")
		orgID         = postgres.StringColumn("org_id")
		projectID     = postgres.StringColumn("project_id")
		endpointID    = postgres.StringColumn("endpoint_id")
		deliveryID    = postgres.StringColumn("delivery_id")
		eventID       = postgres.StringColumn("event_id")
		eventType     = postgres.StringColumn("event_type")
		attempt       = postgres.IntegerColumn("attempt")
		statusCode    = postgres.IntegerColumn("status_code")
		errCol        = postgres.StringColumn("error")
		respBody      = postgres.StringColumn("response_body")
		respTruncated = postgres.BoolColumn("response_truncated")
		respHeaders   = postgres.StringColumn("response_headers")
		durationMs    = postgres.IntegerColumn("duration_ms")
		createdAt     = postgres.TimestampzColumn("created_at")
		all           = postgres.ColumnList{id, orgID, projectID, endpointID, deliveryID, eventID, eventType, attempt, statusCode, errCol, respBody, respTruncated, respHeaders, durationMs, createdAt}
	)
	return &WebhookDeliveries{
		Table:             postgres.NewTable(schema, tablePrefix+"webhook_deliveries", "webhook_deliveries", all...),
		ID:                id,
		OrgID:             orgID,
		ProjectID:         projectID,
		EndpointID:        endpointID,
		DeliveryID:        deliveryID,
		EventID:           eventID,
		EventType:         eventType,
		Attempt:           attempt,
		StatusCode:        statusCode,
		Error:             errCol,
		ResponseBody:      respBody,
		ResponseTruncated: respTruncated,
		ResponseHeaders:   respHeaders,
		DurationMs:        durationMs,
		CreatedAt:         createdAt,
		AllColumns:        all,
	}
}
