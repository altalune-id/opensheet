package db

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRetry_SucceedsFirstTry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls int
		err := retry(t.Context(), time.Minute, time.Second, func(context.Context) error {
			calls++
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, 1, calls)
	})
}

func TestRetry_SucceedsAfterFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls int
		err := retry(t.Context(), time.Minute, time.Second, func(context.Context) error {
			calls++
			if calls < 3 {
				return errors.New("not yet")
			}
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, 3, calls)
	})
}

func TestRetry_ExhaustsBudgetAndReturnsLastError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		boom := errors.New("boom")
		var calls int
		start := time.Now()
		err := retry(t.Context(), 10*time.Second, time.Second, func(context.Context) error {
			calls++
			return boom
		})
		require.ErrorIs(t, err, boom)
		require.Greater(t, calls, 1, "must retry before giving up")
		require.GreaterOrEqual(t, time.Since(start), 10*time.Second)
	})
}

func TestRetry_StopsOnContextCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			time.Sleep(2 * time.Second)
			cancel()
		}()
		err := retry(ctx, time.Hour, time.Second, func(context.Context) error {
			return errors.New("always fails")
		})
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestRetry_DoesNotOvershootBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		err := retry(t.Context(), 10*time.Second, 4*time.Second, func(context.Context) error {
			return errors.New("boom")
		})
		require.Error(t, err)
		elapsed := time.Since(start)
		require.GreaterOrEqual(t, elapsed, 10*time.Second)
		require.LessOrEqual(t, elapsed, 10*time.Second+time.Millisecond,
			"the final sleep must be clamped to the remaining budget, not a full backoff interval")
	})
}

func TestRetry_ZeroBudgetMeansSingleAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		boom := errors.New("boom")
		var calls int
		err := retry(t.Context(), 0, time.Second, func(context.Context) error {
			calls++
			return boom
		})
		require.ErrorIs(t, err, boom)
		require.Equal(t, 1, calls)
	})
}

func TestRetry_BoundsEachAttemptByRemainingBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls int
		start := time.Now()
		err := retry(t.Context(), 10*time.Second, time.Second, func(ctx context.Context) error {
			calls++
			<-ctx.Done()
			return ctx.Err()
		})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, 1, calls, "a hung attempt must consume the whole budget, not be retried")
		require.Equal(t, 10*time.Second, time.Since(start),
			"a hung attempt must not outlive the connect budget")
	})
}

func TestRetry_HungAttemptsStayWithinBudgetAcrossRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls int
		start := time.Now()
		err := retry(t.Context(), 10*time.Second, time.Second, func(ctx context.Context) error {
			calls++
			if calls == 1 {
				return errors.New("refused")
			}
			<-ctx.Done()
			return ctx.Err()
		})
		require.Error(t, err)
		require.Equal(t, 2, calls)
		require.LessOrEqual(t, time.Since(start), 10*time.Second,
			"the hung second attempt must be clamped to the remaining budget")
	})
}

func TestRetry_PermanentErrorStopsImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		boom := errors.New("role does not exist")
		var calls int
		start := time.Now()
		err := retry(t.Context(), 30*time.Second, time.Second, func(context.Context) error {
			calls++
			return permanent(boom)
		})
		require.ErrorIs(t, err, boom)
		require.Equal(t, 1, calls, "a permanent failure must not be retried")
		require.Equal(t, time.Duration(0), time.Since(start), "a permanent failure must not sleep out the budget")
	})
}
