package org

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/tenant"
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
	onRemoved  []MemberRemovedFunc
}

// MemberRemovedFunc reacts to a member leaving an org, such as revoking what the member held there.
type MemberRemovedFunc func(ctx context.Context, orgID, userID uuid.UUID) error

// OnMemberRemoved registers fn to run on every RemoveMember, before the membership is deleted. NOTE: register at boot, before serving; the list is not guarded for concurrent registration.
func (s *Service) OnMemberRemoved(fn MemberRemovedFunc) { s.onRemoved = append(s.onRemoved, fn) }

// NewService wires the Service.
func NewService(store Store, caps capabilities.Capabilities, log *slog.Logger, unexpected apperror.UnexpectedFunc) *Service {
	return &Service{store: store, caps: caps, log: log, unexpected: unexpected}
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
	// Membership in id is the caller's authorization and is checked before this call.
	ctx = tenant.WithOrg(ctx, id)
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
	// Membership in orgID is the caller's authorization and is checked before this call.
	ctx = tenant.WithOrg(ctx, orgID)
	m, err := s.store.MembershipOf(ctx, orgID, userID)
	if err != nil {
		if IsMembershipMissingError(err) || IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, "org.RemoveMember: MembershipOf", fmt.Errorf("org.RemoveMember: MembershipOf: %w", err), "org_id", orgID.String(), "user_id", userID.String())
	}
	// SECURITY: refused here rather than in the handler so every transport — web, API, CLI — is gated.
	tc, tErr := tenant.From(ctx)
	if tErr != nil {
		return tErr
	}
	actor, aErr := s.store.MembershipOf(ctx, orgID, tc.UserID)
	if aErr != nil {
		return aErr
	}
	if refusal := RemovalRefusal(orgID, tc.UserID, userID, actor.Role, m.Role, m.System); refusal != nil {
		return refusal
	}
	// SECURITY: hooks run before the delete, so a failed revoke leaves the member in place and a retry can finish it.
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

// IsManager reports whether userID holds an owner or admin membership in orgID. SECURITY: ctx must already carry the tenant scope.
func (s *Service) IsManager(ctx context.Context, orgID, userID uuid.UUID) (bool, error) {
	ctx, span := tracer.Start(ctx, "org.IsManager")
	defer span.End()
	if userID == uuid.Nil {
		return false, nil
	}
	m, err := s.MembershipOf(ctx, orgID, userID)
	if IsMembershipMissingError(err) || IsNotFoundError(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return m.Role.CanManage(), nil
}

// RequireManager refuses with *NotManagerError unless userID is an owner or admin of orgID; a machine principal is always refused. SECURITY: ctx must already carry the tenant scope.
func (s *Service) RequireManager(ctx context.Context, orgID, userID uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "org.RequireManager")
	defer span.End()
	ok, err := s.IsManager(ctx, orgID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return &NotManagerError{OrgID: orgID.String(), UserID: userID.String()}
	}
	return nil
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
