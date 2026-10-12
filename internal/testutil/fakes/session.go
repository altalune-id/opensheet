package fakes

import (
	"context"
	"time"

	"altalune.id/opensheet/internal/platform/session"
)

// Sessions is a session.Store that delegates to Store and fails Save with SaveErr when set.
type Sessions struct {
	session.Store
	SaveErr error
}

// Save returns f.SaveErr when set, else delegates to the wrapped Store.
func (f *Sessions) Save(ctx context.Context, sid string, p session.Principal, exp time.Time) error {
	if f.SaveErr != nil {
		return f.SaveErr
	}
	return f.Store.Save(ctx, sid, p, exp)
}
