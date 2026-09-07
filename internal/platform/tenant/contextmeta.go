package tenant

import (
	"context"

	"github.com/google/uuid"

	"altalune.id/template/reqid"
)

// ContextMeta extracts request_id and tenant identifiers from ctx for wire envelopes and log lines.
func ContextMeta(ctx context.Context) map[string]string {
	m := map[string]string{}
	if id := reqid.FromContext(ctx); id != "" {
		m["request_id"] = id
	}
	tc, err := From(ctx)
	if err != nil {
		return m
	}
	if tc.OrgID != uuid.Nil {
		m["org_id"] = tc.OrgID.String()
	}
	if tc.ProjectID != uuid.Nil {
		m["project_id"] = tc.ProjectID.String()
	}
	if tc.UserID != uuid.Nil {
		m["user_id"] = tc.UserID.String()
	}
	return m
}
