package org

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	customrole "altalune.id/opensheet/internal/role"
	slugs "altalune.id/opensheet/slug"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/opensheet/internal/org")

// Service orchestrates org and membership use cases.
type Service struct {
	store      Store
	caps       capabilities.Capabilities
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
	uow        tenant.UnitOfWork
	perms      Permissions
	projects   Projects
	onRemoved  []MemberRemovedFunc
}

// Projects lists the projects of an org, so a grant names only real projects of that org.
type Projects interface {
	ProjectIDs(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error)
}

// WithProjects checks granted project ids through p; without it a grant naming any project is refused.
func WithProjects(p Projects) Option {
	return func(s *Service) { s.projects = p }
}

// Permissions resolves what a role holds, for the escalation guard.
type Permissions interface {
	PermissionsOf(ctx context.Context, role string, roleID uuid.UUID) ([]authz.Permission, error)
}

// Option configures a Service.
type Option func(*Service)

// WithPermissions resolves role permissions through p, custom roles included; without it a custom role is refused.
func WithPermissions(p Permissions) Option {
	return func(s *Service) { s.perms = p }
}

// MemberRemovedFunc reacts to a member leaving an org, such as revoking what the member held there.
type MemberRemovedFunc func(ctx context.Context, orgID, userID uuid.UUID) error

// OnMemberRemoved registers fn to run inside RemoveMember's unit of work, before the membership is deleted; fn's writes must join that unit of work. NOTE: register at boot, before serving; the list is not guarded for concurrent registration.
func (s *Service) OnMemberRemoved(fn MemberRemovedFunc) { s.onRemoved = append(s.onRemoved, fn) }

