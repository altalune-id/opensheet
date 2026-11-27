package role

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/tenant"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/opensheet/internal/role")

// StaffOrgs tells whether an org is the platform staff org, where custom roles are refused.
type StaffOrgs interface {
	IsStaff(ctx context.Context, orgID uuid.UUID) (bool, error)
}

// Service is the custom roles driving port.
type Service struct {
	store      Store
	orgs       StaffOrgs
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
	uow        tenant.UnitOfWork
	now        func() time.Time
}

// NewService binds the service to its dependencies.
func NewService(store Store, orgs StaffOrgs, log *slog.Logger, unexpected apperror.UnexpectedFunc, uow tenant.UnitOfWork) *Service {
	return &Service{store: store, orgs: orgs, log: log.With("module", "role"), unexpected: unexpected, uow: uow, now: time.Now}
}

// Create adds a custom role to the org on ctx. SECURITY: needs roles:write, and the caller must hold every permission it grants.
func (s *Service) Create(ctx context.Context, name, description string, perms []Permission) (*Role, error) {
	ctx, span := tracer.Start(ctx, "role.Create")
	defer span.End()
	tc, err := s.writable(ctx)
	if err != nil {
		return nil, err
	}
	r, err := New(tc.OrgID, name, description, perms, s.now())
	if err != nil {
		return nil, err
	}
	if !authz.Covers(authz.Held(ctx), r.Permissions...) {
		return nil, &authz.EscalationError{Role: r.Name}
	}
	if err := s.uow(ctx, func(ctx context.Context) error { return s.save(ctx, r, 0, "role.Create") }); err != nil {
		return nil, err
	}
	return r, nil
}

// Update replaces a custom role's name, description and permissions; ifVersion 0 checks the version it loaded. SECURITY: needs roles:write, and the caller must hold every permission the role has and gets.
func (s *Service) Update(ctx context.Context, id uuid.UUID, name, description string, perms []Permission, ifVersion int64) (*Role, error) {
	ctx, span := tracer.Start(ctx, "role.Update")
	defer span.End()
	if _, err := s.writable(ctx); err != nil {
		return nil, err
	}
	r, err := s.byID(ctx, id, "role.Update")
	if err != nil {
		return nil, err
	}
	if !authz.Covers(authz.Held(ctx), r.Permissions...) {
		return nil, &authz.EscalationError{Role: r.Name}
	}
	if err := r.Revise(name, description, perms, s.now()); err != nil {
		return nil, err
	}
	if !authz.Covers(authz.Held(ctx), r.Permissions...) {
		return nil, &authz.EscalationError{Role: r.Name}
	}
	if ifVersion == 0 {
		ifVersion = r.Version
	}
	if err := s.uow(ctx, func(ctx context.Context) error { return s.save(ctx, r, ifVersion, "role.Update") }); err != nil {
		return nil, err
	}
	r.Version++
	return r, nil
}

// Delete removes a custom role nobody holds. SECURITY: needs roles:write.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "role.Delete")
	defer span.End()
	if _, err := s.writable(ctx); err != nil {
		return err
	}
	if err := s.store.Delete(ctx, id); err != nil {
		if IsInUseError(err) || IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, "role.Delete", err, "role_id", id.String())
	}
	return nil
}

// Allowed reports whether the org on ctx may have custom roles.
func (s *Service) Allowed(ctx context.Context) (bool, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return false, err
	}
	staff, err := s.staff(ctx, tc.OrgID)
	return !staff, err
}

// List returns the org's custom roles by name, none in the staff org. SECURITY: needs members:write, which assigning a role takes.
func (s *Service) List(ctx context.Context) ([]*Role, error) {
	ctx, span := tracer.Start(ctx, "role.List")
	defer span.End()
	if err := s.readable(ctx); err != nil {
		return nil, err
	}
	if allowed, err := s.Allowed(ctx); err != nil || !allowed {
		return nil, err
	}
	out, err := s.store.List(ctx)
	if err != nil {
		return nil, s.unexpected(ctx, "role.List", err)
	}
	return out, nil
}

// Get returns one custom role. SECURITY: needs members:write, as List does.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Role, error) {
	ctx, span := tracer.Start(ctx, "role.Get")
	defer span.End()
	if err := s.readable(ctx); err != nil {
		return nil, err
	}
	return s.byID(ctx, id, "role.Get")
}

// HeldBy counts, per role id, the members and pending invites holding it, so the console disables a delete that would fail. SECURITY: needs members:write.
func (s *Service) HeldBy(ctx context.Context) (map[uuid.UUID]int, error) {
	ctx, span := tracer.Start(ctx, "role.HeldBy")
	defer span.End()
	if err := s.readable(ctx); err != nil {
		return nil, err
	}
	held, err := s.store.HeldBy(ctx)
	if err != nil {
		return nil, s.unexpected(ctx, "role.HeldBy", err)
	}
	return held, nil
}

func (s *Service) readable(ctx context.Context) error {
	if _, err := tenant.From(ctx); err != nil {
		return err
	}
	return authz.CanOrgWide(ctx, authz.MembersWrite)
}

func (s *Service) writable(ctx context.Context) (tenant.Context, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return tenant.Context{}, err
	}
	if err := authz.CanOrgWide(ctx, authz.RolesWrite); err != nil {
		return tenant.Context{}, err
	}
	staff, err := s.staff(ctx, tc.OrgID)
	if err != nil {
		return tenant.Context{}, err
	}
	if staff {
		return tenant.Context{}, &SystemOrgError{}
	}
	return tc, nil
}

func (s *Service) staff(ctx context.Context, orgID uuid.UUID) (bool, error) {
	staff, err := s.orgs.IsStaff(ctx, orgID)
	if err != nil {
		return false, s.unexpected(ctx, "role: staff org", err, "org_id", orgID.String())
	}
	return staff, nil
}

func (s *Service) byID(ctx context.Context, id uuid.UUID, op string) (*Role, error) {
	r, err := s.store.ByID(ctx, id)
	if err != nil {
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, op+": load", err, "role_id", id.String())
	}
	return r, nil
}

func (s *Service) save(ctx context.Context, r *Role, ifVersion int64, op string) error {
	if err := s.store.Save(ctx, r, ifVersion); err != nil {
		if IsAlreadyExistsError(err) || IsNotFoundError(err) || IsStaleVersionError(err) {
			return err
		}
		return s.unexpected(ctx, op+": save", err, "role_id", r.ID.String())
	}
	return nil
}
