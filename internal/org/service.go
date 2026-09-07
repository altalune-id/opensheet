package org

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform/capabilities"
	"altalune.id/template/internal/platform/tenant"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/template/internal/org")

// Service orchestrates org and membership use cases.
type Service struct {
	store      Store
	caps       capabilities.Capabilities
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
}

// NewService wires the Service.
func NewService(store Store, caps capabilities.Capabilities, log *slog.Logger, unexpected apperror.UnexpectedFunc) *Service {
	return &Service{store: store, caps: caps, log: log, unexpected: unexpected}
}

// BootstrapSingleton idempotently ensures the fixed selfhosted org exists and carries an owner membership for ownerID.
// The org and owner membership are stamped System=true so they cannot be renamed or removed.
func (s *Service) BootstrapSingleton(ctx context.Context, slug, name string, ownerID uuid.UUID) (*Org, error) {
	ctx, span := tracer.Start(ctx, "org.BootstrapSingleton")
	defer span.End()

	// NOTE: bootstrap runs before any tenant exists, but the postgres store needs a tenant scope and
	// the orgs RLS policy is `id = current_setting('app.current_org_id')` — so scope to the org this
	// call will create, and reuse that id when inserting so the row satisfies its own policy.
	ctx, orgID := scopeForBootstrap(ctx, ownerID)

	existing, err := s.store.BySlug(ctx, slug)
	if err == nil {
		if !existing.System {
			existing.System = true
			if sErr := s.store.Save(ctx, existing); sErr != nil {
				return nil, s.unexpected(ctx, "org.BootstrapSingleton: Save", sErr, "org_id", existing.ID.String())
			}
		}
		if m, mErr := s.store.MembershipOf(ctx, existing.ID, ownerID); mErr == nil {
			if !m.System {
				m.System = true
				if sErr := s.store.SaveMembership(ctx, m); sErr != nil {
					return nil, s.unexpected(ctx, "org.BootstrapSingleton: SaveMembership", sErr, "org_id", existing.ID.String())
				}
			}
			return existing, nil
		} else if !IsMembershipMissingError(mErr) && !IsNotFoundError(mErr) {
			return nil, s.unexpected(ctx, "org.BootstrapSingleton: MembershipOf", mErr, "org_id", existing.ID.String())
		}
		m, mErr := NewMembership(existing.ID, ownerID, RoleOwner)
		if mErr != nil {
			return nil, mErr
		}
		m.System = true
		if mErr := s.store.SaveMembership(ctx, m); mErr != nil {
			return nil, s.unexpected(ctx, "org.BootstrapSingleton: SaveMembership", mErr, "org_id", existing.ID.String())
		}
		return existing, nil
	}
	if !IsNotFoundError(err) {
		return nil, s.unexpected(ctx, "org.BootstrapSingleton: BySlug", err, "slug", slug)
	}

	o, err := NewOrg(slug, name, ownerID)
	if err != nil {
		return nil, err
	}
	o.ID = orgID
	o.System = true
	if err := s.store.Save(ctx, o); err != nil {
		if IsAlreadyExistsError(err) {
			// NOTE: the slug exists but the lookup above could not see it — under RLS that means the row
			// belongs to a different org id, so report it plainly instead of a misleading not-found.
			return nil, s.unexpected(ctx, "org.BootstrapSingleton: Save", &UnreadableExistingOrgError{Slug: slug}, "slug", slug)
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

// Create constructs a new org and enrols the owner as an owner-role member.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*Org, error) {
	ctx, span := tracer.Start(ctx, "org.Create")
	defer span.End()

	if !s.caps.OrgCreation {
		return nil, &CreationDisabledError{}
	}

	existing, err := s.store.BySlug(ctx, req.Slug)
	switch {
	case err == nil && existing != nil:
		return nil, &AlreadyExistsError{Slug: req.Slug}
	case err != nil && !IsNotFoundError(err):
		return nil, s.unexpected(ctx, "org.Create: BySlug", fmt.Errorf("org.Create: BySlug: %w", err), "slug", req.Slug)
	}

	o, err := NewOrg(req.Slug, req.Name, req.OwnerID)
	if err != nil {
		return nil, err
	}
	// SECURITY: orgs and memberships are scoped by the org's own id, so the new row's scope is the only one RLS accepts — never the caller's active org.
	ctx = tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: req.OwnerID})
	if err := s.store.Save(ctx, o); err != nil {
		if IsAlreadyExistsError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "org.Create: Save", fmt.Errorf("org.Create: Save: %w", err), "slug", req.Slug)
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
	if err := s.store.RemoveMember(ctx, orgID, userID); err != nil {
		if IsMembershipMissingError(err) {
			return err
		}
		return s.unexpected(ctx, "org.RemoveMember", fmt.Errorf("org.RemoveMember: %w", err), "org_id", orgID.String(), "user_id", userID.String())
	}
	return nil
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

// scopeForBootstrap returns ctx carrying a tenant scope and the org id that scope names, minting both when absent.
func scopeForBootstrap(ctx context.Context, ownerID uuid.UUID) (context.Context, uuid.UUID) {
	if tc, err := tenant.From(ctx); err == nil && tc.OrgID != uuid.Nil {
		return ctx, tc.OrgID
	}
	id := uuid.New()
	return tenant.Into(ctx, tenant.Context{OrgID: id, UserID: ownerID}), id
}