// NewService wires the Service.
func NewService(store Store, caps capabilities.Capabilities, log *slog.Logger, unexpected apperror.UnexpectedFunc, uow tenant.UnitOfWork, opts ...Option) *Service {
	s := &Service{store: store, caps: caps, log: log, unexpected: unexpected, uow: uow}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// BootstrapSingleton idempotently ensures the one system org exists with an owner membership for ownerID; an existing system org is reused whatever slug is passed, and a blank slug is generated.
func (s *Service) BootstrapSingleton(ctx context.Context, slug, name string, ownerID uuid.UUID) (*Org, error) {
	ctx, span := tracer.Start(ctx, "org.BootstrapSingleton")
	defer span.End()

	// NOTE: bootstrap runs before any tenant exists, but the postgres store needs a tenant scope and
	// the orgs RLS policy is `id = current_setting('app.current_org_id')` — so scope to the org this
	// call will create, and reuse that id when inserting so the row satisfies its own policy.
	ctx, orgID := scopeForBootstrap(ctx, ownerID)

	o, err := s.ensureSingleton(ctx, orgID, strings.TrimSpace(slug), name, ownerID)
	if !IsSystemOrgExistsError(err) && !IsUnreadableExistingOrgError(err) {
		return o, err
	}
	winner, wErr := s.store.SystemOrg(ctx)
	if IsNotFoundError(wErr) && IsUnreadableExistingOrgError(err) {
		return nil, err
	}
	if wErr != nil {
		return nil, s.unexpected(ctx, "org.BootstrapSingleton: SystemOrg after conflict", wErr)
	}
	return s.adoptSingleton(ctx, winner, ownerID)
}

func (s *Service) ensureSingleton(ctx context.Context, orgID uuid.UUID, slug, name string, ownerID uuid.UUID) (*Org, error) {
	existing, err := s.store.SystemOrg(ctx)
	if err == nil {
		return s.adoptSingleton(ctx, existing, ownerID)
	}
	if !IsNotFoundError(err) {
		return nil, s.unexpected(ctx, "org.BootstrapSingleton: SystemOrg", err)
	}

	if slug == "" {
		return slugs.Retry(slugs.MaxAttempts, IsAlreadyExistsError, func(candidate string) (*Org, error) {
			_, err := s.store.BySlug(ctx, candidate)
			if err == nil {
				return nil, &AlreadyExistsError{Slug: candidate}
			}
			if !IsNotFoundError(err) {
				return nil, s.unexpected(ctx, "org.BootstrapSingleton: BySlug", err, "slug", candidate)
			}
			return s.insertSingleton(ctx, orgID, candidate, name, ownerID)
		})
	}

	existing, err = s.store.BySlug(ctx, slug)
	if err == nil {
		return s.adoptSingleton(ctx, existing, ownerID)
	}
	if !IsNotFoundError(err) {
		return nil, s.unexpected(ctx, "org.BootstrapSingleton: BySlug", err, "slug", slug)
	}
	o, err := s.insertSingleton(ctx, orgID, slug, name, ownerID)
	if IsAlreadyExistsError(err) {
		// NOTE: the slug exists but the lookup above could not see it — under RLS that means the row
		// belongs to a different org id, so report it plainly instead of a misleading not-found.
		return nil, &UnreadableExistingOrgError{Slug: slug}
	}
	return o, err
}

// SECURITY: adopting binds the scope to the adopted org's own id; the caller holds the setup token, which is what authorizes joining it as owner.
func (s *Service) adoptSingleton(ctx context.Context, existing *Org, ownerID uuid.UUID) (*Org, error) {
	ctx = tenant.Into(ctx, tenant.Context{OrgID: existing.ID, UserID: ownerID})
	if !existing.System {
		existing.System = true
		if sErr := s.store.Save(ctx, existing); sErr != nil {
			if IsSystemOrgExistsError(sErr) {
				return nil, sErr
			}
			return nil, s.unexpected(ctx, "org.BootstrapSingleton: Save", sErr, "org_id", existing.ID.String())
		}
	}
	m, mErr := s.store.MembershipOf(ctx, existing.ID, ownerID)
	if mErr == nil {
		if !m.System {
			m.System = true
			if sErr := s.store.SaveMembership(ctx, m); sErr != nil {
				return nil, s.unexpected(ctx, "org.BootstrapSingleton: SaveMembership", sErr, "org_id", existing.ID.String())
			}
		}
		return existing, nil
	}
	if !IsMembershipMissingError(mErr) && !IsNotFoundError(mErr) {
		return nil, s.unexpected(ctx, "org.BootstrapSingleton: MembershipOf", mErr, "org_id", existing.ID.String())
	}
	m, mErr = NewMembership(existing.ID, ownerID, RoleOwner)
	if mErr != nil {
		return nil, mErr
	}
	m.System = true
	if mErr := s.store.SaveMembership(ctx, m); mErr != nil {
		return nil, s.unexpected(ctx, "org.BootstrapSingleton: SaveMembership", mErr, "org_id", existing.ID.String())
	}
	return existing, nil
}

func (s *Service) insertSingleton(ctx context.Context, orgID uuid.UUID, slug, name string, ownerID uuid.UUID) (*Org, error) {
	o, err := NewOrg(slug, name, ownerID)
	if err != nil {
		return nil, err
	}
	o.ID = orgID
	o.System = true
	if err := s.store.Save(ctx, o); err != nil {
		if IsAlreadyExistsError(err) || IsSystemOrgExistsError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "org.BootstrapSingleton: Save", err, "slug", slug)
	}
	m, err := NewMembership(o.ID, ownerID, RoleOwner)
	if err != nil {
		return nil, err
	}
	m.System = true
	if err := s.store.SaveMembership(ctx, m); err != nil {
		return nil, s.unexpected(ctx, "org.BootstrapSingleton: SaveMembership", err, "org_id", o.ID.String())
	}
	return o, nil
}

// Create constructs a new org and enrols the owner as an owner-role member; an empty slug is generated.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*Org, error) {
	ctx, span := tracer.Start(ctx, "org.Create")
	defer span.End()

	if !s.caps.OrgCreation {
		return nil, &CreationDisabledError{}
	}

	if chosen := strings.TrimSpace(req.Slug); chosen != "" {
		return s.createWithSlug(ctx, req, chosen)
	}
	return slugs.Retry(slugs.MaxAttempts, IsAlreadyExistsError, func(candidate string) (*Org, error) {
		return s.createWithSlug(ctx, req, candidate)
	})
}

