package fakes

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"altalune.id/template/internal/org"
)

// Org is an in-memory org.Store.
type Org struct {
	mu          sync.Mutex
	orgs        map[uuid.UUID]*org.Org
	memberships []*org.Membership
	userLookup  map[uuid.UUID]func() (string, string)
}

// NewOrg returns an empty in-memory org.Store.
func NewOrg() *Org {
	return &Org{
		orgs:       make(map[uuid.UUID]*org.Org),
		userLookup: map[uuid.UUID]func() (string, string){},
	}
}

var _ org.Store = (*Org)(nil)

func (r *Org) Save(_ context.Context, o *org.Org) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, existing := range r.orgs {
		if existing.Slug == o.Slug && id != o.ID {
			return &org.AlreadyExistsError{Slug: o.Slug}
		}
	}
	c := *o
	r.orgs[o.ID] = &c
	return nil
}

func (r *Org) BySlug(_ context.Context, slug string) (*org.Org, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range r.orgs {
		if o.Slug == slug {
			c := *o
			return &c, nil
		}
	}
	return nil, &org.NotFoundError{Slug: slug}
}

func (r *Org) ByID(_ context.Context, id uuid.UUID) (*org.Org, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.orgs[id]
	if !ok {
		return nil, &org.NotFoundError{ID: id.String()}
	}
	c := *o
	return &c, nil
}

func (r *Org) List(_ context.Context, userID uuid.UUID) ([]*org.Org, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*org.Org
	for _, m := range r.memberships {
		if m.UserID == userID {
			if o, ok := r.orgs[m.OrgID]; ok {
				c := *o
				out = append(out, &c)
			}
		}
	}
	return out, nil
}

func (r *Org) SaveMembership(_ context.Context, m *org.Membership) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.memberships {
		if x.OrgID == m.OrgID && x.UserID == m.UserID {
			c := *m
			r.memberships[i] = &c
			return nil
		}
	}
	c := *m
	r.memberships = append(r.memberships, &c)
	return nil
}

func (r *Org) MembershipOf(_ context.Context, orgID, userID uuid.UUID) (*org.Membership, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.memberships {
		if x.OrgID == orgID && x.UserID == userID {
			c := *x
			return &c, nil
		}
	}
	return nil, &org.MembershipMissingError{OrgID: orgID.String(), UserID: userID.String()}
}

func (r *Org) ListMembers(_ context.Context, orgID uuid.UUID) ([]*org.Membership, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*org.Membership
	for _, x := range r.memberships {
		if x.OrgID == orgID {
			c := *x
			out = append(out, &c)
		}
	}
	return out, nil
}

func (r *Org) ListMemberProfiles(_ context.Context, orgID uuid.UUID) ([]*org.MemberProfile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*org.MemberProfile, 0, len(r.memberships))
	for _, x := range r.memberships {
		if x.OrgID != orgID {
			continue
		}
		var email, name string
		if lookup, ok := r.userLookup[x.UserID]; ok {
			email, name = lookup()
		}
		out = append(out, &org.MemberProfile{
			UserID:    x.UserID,
			Email:     email,
			Name:      name,
			Role:      x.Role,
			CreatedAt: x.CreatedAt,
			System:    x.System,
		})
	}
	return out, nil
}

// SetUser registers an email/name for a user id, so ListMemberProfiles can join.
func (r *Org) SetUser(id uuid.UUID, email, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.userLookup == nil {
		r.userLookup = map[uuid.UUID]func() (string, string){}
	}
	r.userLookup[id] = func() (string, string) { return email, name }
}

func (r *Org) RemoveMember(_ context.Context, orgID, userID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.memberships {
		if x.OrgID == orgID && x.UserID == userID {
			r.memberships = append(r.memberships[:i], r.memberships[i+1:]...)
			return nil
		}
	}
	return &org.MembershipMissingError{OrgID: orgID.String(), UserID: userID.String()}
}
