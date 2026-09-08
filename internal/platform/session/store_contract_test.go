package session_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/session"
)

// NOTE: newPrincipal is a seam because sessions.user_id has a foreign key to users: a hardcoded
// Principal{} (uuid.Nil) fails with SQLSTATE 23503 on the Postgres backend.
func runStoreContract(
	t *testing.T,
	newStore func(t *testing.T) session.Store,
	newPrincipal func(t *testing.T) session.Principal,
) {
	t.Helper()
	ctx := context.Background()

	t.Run("save then load", func(t *testing.T) {
		s := newStore(t)
		want := newPrincipal(t)
		want.Email = "a@b"
		require.NoError(t, s.Save(ctx, "a", want, time.Now().Add(time.Hour)))
		p, ok, err := s.Load(ctx, "a")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, want.UserID, p.UserID)
		assert.Equal(t, "a@b", p.Email)
	})

	t.Run("load absent sid", func(t *testing.T) {
		s := newStore(t)
		_, ok, err := s.Load(ctx, "nope")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("save overwrites an existing sid", func(t *testing.T) {
		s := newStore(t)
		exp := time.Now().Add(time.Hour)
		first, second := newPrincipal(t), newPrincipal(t)
		first.Email, second.Email = "first@b", "second@b"
		require.NoError(t, s.Save(ctx, "b", first, exp))
		require.NoError(t, s.Save(ctx, "b", second, exp))
		p, ok, err := s.Load(ctx, "b")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "second@b", p.Email, "Save must upsert: UpdateSession re-saves under the same sid")
	})

	t.Run("delete", func(t *testing.T) {
		s := newStore(t)
		require.NoError(t, s.Save(ctx, "c", newPrincipal(t), time.Now().Add(time.Hour)))
		require.NoError(t, s.Delete(ctx, "c"))
		_, ok, err := s.Load(ctx, "c")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("expired row is not honoured", func(t *testing.T) {
		s := newStore(t)
		require.NoError(t, s.Save(ctx, "d", newPrincipal(t), time.Now().Add(-time.Minute)))
		_, ok, err := s.Load(ctx, "d")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("delete expired counts only expired", func(t *testing.T) {
		s := newStore(t)
		require.NoError(t, s.Save(ctx, "live", newPrincipal(t), time.Now().Add(time.Hour)))
		require.NoError(t, s.Save(ctx, "dead", newPrincipal(t), time.Now().Add(-time.Hour)))
		n, err := s.DeleteExpired(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		_, ok, err := s.Load(ctx, "live")
		require.NoError(t, err)
		assert.True(t, ok)
	})
}

func TestMemoryStore_Contract(t *testing.T) {
	t.Parallel()
	runStoreContract(t,
		func(t *testing.T) session.Store { return session.NewMemoryStore() },
		func(t *testing.T) session.Principal { return session.Principal{UserID: uuid.Must(uuid.NewV7())} },
	)
}
