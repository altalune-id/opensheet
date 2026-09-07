package user

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform/tenant"
)

const (
	// PolicyModeSelfhosted binds every user to the singleton org.
	PolicyModeSelfhosted = "selfhosted"
	// PolicyModeCloud routes uninvited users to /signup/complete instead of creating a personal org.
	PolicyModeCloud = "cloud"
)

// Policy carries the deployment-mode inputs OnboardWorkflow needs.
type Policy struct {
	Mode             string
	SingletonOrgSlug string
}

type orgStore interface {
	BySlug(ctx context.Context, slug string) (*OrgRef, error)
	Save(ctx context.Context, o *OrgRef) error
	ListForUser(ctx context.Context, userID uuid.UUID) ([]*OrgRef, error)
	MembershipOf(ctx context.Context, orgID, userID uuid.UUID) (*MembershipRef, error)
	SaveMembership(ctx context.Context, m *MembershipRef) error
}

type projectStore interface {
	BySlug(ctx context.Context, orgID uuid.UUID, slug string) (*ProjectRef, error)
	Save(ctx context.Context, p *ProjectRef) error
	ListByOrg(ctx context.Context, orgID uuid.UUID) ([]*ProjectRef, error)
}

type inviteStore interface {
	ListByOrg(ctx context.Context, orgID uuid.UUID) ([]*InviteRef, error)
	ListPendingForEmail(ctx context.Context, email string) ([]*InviteRef, error)
	Save(ctx context.Context, inv *InviteRef) error
}

// OrgRef is the projection of the org aggregate consumed by OnboardWorkflow.
type OrgRef struct {
	ID        uuid.UUID
	Slug      string
	Name      string
	OwnerID   uuid.UUID
	CreatedAt time.Time
}

// MembershipRef is the projection of a membership record.
type MembershipRef struct {
	OrgID  uuid.UUID
	UserID uuid.UUID
	Role   string
}

// ProjectRef is the projection of a project record.
type ProjectRef struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	Slug      string
	Name      string
	CreatedAt time.Time
}

// InviteRef is the projection of an invite record.
type InviteRef struct {
	ID         uuid.UUID
	OrgID      uuid.UUID
	Email      string
	Role       string
	ExpiresAt  time.Time
	AcceptedAt *time.Time
}

// OnboardResult reports the org and project the caller should switch into.
type OnboardResult struct {
	OrgID     uuid.UUID
	ProjectID uuid.UUID
}

// OnboardWorkflow resolves the active org (+ project) for a freshly authenticated user.
type OnboardWorkflow struct {
	users      Store
	orgs       orgStore
	projects   projectStore
	invites    inviteStore
	policy     Policy
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
	now        func() time.Time
}