func (s *Service) createWithSlug(ctx context.Context, req CreateRequest, chosen string) (*Org, error) {
	existing, err := s.store.BySlug(ctx, chosen)
	switch {
	case err == nil && existing != nil:
		return nil, &AlreadyExistsError{Slug: chosen}
	case err != nil && !IsNotFoundError(err):
		return nil, s.unexpected(ctx, "org.Create: BySlug", fmt.Errorf("org.Create: BySlug: %w", err), "slug", chosen)
	}

	o, err := NewOrg(chosen, req.Name, req.OwnerID)
	if err != nil {
		return nil, err
	}
	// SECURITY: orgs and memberships are scoped by the org's own id, so the new row's scope is the only one RLS accepts — never the caller's active org.
	ctx = tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: req.OwnerID})
	if err := s.store.Save(ctx, o); err != nil {
		if IsAlreadyExistsError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "org.Create: Save", fmt.Errorf("org.Create: Save: %w", err), "slug", chosen)
	}

	m, err := NewMembership(o.ID, req.OwnerID, RoleOwner)
	if err != nil {
		return nil, err
	}
	if err := s.store.SaveMembership(ctx, m); err != nil {
		return nil, s.unexpected(ctx, "org.Create: SaveMembership", fmt.Errorf("org.Create: SaveMembership: %w", err), "org_id", o.ID.String(), "user_id", req.OwnerID.String())
	}
	return o, nil
}

// List returns every org the given user is a member of.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]*Org, error) {
	ctx, span := tracer.Start(ctx, "org.List")
	defer span.End()

	orgs, err := s.store.List(ctx, userID)
	if err != nil {
		return nil, s.unexpected(ctx, "org.List", fmt.Errorf("org.List: %w", err), "user_id", userID.String())
	}
	return orgs, nil
}

// Rename loads, mutates and persists an org's display name.
func (s *Service) Rename(ctx context.Context, id uuid.UUID, name string) (*Org, error) {
	ctx, span := tracer.Start(ctx, "org.Rename")
	defer span.End()

	// SECURITY: the scope must name the org being renamed, not whichever org the caller is active in.
	ctx = tenant.WithOrg(ctx, id)
	if err := authz.CanOrgWide(ctx, authz.MembersWrite); err != nil {
		return nil, err
	}
	o, err := s.store.ByID(ctx, id)
	if err != nil {
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "org.Rename: ByID", fmt.Errorf("org.Rename: ByID: %w", err), "org_id", id.String())
	}
	if o.System {
		return nil, &SystemProtectedError{Op: "rename", OrgID: id.String(), Resource: "org"}
	}
	if err := o.Rename(name); err != nil {
		return nil, err
	}
	if err := s.store.Save(ctx, o); err != nil {
		return nil, s.unexpected(ctx, "org.Rename: Save", fmt.Errorf("org.Rename: Save: %w", err), "org_id", id.String())
	}
	return o, nil
}

// AddMember creates a membership row idempotently.
func (s *Service) AddMember(ctx context.Context, orgID, userID uuid.UUID, role Role) (*Membership, error) {
	ctx, span := tracer.Start(ctx, "org.AddMember")
	defer span.End()

	if !role.IsValid() {
		return nil, &InvalidRoleError{Role: string(role)}
	}

	if _, err := s.store.MembershipOf(ctx, orgID, userID); err == nil {
		return nil, &MembershipExistsError{OrgID: orgID.String(), UserID: userID.String()}
	} else if !IsMembershipMissingError(err) && !IsNotFoundError(err) {
		return nil, s.unexpected(ctx, "org.AddMember: MembershipOf", fmt.Errorf("org.AddMember: MembershipOf: %w", err), "org_id", orgID.String(), "user_id", userID.String())
	}

	m, err := NewMembership(orgID, userID, role)
	if err != nil {
		return nil, err
	}
	if err := s.store.SaveMembership(ctx, m); err != nil {
		return nil, s.unexpected(ctx, "org.AddMember: SaveMembership", fmt.Errorf("org.AddMember: SaveMembership: %w", err), "org_id", orgID.String(), "user_id", userID.String())
	}
	return m, nil
}

