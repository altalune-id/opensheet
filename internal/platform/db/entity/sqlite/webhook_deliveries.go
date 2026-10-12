package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// WebhookDeliveries is the jet binding for the webhook_deliveries table.
type WebhookDeliveries struct {
	sqlite.Table

	ID                sqlite.ColumnString
	OrgID             sqlite.ColumnString
	ProjectID         sqlite.ColumnString
	EndpointID        sqlite.ColumnString
	DeliveryID        sqlite.ColumnString
	EventID           sqlite.ColumnString
	EventType         sqlite.ColumnString
	Attempt           sqlite.ColumnInteger
	StatusCode        sqlite.ColumnInteger
	Error             sqlite.ColumnString
	ResponseBody      sqlite.ColumnString
	ResponseTruncated sqlite.ColumnInteger
	ResponseHeaders   sqlite.ColumnString
	DurationMs        sqlite.ColumnInteger
	CreatedAt         sqlite.ColumnString

	AllColumns sqlite.ColumnList
}

// NewWebhookDeliveries builds the webhook_deliveries binding.
func NewWebhookDeliveries(tablePrefix string) *WebhookDeliveries {
	var (
		id            = sqlite.StringColumn("id")
		orgID         = sqlite.StringColumn("org_id")
		projectID     = sqlite.StringColumn("project_id")
		endpointID    = sqlite.StringColumn("endpoint_id")
		deliveryID    = sqlite.StringColumn("delivery_id")
		eventID       = sqlite.StringColumn("event_id")
		eventType     = sqlite.StringColumn("event_type")
		attempt       = sqlite.IntegerColumn("attempt")
		statusCode    = sqlite.IntegerColumn("status_code")
		errCol        = sqlite.StringColumn("error")
		respBody      = sqlite.StringColumn("response_body")
		respTruncated = sqlite.IntegerColumn("response_truncated")
		respHeaders   = sqlite.StringColumn("response_headers")
		durationMs    = sqlite.IntegerColumn("duration_ms")
		createdAt     = sqlite.StringColumn("created_at")
		all           = sqlite.ColumnList{id, orgID, projectID, endpointID, deliveryID, eventID, eventType, attempt, statusCode, errCol, respBody, respTruncated, respHeaders, durationMs, createdAt}
	)
	return &WebhookDeliveries{
		Table:             sqlite.NewTable("", tablePrefix+"webhook_deliveries", "webhook_deliveries", all...),
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
