package dataplane

import (
	"context"

	"github.com/google/uuid"
)

// Orgs resolves an org slug before any tenant scope exists.
type Orgs interface {
	BySlug(ctx context.Context, slug string) (OrgRef, error)
}

// Projects resolves a project slug inside an org.
type Projects interface {
	BySlug(ctx context.Context, orgID uuid.UUID, slug string) (ProjectRef, error)
}

// OrgRef is the org this surface needs, referenced across the module boundary by id.
type OrgRef struct{ ID uuid.UUID }

// ProjectRef is the project this surface needs, referenced across the module boundary by id.
type ProjectRef struct{ ID uuid.UUID }