// RemoveMember deletes a membership row.
func (s *Service) RemoveMember(ctx context.Context, orgID, userID uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "org.RemoveMember")
	defer span.End()

	// SECURITY: the scope must name orgID, not whichever org the caller is active in.
	ctx = tenant.WithOrg(ctx, orgID)
	if err := authz.CanOrgWide(ctx, authz.MembersWrite); err != nil {
		return err
	}
	if err := s.removable(ctx, orgID, userID); err != nil {
		return err
	}
	return s.uow(ctx, func(ctx context.Context) error {
		if err := s.store.LockOrg(ctx, orgID); err != nil {
			return s.unexpected(ctx, "org.RemoveMember: lock", err, "org_id", orgID.String())
		}
		if err := s.removable(ctx, orgID, userID); err != nil {
			return err
		}
		// SECURITY: hooks run in the unit of work after the locked check, so a refused removal revokes nothing and a failed revoke keeps the member.
		for _, fn := range s.onRemoved {
			if err := fn(ctx, orgID, userID); err != nil {
				return s.unexpected(ctx, "org.RemoveMember: hook", fmt.Errorf("org.RemoveMember: hook: %w", err), "org_id", orgID.String(), "user_id", userID.String())
			}
		}
		if err := s.store.RemoveMember(ctx, orgID, userID); err != nil {
			if IsMembershipMissingError(err) {
				return err
			}
			return s.unexpected(ctx, "org.RemoveMember", fmt.Errorf("org.RemoveMember: %w", err), "org_id", orgID.String(), "user_id", userID.String())
		}
		return nil
	})
}

// SECURITY: must run again under the org lock, or two concurrent removals can leave the org ownerless.
func (s *Service) removable(ctx context.Context, orgID, userID uuid.UUID) error {
	m, err := s.store.MembershipOf(ctx, orgID, userID)
	if err != nil {
		if IsMembershipMissingError(err) || IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, "org.RemoveMember: MembershipOf", fmt.Errorf("org.RemoveMember: MembershipOf: %w", err), "org_id", orgID.String(), "user_id", userID.String())
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	actorRole, held, _, err := s.actor(ctx, orgID, tc.UserID)
	if err != nil {
		return err
	}
	targetPerms, err := s.permissionsOf(ctx, m.Role, m.RoleID)
	if err != nil {
		return err
	}
	if refusal := RemovalRefusal(orgID, tc.UserID, userID, actorRole, m.Role, m.System, held, targetPerms); refusal != nil {
		return refusal
	}
	if m.Role == RoleOwner {
		return s.requireAnotherOwner(ctx, orgID, userID)
	}
	return nil
}

// SetRole gives userID a built-in role, or the custom role roleID, in orgID. SECURITY: the caller must hold every permission of the old and the new role, only a built-in owner makes or unmakes an owner, and the last owner stays.
func (s *Service) SetRole(ctx context.Context, orgID, userID uuid.UUID, role Role, roleID uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "org.SetRole")
	defer span.End()

	ctx = tenant.WithOrg(ctx, orgID)
	if err := authz.CanOrgWide(ctx, authz.MembersWrite); err != nil {
		return err
	}
	if !validGrant(role, roleID) {
		return &InvalidRoleError{Role: string(role)}
	}
	m, err := s.settable(ctx, orgID, userID, role, roleID)
	if err != nil || (m.Role == role && m.RoleID == roleID) {
		return err
	}
	return s.uow(ctx, func(ctx context.Context) error {
		if err := s.store.LockOrg(ctx, orgID); err != nil {
			return s.unexpected(ctx, "org.SetRole: lock", err, "org_id", orgID.String())
		}
		m, err := s.settable(ctx, orgID, userID, role, roleID)
		if err != nil || (m.Role == role && m.RoleID == roleID) {
			return err
		}
		// SECURITY: an owner always reaches every project, and the owner CHECK refuses the save otherwise, so the grant widens first in the same unit of work.
		if role == RoleOwner && !m.AllProjects {
			if err := s.store.SetProjectGrant(ctx, orgID, userID, ProjectGrant{All: true}); err != nil {
				return s.unexpected(ctx, "org.SetRole: widen grant", err, "org_id", orgID.String(), "user_id", userID.String())
			}
		}
		updated := *m
		updated.Role, updated.RoleID = role, roleID
		if err := s.store.SaveMembership(ctx, &updated); err != nil {
			return s.unexpected(ctx, "org.SetRole: save", err, "org_id", orgID.String(), "user_id", userID.String())
		}
		return nil
	})
}