// NewOnboardWorkflow constructs an OnboardWorkflow with typed dependencies.
func NewOnboardWorkflow(
	users Store,
	orgs orgStore,
	projects projectStore,
	invites inviteStore,
	policy Policy,
	log *slog.Logger,
	unexpected apperror.UnexpectedFunc,
) *OnboardWorkflow {
	return &OnboardWorkflow{
		users:      users,
		orgs:       orgs,
		projects:   projects,
		invites:    invites,
		policy:     policy,
		log:        log,
		unexpected: unexpected,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// Onboard resolves (orgID, projectID) for the given user under the workflow's policy.
func (w *OnboardWorkflow) Onboard(ctx context.Context, userID uuid.UUID, userEmail string) (OnboardResult, error) {
	ctx, span := tracer.Start(ctx, "user.Onboard")
	defer span.End()
	switch w.policy.Mode {
	case PolicyModeSelfhosted:
		return w.onboardSelfhosted(ctx, userID, userEmail)
	case PolicyModeCloud:
		return w.onboardCloud(ctx, userID, userEmail)
	default:
		return OnboardResult{}, fmt.Errorf("user: unknown policy mode %q", w.policy.Mode)
	}
}

func (w *OnboardWorkflow) onboardSelfhosted(ctx context.Context, userID uuid.UUID, userEmail string) (OnboardResult, error) {
	slug := w.policy.SingletonOrgSlug
	o, err := w.orgs.BySlug(ctx, slug)
	if err != nil {
		if IsSingletonOrgMissingError(err) {
			return OnboardResult{}, &SignupRequiredError{UserID: userID.String(), Email: userEmail}
		}
		return OnboardResult{}, w.unexpected(ctx, "user.Onboard: singleton org lookup", err, slog.String("slug", slug))
	}

	if _, mErr := w.orgs.MembershipOf(tenant.WithOrg(ctx, o.ID), o.ID, userID); mErr == nil {
		projectID, err := w.pickProject(ctx, o.ID)
		if err != nil {
			return OnboardResult{}, err
		}
		return OnboardResult{OrgID: o.ID, ProjectID: projectID}, nil
	}

	inv, err := w.findPendingInvite(ctx, o.ID, userEmail)
	if err != nil {
		return OnboardResult{}, err
	}
	if inv == nil {
		return OnboardResult{}, &NotInvitedError{Email: userEmail}
	}

	now := w.now()
	inv.AcceptedAt = &now
	if err := w.invites.Save(tenant.WithOrg(ctx, inv.OrgID), inv); err != nil {
		return OnboardResult{}, w.unexpected(ctx, "user.Onboard: accept invite", err)
	}
	m := &MembershipRef{OrgID: o.ID, UserID: userID, Role: inv.Role}
	if err := w.orgs.SaveMembership(tenant.WithOrg(ctx, m.OrgID), m); err != nil {
		return OnboardResult{}, w.unexpected(ctx, "user.Onboard: save membership", err)
	}
	projectID, err := w.pickProject(ctx, o.ID)
	if err != nil {
		return OnboardResult{}, err
	}
	return OnboardResult{OrgID: o.ID, ProjectID: projectID}, nil
}

func (w *OnboardWorkflow) onboardCloud(ctx context.Context, userID uuid.UUID, userEmail string) (OnboardResult, error) {
	memberships, err := w.orgs.ListForUser(ctx, userID)
	if err != nil {
		return OnboardResult{}, w.unexpected(ctx, "user.Onboard: list memberships", err)
	}
	if len(memberships) > 0 {
		projectID, err := w.pickProject(ctx, memberships[0].ID)
		if err != nil {
			return OnboardResult{}, err
		}
		return OnboardResult{OrgID: memberships[0].ID, ProjectID: projectID}, nil
	}
	inv, err := w.findAnyPendingInvite(ctx, userEmail)
	if err != nil {
		return OnboardResult{}, err
	}
	if inv == nil {
		return OnboardResult{}, &SignupRequiredError{UserID: userID.String(), Email: userEmail}
	}
	now := w.now()
	inv.AcceptedAt = &now
	if err := w.invites.Save(tenant.WithOrg(ctx, inv.OrgID), inv); err != nil {
		return OnboardResult{}, w.unexpected(ctx, "user.Onboard: accept invite", err)
	}
	m := &MembershipRef{OrgID: inv.OrgID, UserID: userID, Role: inv.Role}
	if err := w.orgs.SaveMembership(tenant.WithOrg(ctx, m.OrgID), m); err != nil {
		return OnboardResult{}, w.unexpected(ctx, "user.Onboard: save membership", err)
	}
	projectID, err := w.pickProject(ctx, inv.OrgID)
	if err != nil {
		return OnboardResult{}, err
	}
	return OnboardResult{OrgID: inv.OrgID, ProjectID: projectID}, nil
}

func (w *OnboardWorkflow) findAnyPendingInvite(ctx context.Context, userEmail string) (*InviteRef, error) {
	e := strings.ToLower(strings.TrimSpace(userEmail))
	list, err := w.invites.ListPendingForEmail(ctx, e)
	if err != nil {
		return nil, w.unexpected(ctx, "user.Onboard: list invites for email", err)
	}
	now := w.now()
	for _, inv := range list {
		if inv.AcceptedAt != nil {
			continue
		}
		if now.After(inv.ExpiresAt) {
			continue
		}
		return inv, nil
	}
	return nil, nil
}

// SECURITY: the caller is mid-login and holds no tenant scope yet, so bind to the org being joined.
func (w *OnboardWorkflow) pickProject(ctx context.Context, orgID uuid.UUID) (uuid.UUID, error) {
	list, err := w.projects.ListByOrg(tenant.WithOrg(ctx, orgID), orgID)
	if err != nil {
		return uuid.Nil, w.unexpected(ctx, "user.Onboard: list projects", err)
	}
	if len(list) == 0 {
		return uuid.Nil, nil
	}
	return list[0].ID, nil
}

// SECURITY: as pickProject — no tenant scope exists until the org is chosen.
func (w *OnboardWorkflow) findPendingInvite(ctx context.Context, orgID uuid.UUID, userEmail string) (*InviteRef, error) {
	list, err := w.invites.ListByOrg(tenant.WithOrg(ctx, orgID), orgID)
	if err != nil {
		return nil, w.unexpected(ctx, "user.Onboard: list invites", err)
	}
	e := strings.ToLower(strings.TrimSpace(userEmail))
	now := w.now()
	for _, inv := range list {
		if inv.AcceptedAt != nil {
			continue
		}
		if now.After(inv.ExpiresAt) {
			continue
		}
		if strings.EqualFold(inv.Email, e) {
			return inv, nil
		}
	}
	return nil, nil
}

// SlugFromEmail derives a URL-safe slug from an email address, falling back to fallback when empty.
func SlugFromEmail(email, fallback string) string {
	local := email
	if at := strings.IndexByte(email, '@'); at > 0 {
		local = email[:at]
	}
	local = strings.ToLower(strings.TrimSpace(local))
	var b strings.Builder
	for _, r := range local {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '_' || r == '-' || r == '+':
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return fallback
	}
	return s
}
