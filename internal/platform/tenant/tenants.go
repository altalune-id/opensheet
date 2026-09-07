package tenant

import (
	"context"
	"log/slog"

	"altalune.id/template/scheduler"
)

var _ scheduler.Tenants = (*Enumerator)(nil)

// Enumerator fans a callback out over every org read from an OrgReader.
type Enumerator struct {
	orgs OrgReader
	log  *slog.Logger
}

// NewEnumerator builds the tenant enumerator over an OrgReader.
func NewEnumerator(orgs OrgReader, log *slog.Logger) *Enumerator {
	return &Enumerator{orgs: orgs, log: log}
}

// Each invokes fn once per tenant with a tenant-bound ctx, aborting on the first non-nil fn error.
func (e *Enumerator) Each(ctx context.Context, fn func(ctx context.Context, tenantID string) error) error {
	ids, err := e.orgs.OrgIDs(ctx)
	if err != nil {
		return err
	}
	if e.log != nil {
		e.log.DebugContext(ctx, "tenant: enumerated orgs", slog.Int("count", len(ids)))
	}
	for _, id := range ids {
		if fnErr := fn(Into(ctx, Context{OrgID: id}), id.String()); fnErr != nil {
			return fnErr
		}
	}
	return nil
}