// SECURITY: must run again under the org lock, or a concurrent change slips past the cover and last-owner checks.
func (s *Service) settable(ctx context.Context, orgID, userID uuid.UUID, next Role, nextID uuid.UUID) (*Membership, error) {
	m, err := s.store.MembershipOf(ctx, orgID, userID)
	if err != nil {
		if IsMembershipMissingError(err) || IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "org.SetRole: MembershipOf", err, "org_id", orgID.String(), "user_id", userID.String())
	}
	if m.System {
		return nil, &SystemProtectedError{Op: "set_role", OrgID: orgID.String(), UserID: userID.String(), Resource: "membership"}
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	actorRole, held, _, err := s.actor(ctx, orgID, tc.UserID)
	if err != nil {
		return nil, err
	}
	currentPerms, err := s.permissionsOf(ctx, m.Role, m.RoleID)
	if err != nil {
		return nil, err
	}
	nextPerms, err := s.permissionsOf(ctx, next, nextID)
	if err != nil {
		return nil, err
	}
	if refusal := RoleChangeRefusal(held, actorRole, m.Role, next, currentPerms, nextPerms); refusal != nil {
		return nil, refusal
	}
	if m.Role == RoleOwner && next != RoleOwner {
		if err := s.requireAnotherOwner(ctx, orgID, userID); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// RemovalRefusal reports why an actor holding held as actorRole may not remove the target membership holding targetPerms, or nil when removal is allowed. SECURITY: the service gate and the members view both consult this, so a hidden button and a refused post cannot drift apart.
func RemovalRefusal(orgID, actor, target uuid.UUID, actorRole, targetRole Role, system bool, held, targetPerms []authz.Permission) error {
	switch {
	case system:
		return &SystemProtectedError{Op: "remove_member", OrgID: orgID.String(), UserID: target.String(), Resource: "membership"}
	case actor == target:
		return &SelfRemovalError{OrgID: orgID.String(), UserID: target.String()}
	case targetRole == RoleOwner && actorRole != RoleOwner:
		return &OwnerRemovalError{OrgID: orgID.String(), UserID: target.String()}
	case !authz.Covers(held, targetPerms...):
		return &authz.EscalationError{Role: string(targetRole), Op: authz.EscalationRemove}
	}
	return nil
}

// RoleChangeRefusal returns an *authz.EscalationError when an actor holding held as actorRole may not move a member from current, holding currentPerms, to next, holding nextPerms, or nil.
func RoleChangeRefusal(held []authz.Permission, actorRole, current, next Role, currentPerms, nextPerms []authz.Permission) error {
	if !authz.Covers(held, currentPerms...) || !authz.Covers(held, nextPerms...) ||
		((current == RoleOwner || next == RoleOwner) && actorRole != RoleOwner) {
		return &authz.EscalationError{Role: string(next)}
	}
	return nil
}

// SECURITY: reads the actor's membership now, so a role lost since the request was bound grants nothing; only a member bound and still seated as a built-in owner acts as owner.
func (s *Service) actor(ctx context.Context, orgID, actorID uuid.UUID) (Role, []authz.Permission, ProjectGrant, error) {
	m, err := s.store.MembershipOf(ctx, orgID, actorID)
	if err != nil && !IsMembershipMissingError(err) && !IsNotFoundError(err) {
		return "", nil, ProjectGrant{}, s.unexpected(ctx, "org: actor membership", err, "org_id", orgID.String(), "user_id", actorID.String())
	}
	var held []authz.Permission
	if err == nil {
		perms, err := s.permissionsOf(ctx, m.Role, m.RoleID)
		if err != nil && !IsInvalidRoleError(err) {
			return "", nil, ProjectGrant{}, err
		}
		held = slices.DeleteFunc(authz.Held(ctx), func(p authz.Permission) bool { return !slices.Contains(perms, p) })
	}
	if !authz.Covers(held, authz.MembersWrite) {
		return "", nil, ProjectGrant{}, &authz.DeniedError{Permission: authz.MembersWrite, Org: session.PrincipalFrom(ctx).HeldOrg, Side: authz.SideRole}
	}
	actorRole := m.Role
	if actorRole != HeldRole(ctx, orgID) {
		actorRole = ""
	}
	return actorRole, held, ProjectGrant{All: m.AllProjects || m.Role == RoleOwner, ProjectIDs: m.ProjectIDs}, nil
}

func (s *Service) permissionsOf(ctx context.Context, r Role, roleID uuid.UUID) ([]authz.Permission, error) {
	if roleID == uuid.Nil {
		return authz.Builtin(string(r)), nil
	}
	if s.perms == nil {
		return nil, &InvalidRoleError{Role: string(r)}
	}
	perms, err := s.perms.PermissionsOf(ctx, string(r), roleID)
	if customrole.IsNotFoundError(err) {
		return nil, &InvalidRoleError{Role: string(r)}
	}
	if err != nil {
		return nil, s.unexpected(ctx, "org: role permissions", err, "role_id", roleID.String())
	}
	return perms, nil
}

// HeldRole returns the built-in role the principal on ctx is bound to in orgID, or "" when it is bound elsewhere.
func HeldRole(ctx context.Context, orgID uuid.UUID) Role {
	p := session.PrincipalFrom(ctx)
	if p.HeldOrgID != orgID {
		return ""
	}
	return Role(p.HeldRole)
}

func (s *Service) requireAnotherOwner(ctx context.Context, orgID, userID uuid.UUID) error {
	members, err := s.store.ListMembers(ctx, orgID)
	if err != nil {
		return s.unexpected(ctx, "org: count owners", err, "org_id", orgID.String())
	}
	for _, m := range members {
		if m.Role == RoleOwner && m.UserID != userID {
			return nil
		}
	}
	name := ""
	if o, err := s.store.ByID(ctx, orgID); err == nil {
		name = o.Name
	}
	return &authz.LastOwnerError{Org: name}
}

// SystemOrg returns the singleton org by its system flag, or a NotFoundError when none exists.
func (s *Service) SystemOrg(ctx context.Context) (*Org, error) {
	ctx, span := tracer.Start(ctx, "org.SystemOrg")
	defer span.End()
	o, err := s.store.SystemOrg(ctx)
	if err != nil {
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "org.SystemOrg", fmt.Errorf("org.SystemOrg: %w", err))
	}
	return o, nil
}

// BySlug looks up an org by its immutable slug.
func (s *Service) BySlug(ctx context.Context, slug string) (*Org, error) {
	ctx, span := tracer.Start(ctx, "org.BySlug")
	defer span.End()
	o, err := s.store.BySlug(ctx, slug)
	if err != nil {
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "org.BySlug", fmt.Errorf("org.BySlug: %w", err), "slug", slug)
	}
	return o, nil
}

