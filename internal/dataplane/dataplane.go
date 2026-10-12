// Package dataplane implements S3, the REST data plane over published sheets: the surface an integrator's running product calls directly, authenticated by API key.
package dataplane

import (
	"context"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/session"
)

// Authorizer authenticates a raw credential and authorizes it against a scope and tenant.
type Authorizer interface {
	Authenticate(ctx context.Context, raw string) (session.Principal, error)
	Authorize(ctx context.Context, raw, scope string, orgID, projectID, resourceID uuid.UUID) (session.Principal, error)
	AuthorizeProject(ctx context.Context, raw, scope string, orgID, projectID uuid.UUID) (session.Principal, error)
}

// Capabilities is the feature-flag snapshot the data plane checks, such as public reads.
type Capabilities = capabilities.Capabilities
