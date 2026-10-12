package session_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/session"
)

// NOTE: newPrincipal is a seam because sessions.user_id has a foreign key to users: a hardcoded Principal{} (uuid.Nil) fails with SQLSTATE 23503 on the Postgres backend.
func runStoreContract(
	t *testing.T,
	newStore func(t *testing.T) session.Store,
	newPrincipal func(t *testing.T) session.Principal,
) {
	t.Helper()

	t.Run("save then load", func(t *testing.T) {
		s := newStore(t)
		want := newPrincipal(t)
		want.Email = "a@b"
		want.Source = session.SourceOIDC
		require.NoError(t, s.Save(t.Context(), "a", want, time.Now().Add(time.Hour)))
		p, ok, err := s.Load(t.Context(), "a")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, want.UserID, p.UserID)
		assert.Equal(t, "a@b", p.Email)
		assert.Equal(t, session.SourceOIDC, p.Source)
	})

	t.Run("load absent sid", func(t *testing.T) {
		s := newStore(t)
		_, ok, err := s.Load(t.Context(), "nope")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("expired session does not load", func(t *testing.T) {
		s := newStore(t)
		require.NoError(t, s.Save(t.Context(), "a", newPrincipal(t), time.Now().Add(-time.Minute)))
		_, ok, err := s.Load(t.Context(), "a")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("delete", func(t *testing.T) {
		s := newStore(t)
		require.NoError(t, s.Save(t.Context(), "a", newPrincipal(t), time.Now().Add(time.Hour)))
		require.NoError(t, s.Delete(t.Context(), "a"))
		_, ok, err := s.Load(t.Context(), "a")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("delete absent sid is not an error", func(t *testing.T) {
		s := newStore(t)
		assert.NoError(t, s.Delete(t.Context(), "nope"))
	})

	// A re-Save under a live sid happens on every org switch; a plain INSERT would raise a unique violation.
	t.Run("resave under an existing sid overwrites", func(t *testing.T) {
		s := newStore(t)
		first := newPrincipal(t)
		first.Email = "first@b"
		require.NoError(t, s.Save(t.Context(), "a", first, time.Now().Add(time.Hour)))

		second := first
		second.Email = "second@b"
		second.ActiveOrgID = uuid.New()
		require.NoError(t, s.Save(t.Context(), "a", second, time.Now().Add(time.Hour)))

		p, ok, err := s.Load(t.Context(), "a")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "second@b", p.Email)
		assert.Equal(t, second.ActiveOrgID, p.ActiveOrgID)
	})

	t.Run("delete expired removes only expired rows", func(t *testing.T) {
		s := newStore(t)
		require.NoError(t, s.Save(t.Context(), "live", newPrincipal(t), time.Now().Add(time.Hour)))
		require.NoError(t, s.Save(t.Context(), "dead", newPrincipal(t), time.Now().Add(-time.Hour)))

		n, err := s.DeleteExpired(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 1, n)

		_, ok, err := s.Load(t.Context(), "live")
		require.NoError(t, err)
		assert.True(t, ok, "the live session must survive the sweep")
	})

	t.Run("delete expired on an empty store", func(t *testing.T) {
		s := newStore(t)
		n, err := s.DeleteExpired(t.Context())
		require.NoError(t, err)
		assert.Zero(t, n)
	})

	t.Run("round trips every principal field", func(t *testing.T) {
		s := newStore(t)
		want := newPrincipal(t)
		want.Email = "full@b"
		want.Name = "Full"
		want.Source = session.SourceOIDC
		want.IDPIssuer = "https://iss.example.com"
		want.IDPSubject = "sub-1"
		want.IDToken = "header.payload.signature"
		want.Scopes = []string{"a", "b"}
		want.ActiveOrgID = uuid.New()
		want.ActiveProjectID = uuid.New()
		want.IsAdmin = true
		want.Locale = "en-US"
		want.TermsAcceptedAt = time.Now().UTC().Truncate(time.Second)
		want.IssuedAt = time.Now().UTC().Truncate(time.Second)

		require.NoError(t, s.Save(t.Context(), "a", want, time.Now().Add(time.Hour)))
		got, ok, err := s.Load(t.Context(), "a")
		require.NoError(t, err)
		require.True(t, ok)

		assert.Equal(t, want.Scopes, got.Scopes)
		assert.Equal(t, want.IDToken, got.IDToken)
		assert.Equal(t, want.IsAdmin, got.IsAdmin)
		assert.Equal(t, want.Locale, got.Locale)
		assert.Equal(t, want.ActiveOrgID, got.ActiveOrgID)
		assert.Equal(t, want.ActiveProjectID, got.ActiveProjectID)
		assert.True(t, want.TermsAcceptedAt.Equal(got.TermsAcceptedAt))
		assert.True(t, want.IssuedAt.Equal(got.IssuedAt))
	})

	t.Run("sessions are isolated by sid", func(t *testing.T) {
		s := newStore(t)
		a, b := newPrincipal(t), newPrincipal(t)
		a.Email, b.Email = "a@b", "b@b"
		require.NoError(t, s.Save(t.Context(), "a", a, time.Now().Add(time.Hour)))
		require.NoError(t, s.Save(t.Context(), "b", b, time.Now().Add(time.Hour)))
		require.NoError(t, s.Delete(t.Context(), "a"))

		_, ok, err := s.Load(t.Context(), "b")
		require.NoError(t, err)
		assert.True(t, ok, "deleting one sid must not touch another")
	})
}

func TestMemoryStore_Contract(t *testing.T) {
	runStoreContract(t,
		func(*testing.T) session.Store { return session.NewMemoryStore() },
		func(*testing.T) session.Principal { return session.Principal{UserID: uuid.New()} },
	)
}