// ByID looks up an org by its aggregate id.
func (s *Service) ByID(ctx context.Context, id uuid.UUID) (*Org, error) {
	ctx, span := tracer.Start(ctx, "org.ByID")
	defer span.End()
	o, err := s.store.ByID(ctx, id)
	if err != nil {
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "org.ByID", fmt.Errorf("org.ByID: %w", err), "org_id", id.String())
	}
	return o, nil
}

// MembershipOf returns the membership row for (orgID, userID), or a MembershipMissingError.
func (s *Service) MembershipOf(ctx context.Context, orgID, userID uuid.UUID) (*Membership, error) {
	ctx, span := tracer.Start(ctx, "org.MembershipOf")
	defer span.End()
	m, err := s.store.MembershipOf(ctx, orgID, userID)
	if err != nil {
		if IsMembershipMissingError(err) || IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "org.MembershipOf", fmt.Errorf("org.MembershipOf: %w", err), "org_id", orgID.String(), "user_id", userID.String())
	}
	return m, nil
}

// RequireMember refuses with *MembershipMissingError unless userID holds any membership in orgID. SECURITY: ctx must already carry the tenant scope.
func (s *Service) RequireMember(ctx context.Context, orgID, userID uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "org.RequireMember")
	defer span.End()
	if userID == uuid.Nil {
		return &MembershipMissingError{OrgID: orgID.String(), UserID: userID.String()}
	}
	if _, err := s.MembershipOf(ctx, orgID, userID); err != nil {
		if IsNotFoundError(err) {
			return &MembershipMissingError{OrgID: orgID.String(), UserID: userID.String()}
		}
		return err
	}
	return nil
}

// ListMembers returns every membership in the given org.
func (s *Service) ListMembers(ctx context.Context, orgID uuid.UUID) ([]*Membership, error) {
	ctx, span := tracer.Start(ctx, "org.ListMembers")
	defer span.End()

	ctx = tenant.WithOrg(ctx, orgID)
	if err := authz.Can(ctx, authz.MembersRead); err != nil {
		return nil, err
	}
	ms, err := s.store.ListMembers(ctx, orgID)
	if err != nil {
		return nil, s.unexpected(ctx, "org.ListMembers", fmt.Errorf("org.ListMembers: %w", err), "org_id", orgID.String())
	}
	return ms, nil
}

