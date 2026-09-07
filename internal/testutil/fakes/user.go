package fakes

import (
	"context"
	"strings"
	"sync"

	"github.com/google/uuid"

	"altalune.id/template/internal/user"
)

// User is an in-memory user.Store for tests.
type User struct {
	mu    sync.Mutex
	byID  map[uuid.UUID]*user.User
	byMap map[string]uuid.UUID
	// SaveErr, if non-nil, is returned from Save (once, unless StickyError is true).
	SaveErr     error
	ByIDErr     error
	ByEmailErr  error
	StickyError bool
}

var _ user.Store = (*User)(nil)

// NewUser builds an empty fake user.Store.
func NewUser() *User {
	return &User{
		byID:  map[uuid.UUID]*user.User{},
		byMap: map[string]uuid.UUID{},
	}
}

// Save stores a copy under both id and email.
func (f *User) Save(_ context.Context, u *user.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SaveErr != nil {
		err := f.SaveErr
		if !f.StickyError {
			f.SaveErr = nil
		}
		return err
	}
	if u == nil {
		return nil
	}
	email := strings.ToLower(strings.TrimSpace(u.Email))
	if existing, ok := f.byMap[email]; ok && existing != u.ID {
		return &user.AlreadyExistsError{Field: "email", Value: u.Email}
	}
	cp := *u
	f.byID[u.ID] = &cp
	f.byMap[email] = u.ID
	return nil
}

// ByID returns a copy of the user if present.
func (f *User) ByID(_ context.Context, id uuid.UUID) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ByIDErr != nil {
		err := f.ByIDErr
		if !f.StickyError {
			f.ByIDErr = nil
		}
		return nil, err
	}
	u, ok := f.byID[id]
	if !ok {
		return nil, &user.NotFoundError{ID: id.String()}
	}
	cp := *u
	return &cp, nil
}

// ByEmail returns a copy of the user if present.
func (f *User) ByEmail(_ context.Context, email string) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ByEmailErr != nil {
		err := f.ByEmailErr
		if !f.StickyError {
			f.ByEmailErr = nil
		}
		return nil, err
	}
	id, ok := f.byMap[strings.ToLower(strings.TrimSpace(email))]
	if !ok {
		return nil, &user.NotFoundError{Email: email}
	}
	cp := *f.byID[id]
	return &cp, nil
}

// Len returns the number of stored users (test helper).
func (f *User) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.byID)
}

// HasLocalUsers reports whether any stored user carries a non-empty password hash.
func (f *User) HasLocalUsers(_ context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.byID {
		if u.PasswordHash != "" {
			return true, nil
		}
	}
	return false, nil
}

// UpdateLocale sets the stored user's locale.
func (f *User) UpdateLocale(_ context.Context, id uuid.UUID, locale string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.byID[id]
	if !ok {
		return &user.NotFoundError{ID: id.String()}
	}
	u.Locale = locale
	return nil
}
