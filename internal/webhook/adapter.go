package webhook

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/events"
	"altalune.id/opensheet/internal/platform/outbox"
	"altalune.id/opensheet/internal/platform/tenant"
)

func prepareAttempt(tc tenant.Context, a Attempt) (Attempt, error) {
	if a.OrgID != tc.OrgID {
		// SECURITY: an attempt naming another org would land in that org's history.
		return Attempt{}, &InvalidAttemptError{Field: "OrgID", Reason: "does not match the tenant scope on ctx"}
	}
	if a.ProjectID != tc.ProjectID {
		// SECURITY: the project FK is checked as the table owner and bypasses RLS, so only this
		// comparison stops an attempt carrying another project.
		return Attempt{}, &InvalidAttemptError{Field: "ProjectID", Reason: "does not match the tenant scope on ctx"}
	}
	if a.ID == uuid.Nil {
		a.ID = uuid.Must(uuid.NewV7())
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	a.CreatedAt = a.CreatedAt.UTC()
	a.Error = apperror.TruncateCause(a.Error, outbox.MaxCauseLen)
	return BoundResponse(a), nil
}

func marshalEventTypes(types []events.Type) (string, error) {
	if types == nil {
		types = []events.Type{}
	}
	b, err := json.Marshal(types)
	if err != nil {
		return "", fmt.Errorf("webhook: marshal event types: %w", err)
	}
	return string(b), nil
}

func unmarshalEventTypes(raw string) ([]events.Type, error) {
	var types []events.Type
	if err := json.Unmarshal([]byte(raw), &types); err != nil {
		return nil, fmt.Errorf("webhook: unmarshal event types: %w", err)
	}
	return types, nil
}

type storedHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func marshalHeaders(hs []Header) (string, error) {
	stored := make([]storedHeader, len(hs))
	for i, h := range hs {
		stored[i] = storedHeader(h)
	}
	b, err := json.Marshal(stored)
	if err != nil {
		return "", fmt.Errorf("webhook: marshal response headers: %w", err)
	}
	return string(b), nil
}

func unmarshalHeaders(raw string) ([]Header, error) {
	var stored []storedHeader
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, fmt.Errorf("webhook: unmarshal response headers: %w", err)
	}
	hs := make([]Header, len(stored))
	for i, h := range stored {
		hs[i] = Header(h)
	}
	return hs, nil
}
