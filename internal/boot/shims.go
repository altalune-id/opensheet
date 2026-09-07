package boot

import (
	"context"

	"github.com/google/uuid"

	"altalune.id/template/internal/auth"
	"altalune.id/template/internal/invite"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/user"
)

type userStoreForInvite struct{ store user.Store }

func (s userStoreForInvite) ByEmail(ctx context.Context, email string) (*invite.UserRef, error) {
	u, err := s.store.ByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	return &invite.UserRef{ID: u.ID, Email: u.Email, Name: u.Name, Source: u.Source}, nil
}

func (s userStoreForInvite) Save(ctx context.Context, u *invite.UserRef) error {
	return s.store.Save(ctx, &user.User{
		ID:     u.ID,
		Email:  u.Email,
		Name:   u.Name,
		Source: u.Source,
	})
}

type orgStoreForInvite struct{ store org.Store }

func (s orgStoreForInvite) ByID(ctx context.Context, id uuid.UUID) (*invite.OrgRef, error) {
	o, err := s.store.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &invite.OrgRef{ID: o.ID, Slug: o.Slug, Name: o.Name}, nil
}

func (s orgStoreForInvite) SaveMembership(ctx context.Context, m *invite.MembershipRef) error {
	return s.store.SaveMembership(ctx, &org.Membership{OrgID: m.OrgID, UserID: m.UserID, Role: org.Role(m.Role)})
}

func (s orgStoreForInvite) MembershipOf(ctx context.Context, orgID, userID uuid.UUID) (*invite.MembershipRef, error) {
	m, err := s.store.MembershipOf(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	return &invite.MembershipRef{OrgID: m.OrgID, UserID: m.UserID, Role: invite.Role(m.Role)}, nil
}

type userStoreForAuth struct{ store user.Store }

func (s userStoreForAuth) ByEmail(ctx context.Context, email string) (*auth.UserRef, error) {
	u, err := s.store.ByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	return &auth.UserRef{
		ID:              u.ID,
		Email:           u.Email,
		Name:            u.Name,
		Source:          u.Source,
		IsAdmin:         u.IsAdmin,
		Locale:          u.Locale,
		PasswordHash:    u.PasswordHash,
		TermsAcceptedAt: u.TermsAcceptedAt,
	}, nil
}

func (s userStoreForAuth) Save(ctx context.Context, u *auth.UserRef) error {
	return s.store.Save(ctx, &user.User{
		ID:           u.ID,
		Email:        u.Email,
		Name:         u.Name,
		Source:       u.Source,
		IsAdmin:      u.IsAdmin,
		PasswordHash: u.PasswordHash,
	})
}

type orgStoreForOnboard struct{ store org.Store }

func (s orgStoreForOnboard) BySlug(ctx context.Context, slug string) (*user.OrgRef, error) {
	o, err := s.store.BySlug(ctx, slug)
	if err != nil {
		if org.IsNotFoundError(err) {
			return nil, &user.SingletonOrgMissingError{Slug: slug}
		}
		return nil, err
	}
	return &user.OrgRef{ID: o.ID, Slug: o.Slug, Name: o.Name, OwnerID: o.OwnerID, CreatedAt: o.CreatedAt}, nil
}

func (s orgStoreForOnboard) Save(ctx context.Context, o *user.OrgRef) error {
	return s.store.Save(ctx, &org.Org{ID: o.ID, Slug: o.Slug, Name: o.Name, OwnerID: o.OwnerID, CreatedAt: o.CreatedAt})
}

func (s orgStoreForOnboard) ListForUser(ctx context.Context, userID uuid.UUID) ([]*user.OrgRef, error) {
	orgs, err := s.store.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]*user.OrgRef, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, &user.OrgRef{ID: o.ID, Slug: o.Slug, Name: o.Name, OwnerID: o.OwnerID, CreatedAt: o.CreatedAt})
	}
	return out, nil
}

func (s orgStoreForOnboard) MembershipOf(ctx context.Context, orgID, userID uuid.UUID) (*user.MembershipRef, error) {
	m, err := s.store.MembershipOf(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	return &user.MembershipRef{OrgID: m.OrgID, UserID: m.UserID, Role: string(m.Role)}, nil
}

func (s orgStoreForOnboard) SaveMembership(ctx context.Context, m *user.MembershipRef) error {
	return s.store.SaveMembership(ctx, &org.Membership{OrgID: m.OrgID, UserID: m.UserID, Role: org.Role(m.Role)})
}

type projectStoreForOnboard struct{ store project.Store }

func (s projectStoreForOnboard) BySlug(ctx context.Context, orgID uuid.UUID, slug string) (*user.ProjectRef, error) {
	p, err := s.store.BySlug(ctx, orgID, slug)
	if err != nil {
		return nil, err
	}
	return &user.ProjectRef{ID: p.ID, OrgID: p.OrgID, Slug: p.Slug, Name: p.Name, CreatedAt: p.CreatedAt}, nil
}

func (s projectStoreForOnboard) Save(ctx context.Context, p *user.ProjectRef) error {
	return s.store.Save(ctx, &project.Project{ID: p.ID, OrgID: p.OrgID, Slug: p.Slug, Name: p.Name, CreatedAt: p.CreatedAt})
}

func (s projectStoreForOnboard) ListByOrg(ctx context.Context, orgID uuid.UUID) ([]*user.ProjectRef, error) {
	projects, err := s.store.List(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]*user.ProjectRef, 0, len(projects))
	for _, p := range projects {
		out = append(out, &user.ProjectRef{ID: p.ID, OrgID: p.OrgID, Slug: p.Slug, Name: p.Name, CreatedAt: p.CreatedAt})
	}
	return out, nil
}

type inviteStoreForOnboard struct{ store invite.Store }

func (s inviteStoreForOnboard) ListByOrg(ctx context.Context, orgID uuid.UUID) ([]*user.InviteRef, error) {
	invs, err := s.store.ListPending(ctx, orgID)
	if err != nil {
		return nil, err
	}
	return toUserInvites(invs), nil
}

func (s inviteStoreForOnboard) ListPendingForEmail(ctx context.Context, email string) ([]*user.InviteRef, error) {
	invs, err := s.store.FindPendingForEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	return toUserInvites(invs), nil
}

func (s inviteStoreForOnboard) Save(ctx context.Context, i *user.InviteRef) error {
	existing, err := s.store.ByID(ctx, i.ID)
	if err != nil {
		return err
	}
	existing.UsedAt = i.AcceptedAt
	return s.store.Save(ctx, existing)
}

func toUserInvites(invs []*invite.Invite) []*user.InviteRef {
	out := make([]*user.InviteRef, 0, len(invs))
	for _, inv := range invs {
		out = append(out, &user.InviteRef{
			ID:         inv.ID,
			OrgID:      inv.OrgID,
			Email:      inv.Email,
			Role:       string(inv.Role),
			ExpiresAt:  inv.ExpiresAt,
			AcceptedAt: inv.UsedAt,
		})
	}
	return out
}
