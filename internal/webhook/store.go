package webhook

import (
	"context"

	"github.com/google/uuid"
)

// Store is the driven port.
type Store interface {
	// Save upserts e, writing its secrets only on insert, and returns a *NotFoundError when the id belongs to another org.
	Save(ctx context.Context, e *Endpoint) error
	// SaveSecrets replaces the endpoint's secrets when the stored pair still equals expected, or returns a *SecretConflictError or *NotFoundError.
	SaveSecrets(ctx context.Context, id uuid.UUID, expected, next SealedSecrets) error
	// ByID returns the endpoint in the caller's org, or a *NotFoundError.
	ByID(ctx context.Context, id uuid.UUID) (*Endpoint, error)
	// List returns the project's endpoints, newest first.
	List(ctx context.Context, orgID, projectID uuid.UUID) ([]*Endpoint, error)
	// Delete removes the endpoint and its attempts, or returns a *NotFoundError.
	Delete(ctx context.Context, id uuid.UUID) error
	// SaveAttempt appends a, returning an *InvalidAttemptError when its org or project disagrees with the tenant scope on ctx, or a *NotFoundError when its endpoint no longer exists.
	SaveAttempt(ctx context.Context, a Attempt) error
	// ListAttempts returns the attempts of one delivery to one endpoint, newest first.
	ListAttempts(ctx context.Context, endpointID, deliveryID uuid.UUID) ([]Attempt, error)
}
