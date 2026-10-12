package fakes

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/outbox"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/webhook"
)

// WebhookStore is an in-memory webhook.Store that scopes by the org on ctx only, like the real adapters.
type WebhookStore struct {
	mu        sync.Mutex
	endpoints map[uuid.UUID]*webhook.Endpoint
	attempts  []webhook.Attempt

	// SaveAttemptErr, when set, is returned by SaveAttempt instead of recording the attempt.
	SaveAttemptErr error
	// AfterByID, when set, runs after each successful ByID and outside the lock, so a test can interleave a racing write.
	AfterByID func()
}

// NewWebhookStore returns an empty in-memory webhook.Store.
func NewWebhookStore() *WebhookStore {
	return &WebhookStore{endpoints: map[uuid.UUID]*webhook.Endpoint{}}
}

var _ webhook.Store = (*WebhookStore)(nil)

// Seed inserts e without going through Save or the tenant scope.
func (f *WebhookStore) Seed(e *webhook.Endpoint) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.endpoints[e.ID] = cloneEndpoint(e)
}

// Attempts returns every recorded attempt in insertion order.
func (f *WebhookStore) Attempts() []webhook.Attempt {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.attempts)
}

func (f *WebhookStore) Save(ctx context.Context, e *webhook.Endpoint) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if e.OrgID != tc.OrgID {
		return &webhook.NotFoundError{ID: e.ID.String()}
	}
	stored := cloneEndpoint(e)
	if existing, ok := f.endpoints[e.ID]; ok {
		// NOTE: both real stores refuse the conflict update under another org and keep the stored created_at, org, project and secrets.
		if existing.OrgID != tc.OrgID {
			return &webhook.NotFoundError{ID: e.ID.String()}
		}
		stored.OrgID = existing.OrgID
		stored.ProjectID = existing.ProjectID
		stored.CreatedAt = existing.CreatedAt
		stored.Secrets = cloneSecrets(existing.Secrets)
	}
	f.endpoints[e.ID] = stored
	return nil
}

func (f *WebhookStore) SaveSecrets(ctx context.Context, id uuid.UUID, expected, next webhook.SealedSecrets) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.endpoints[id]
	if !ok || e.OrgID != tc.OrgID {
		return &webhook.NotFoundError{ID: id.String()}
	}
	// NOTE: both real stores compare the primary bytes and the secondary null-safely, an empty secondary being stored as NULL.
	if !bytes.Equal(e.Secrets.Primary, expected.Primary) || !sameSecondary(e.Secrets.Secondary, expected.Secondary) {
		return &webhook.SecretConflictError{ID: id.String()}
	}
	e.Secrets = cloneSecrets(next)
	if len(next.Secondary) == 0 {
		e.Secrets.Secondary = nil
	}
	e.UpdatedAt = time.Now().UTC()
	return nil
}

func (f *WebhookStore) ByID(ctx context.Context, id uuid.UUID) (*webhook.Endpoint, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	e, ok := f.endpoints[id]
	if !ok || e.OrgID != tc.OrgID {
		f.mu.Unlock()
		return nil, &webhook.NotFoundError{ID: id.String()}
	}
	out := cloneEndpoint(e)
	hook := f.AfterByID
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return out, nil
}

func (f *WebhookStore) List(ctx context.Context, orgID, projectID uuid.UUID) ([]*webhook.Endpoint, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*webhook.Endpoint, 0, len(f.endpoints))
	for _, e := range f.endpoints {
		if e.OrgID != orgID || e.OrgID != tc.OrgID || e.ProjectID != projectID {
			continue
		}
		out = append(out, cloneEndpoint(e))
	}
	slices.SortFunc(out, func(a, b *webhook.Endpoint) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID.String(), a.ID.String())
	})
	return out, nil
}

func (f *WebhookStore) Delete(ctx context.Context, id uuid.UUID) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.endpoints[id]
	if !ok || e.OrgID != tc.OrgID {
		return &webhook.NotFoundError{ID: id.String()}
	}
	delete(f.endpoints, id)
	f.attempts = slices.DeleteFunc(f.attempts, func(a webhook.Attempt) bool { return a.EndpointID == id })
	return nil
}

func (f *WebhookStore) SaveAttempt(ctx context.Context, a webhook.Attempt) error {
	if f.SaveAttemptErr != nil {
		return f.SaveAttemptErr
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	if a.OrgID != tc.OrgID {
		return &webhook.InvalidAttemptError{Field: "OrgID", Reason: "does not match the tenant scope on ctx"}
	}
	if a.ProjectID != tc.ProjectID {
		return &webhook.InvalidAttemptError{Field: "ProjectID", Reason: "does not match the tenant scope on ctx"}
	}
	if a.ID == uuid.Nil {
		a.ID = uuid.Must(uuid.NewV7())
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	a.CreatedAt = a.CreatedAt.UTC()
	a.Error = apperror.TruncateCause(a.Error, outbox.MaxCauseLen)
	a = webhook.BoundResponse(a)
	f.mu.Lock()
	defer f.mu.Unlock()
	// NOTE: mirrors the endpoint FK, which both real stores report as *NotFoundError.
	if _, ok := f.endpoints[a.EndpointID]; !ok {
		return &webhook.NotFoundError{ID: a.EndpointID.String()}
	}
	f.attempts = append(f.attempts, a)
	return nil
}

func (f *WebhookStore) ListAttempts(ctx context.Context, endpointID, deliveryID uuid.UUID) ([]webhook.Attempt, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []webhook.Attempt{}
	for _, a := range f.attempts {
		if a.OrgID == tc.OrgID && a.EndpointID == endpointID && a.DeliveryID == deliveryID {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b webhook.Attempt) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID.String(), a.ID.String())
	})
	return out, nil
}

func cloneEndpoint(e *webhook.Endpoint) *webhook.Endpoint {
	cp := *e
	cp.EventTypes = slices.Clone(e.EventTypes)
	cp.Secrets = cloneSecrets(e.Secrets)
	return &cp
}

func cloneSecrets(s webhook.SealedSecrets) webhook.SealedSecrets {
	return webhook.SealedSecrets{Primary: slices.Clone(s.Primary), Secondary: slices.Clone(s.Secondary)}
}

func sameSecondary(stored, expected []byte) bool {
	if len(stored) == 0 || len(expected) == 0 {
		return len(stored) == 0 && len(expected) == 0
	}
	return bytes.Equal(stored, expected)
}
