package sheet_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/sheet"
)

func runIdempotencyContract(
	t *testing.T,
	newStore func(t *testing.T) sheet.IdempotencyStore,
	scoped func(context.Context) context.Context,
) {
	t.Helper()
	newKey := func() sheet.IdempotencyKey {
		return sheet.IdempotencyKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Rates", Key: "idem-1"}
	}

	t.Run("first caller claims", func(t *testing.T) {
		s, ctx, k := newStore(t), scoped(t.Context()), newKey()

		claimed, _, err := s.Reserve(ctx, k, "hash-a", sheet.IdempotencyTTL)
		require.NoError(t, err)
		assert.True(t, claimed)
	})

	t.Run("second caller does not claim and sees the prior attempt", func(t *testing.T) {
		s, ctx, k := newStore(t), scoped(t.Context()), newKey()
		_, _, err := s.Reserve(ctx, k, "hash-a", sheet.IdempotencyTTL)
		require.NoError(t, err)
		require.NoError(t, s.Complete(ctx, k, []byte(`{"appended":1}`), sheet.IdempotencyTTL))

		claimed, prior, err := s.Reserve(ctx, k, "hash-a", sheet.IdempotencyTTL)
		require.NoError(t, err)
		assert.False(t, claimed)
		assert.True(t, prior.Done)
		assert.JSONEq(t, `{"appended":1}`, string(prior.Payload))
	})

	t.Run("a differing body hash is reported", func(t *testing.T) {
		s, ctx, k := newStore(t), scoped(t.Context()), newKey()
		_, _, err := s.Reserve(ctx, k, "hash-a", sheet.IdempotencyTTL)
		require.NoError(t, err)

		claimed, prior, err := s.Reserve(ctx, k, "hash-b", sheet.IdempotencyTTL)
		require.NoError(t, err)
		assert.False(t, claimed)
		assert.Equal(t, "hash-a", prior.BodyHash, "the caller compares and answers 422")
	})

	t.Run("release makes a retry claimable again", func(t *testing.T) {
		s, ctx, k := newStore(t), scoped(t.Context()), newKey()
		_, _, err := s.Reserve(ctx, k, "hash-a", sheet.IdempotencyTTL)
		require.NoError(t, err)
		require.NoError(t, s.Release(ctx, k))

		claimed, _, err := s.Reserve(ctx, k, "hash-a", sheet.IdempotencyTTL)
		require.NoError(t, err)
		assert.True(t, claimed)
	})

	t.Run("reserved but not done replays as not-done", func(t *testing.T) {
		s, ctx, k := newStore(t), scoped(t.Context()), newKey()
		_, _, err := s.Reserve(ctx, k, "hash-a", sheet.IdempotencyTTL)
		require.NoError(t, err)

		claimed, prior, err := s.Reserve(ctx, k, "hash-a", sheet.IdempotencyTTL)
		require.NoError(t, err)
		assert.False(t, claimed)
		assert.False(t, prior.Done, "the caller answers 409: we do not know whether Google applied the row")
	})

	t.Run("a completed attempt carries its creation time", func(t *testing.T) {
		s, ctx, k := newStore(t), scoped(t.Context()), newKey()
		before := time.Now().UTC().Add(-time.Second)
		_, _, err := s.Reserve(ctx, k, "hash-a", sheet.IdempotencyTTL)
		require.NoError(t, err)

		_, prior, err := s.Reserve(ctx, k, "hash-a", sheet.IdempotencyTTL)
		require.NoError(t, err)
		assert.False(t, prior.CreatedAt.IsZero())
		assert.True(t, prior.CreatedAt.After(before), "CreatedAt = %v, want after %v", prior.CreatedAt, before)
	})

	t.Run("delete expired sweeps the expired attempt and spares the live one", func(t *testing.T) {
		s, ctx := newStore(t), scoped(t.Context())
		stale, live := newKey(), newKey()
		_, _, err := s.Reserve(ctx, stale, "hash-a", -time.Second)
		require.NoError(t, err)
		_, _, err = s.Reserve(ctx, live, "hash-a", sheet.IdempotencyTTL)
		require.NoError(t, err)

		n, err := s.DeleteExpired(ctx)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, n, 1)

		claimed, _, err := s.Reserve(ctx, stale, "hash-a", sheet.IdempotencyTTL)
		require.NoError(t, err)
		assert.True(t, claimed, "a swept attempt must be claimable again")

		claimed, _, err = s.Reserve(ctx, live, "hash-a", sheet.IdempotencyTTL)
		require.NoError(t, err)
		assert.False(t, claimed, "a live attempt must survive the sweep")
	})
}

func TestMemoryIdempotencyStore_Contract(t *testing.T) {
	t.Parallel()
	runIdempotencyContract(t,
		func(*testing.T) sheet.IdempotencyStore { return sheet.NewMemoryIdempotencyStore() },
		func(ctx context.Context) context.Context { return ctx },
	)
}

func TestMemoryIdempotencyStore_ExactlyOneConcurrentCallerClaims(t *testing.T) {
	t.Parallel()
	s := sheet.NewMemoryIdempotencyStore()
	k := sheet.IdempotencyKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Rates", Key: "idem-1"}

	const callers = 8
	results := make(chan bool, callers)
	start := make(chan struct{})
	for range callers {
		go func() {
			<-start
			claimed, _, err := s.Reserve(t.Context(), k, "hash-a", sheet.IdempotencyTTL)
			if err != nil {
				results <- false
				return
			}
			results <- claimed
		}()
	}
	close(start)

	claims := 0
	for range callers {
		if <-results {
			claims++
		}
	}
	assert.Equal(t, 1, claims, "exactly one caller may claim the reservation")
}

func TestMemoryIdempotencyStore_PayloadIsCopiedInAndOut(t *testing.T) {
	t.Parallel()
	s := sheet.NewMemoryIdempotencyStore()
	k := sheet.IdempotencyKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Rates", Key: "idem-1"}
	_, _, err := s.Reserve(t.Context(), k, "hash-a", sheet.IdempotencyTTL)
	require.NoError(t, err)

	payload := []byte(`{"appended":1}`)
	require.NoError(t, s.Complete(t.Context(), k, payload, sheet.IdempotencyTTL))
	payload[0] = 'X'

	_, prior, err := s.Reserve(t.Context(), k, "hash-a", sheet.IdempotencyTTL)
	require.NoError(t, err)
	assert.JSONEq(t, `{"appended":1}`, string(prior.Payload), "a caller must not be able to mutate a stored payload")
}
