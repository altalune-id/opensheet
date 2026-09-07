// Package tenant carries the OrgID/ProjectID/UserID triple through the request lifecycle.
package tenant

import (
	"context"

	"github.com/google/uuid"
)

// Context is the tenant scope threaded through every request.
type Context struct {
	OrgID     uuid.UUID
	ProjectID uuid.UUID
	UserID    uuid.UUID
}

type ctxKey struct{}

// Into returns a copy of ctx carrying tc.
func Into(ctx context.Context, tc Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, tc)
}

// From returns the tenant Context carried by ctx, or an error when it is absent or names no org.
// SECURITY: a zero OrgID sets app.current_org_id to the nil uuid, which reads as a real scope matching no rows.
func From(ctx context.Context) (Context, error) {
	tc, ok := ctx.Value(ctxKey{}).(Context)
	if !ok {
		return Context{}, &MissingError{}
	}
	if tc.OrgID == uuid.Nil {
		return Context{}, &UnscopedError{}
	}
	return tc, nil
}

// WithProject returns ctx scoped to projectID, keeping the org and user already carried on it.
func WithProject(ctx context.Context, projectID uuid.UUID) context.Context {
	tc, _ := ctx.Value(ctxKey{}).(Context)
	tc.ProjectID = projectID
	return Into(ctx, tc)
}

// WithOrg returns ctx scoped to orgID, keeping any project and user already carried on it.
func WithOrg(ctx context.Context, orgID uuid.UUID) context.Context {
	tc, _ := ctx.Value(ctxKey{}).(Context)
	tc.OrgID = orgID
	return Into(ctx, tc)
}
