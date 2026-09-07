package org

import (
	"context"

	"github.com/google/uuid"
)

// Store persists Org aggregates and their memberships.
type Store interface {
	Save(ctx context.Context, o *Org) error
	BySlug(ctx context.Context, slug string) (*Org, error)
	ByID(ctx context.Context, id uuid.UUID) (*Org, error)
	List(ctx context.Context, userID uuid.UUID) ([]*Org, error)

	SaveMembership(ctx context.Context, m *Membership) error
	MembershipOf(ctx context.Context, orgID, userID uuid.UUID) (*Membership, error)
	ListMembers(ctx context.Context, orgID uuid.UUID) ([]*Membership, error)
	ListMemberProfiles(ctx context.Context, orgID uuid.UUID) ([]*MemberProfile, error)
	RemoveMember(ctx context.Context, orgID, userID uuid.UUID) error
}