// ListMemberProfiles returns memberships joined with the member's identity fields (email, name).
func (s *Service) ListMemberProfiles(ctx context.Context, orgID uuid.UUID) ([]*MemberProfile, error) {
	ctx, span := tracer.Start(ctx, "org.ListMemberProfiles")
	defer span.End()

	ctx = tenant.WithOrg(ctx, orgID)
	if err := authz.Can(ctx, authz.MembersRead); err != nil {
		return nil, err
	}
	ps, err := s.store.ListMemberProfiles(ctx, orgID)
	if err != nil {
		return nil, s.unexpected(ctx, "org.ListMemberProfiles", fmt.Errorf("org.ListMemberProfiles: %w", err), "org_id", orgID.String())
	}
	return ps, nil
}

func scopeForBootstrap(ctx context.Context, ownerID uuid.UUID) (context.Context, uuid.UUID) {
	if tc, err := tenant.From(ctx); err == nil && tc.OrgID != uuid.Nil {
		return ctx, tc.OrgID
	}
	id := uuid.New()
	return tenant.Into(ctx, tenant.Context{OrgID: id, UserID: ownerID}), id
}

// SetProjectGrant sets the projects userID reaches in orgID. SECURITY: the caller must hold every permission of the target's role and reach every project it grants; an owner always reaches every project.
func (s *Service) SetProjectGrant(ctx context.Context, orgID, userID uuid.UUID, g ProjectGrant) error {
	ctx, span := tracer.Start(ctx, "org.SetProjectGrant")
	defer span.End()

	ctx = tenant.WithOrg(ctx, orgID)
	if err := authz.CanOrgWide(ctx, authz.MembersWrite); err != nil {
		return err
	}
	if err := g.Validate(); err != nil {
		return err
	}
	if err := s.grantable(ctx, orgID, userID, g); err != nil {
		return err
	}
	return s.uow(ctx, func(ctx context.Context) error {
		if err := s.store.LockOrg(ctx, orgID); err != nil {
			return s.unexpected(ctx, "org.SetProjectGrant: lock", err, "org_id", orgID.String())
		}
		if err := s.grantable(ctx, orgID, userID, g); err != nil {
			return err
		}
		if err := s.store.SetProjectGrant(ctx, orgID, userID, g); err != nil {
			if IsMembershipMissingError(err) {
				return err
			}
			return s.unexpected(ctx, "org.SetProjectGrant: save", err, "org_id", orgID.String(), "user_id", userID.String())
		}
		return nil
	})
}

// SECURITY: must run again under the org lock, or a concurrent change to the actor's or target's role or grant slips past the checks.
func (s *Service) grantable(ctx context.Context, orgID, userID uuid.UUID, g ProjectGrant) error {
	m, err := s.store.MembershipOf(ctx, orgID, userID)
	if err != nil {
		if IsMembershipMissingError(err) || IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, "org.SetProjectGrant: MembershipOf", err, "org_id", orgID.String(), "user_id", userID.String())
	}
	if m.System {
		return &SystemProtectedError{Op: "set_project_grant", OrgID: orgID.String(), UserID: userID.String(), Resource: "membership"}
	}
	if m.Role == RoleOwner {
		return &ProjectGrantConflictError{}
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	_, held, actorGrant, err := s.actor(ctx, orgID, tc.UserID)
	if err != nil {
		return err
	}
	targetPerms, err := s.permissionsOf(ctx, m.Role, m.RoleID)
	if err != nil {
		return err
	}
	if !authz.Covers(held, targetPerms...) {
		return &authz.EscalationError{Role: string(m.Role)}
	}
	if err := s.inOrg(ctx, orgID, g); err != nil {
		return err
	}
	return g.Within(actorGrant)
}

func (s *Service) inOrg(ctx context.Context, orgID uuid.UUID, g ProjectGrant) error {
	if len(g.ProjectIDs) == 0 {
		return nil
	}
	if s.projects == nil {
		return &ProjectGrantEscalationError{ProjectID: g.ProjectIDs[0]}
	}
	ids, err := s.projects.ProjectIDs(ctx, orgID)
	if err != nil {
		return s.unexpected(ctx, "org.SetProjectGrant: projects", err, "org_id", orgID.String())
	}
	return g.InOrg(ids)
}
