package fakes

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/role"
)

// Role is an in-memory role.Store that honors ifVersion, the per-org name index and held roles, and never filters by the org on ctx.
type Role struct {
	mu   sync.Mutex
	rows map[uuid.UUID]*role.Role
	held map[uuid.UUID]bool
}

// NewRole returns an empty in-memory role.Store.
func NewRole() *Role {
	return &Role{rows: map[uuid.UUID]*role.Role{}, held: map[uuid.UUID]bool{}}
}

var _ role.Store = (*Role)(nil)

// Hold marks id as held by a member, so Delete refuses it.
func (f *Role) Hold(id uuid.UUID) { f.mu.Lock(); f.held[id] = true; f.mu.Unlock() }

// Release clears a Hold.
func (f *Role) Release(id uuid.UUID) { f.mu.Lock(); delete(f.held, id); f.mu.Unlock() }

// Save inserts r, or updates it when ifVersion matches; ifVersion 0 updates unconditionally.
func (f *Role) Save(_ context.Context, r *role.Role, ifVersion int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, other := range f.rows {
		if id != r.ID && other.OrgID == r.OrgID && strings.EqualFold(other.Name, r.Name) {
			return &role.AlreadyExistsError{Name: r.Name}
		}
	}
	c := cloneRole(r)
	if cur, ok := f.rows[r.ID]; ok {
		if ifVersion != 0 && cur.Version != ifVersion {
			return &role.StaleVersionError{Want: ifVersion, Got: cur.Version}
		}
		c.Version = cur.Version + 1
		c.CreatedAt = cur.CreatedAt
	}
	f.rows[r.ID] = c
	return nil
}

// ByID returns the role with id.
func (f *Role) ByID(_ context.Context, id uuid.UUID) (*role.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[id]
	if !ok {
		return nil, &role.NotFoundError{ID: id.String()}
	}
	return cloneRole(r), nil
}

// List returns every role by name.
func (f *Role) List(context.Context) ([]*role.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*role.Role, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, cloneRole(r))
	}
	slices.SortFunc(out, func(a, b *role.Role) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out, nil
}

// Delete removes the role unless it is held.
func (f *Role) Delete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[id]; !ok {
		return &role.NotFoundError{ID: id.String()}
	}
	if f.held[id] {
		return &role.InUseError{ID: id.String()}
	}
	delete(f.rows, id)
	return nil
}

// HeldBy reports 1 for every stored role marked by Hold.
func (f *Role) HeldBy(context.Context) (map[uuid.UUID]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[uuid.UUID]int{}
	for id := range f.held {
		if _, ok := f.rows[id]; ok {
			out[id] = 1
		}
	}
	return out, nil
}

// Permissions returns the permissions of roleID.
func (f *Role) Permissions(ctx context.Context, roleID uuid.UUID) ([]role.Permission, error) {
	r, err := f.ByID(ctx, roleID)
	if err != nil {
		return nil, err
	}
	return r.Permissions, nil
}

func cloneRole(r *role.Role) *role.Role {
	c := *r
	c.Permissions = slices.Clone(r.Permissions)
	return &c
}
