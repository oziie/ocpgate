package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fastPolicy keeps the tests quick while still exercising the backoff path.
func fastPolicy(attempts int) Policy {
	return Policy{Attempts: attempts, BaseDelay: time.Millisecond, MaxDelay: 4 * time.Millisecond}
}

var errTransient = errors.New("connection reset")

func TestDoSucceedsOnFirstAttempt(t *testing.T) {
	calls := 0

	err := Do(context.Background(), fastPolicy(3), func(context.Context) error {
		calls++
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestDoRetriesUntilSuccess(t *testing.T) {
	calls := 0

	err := Do(context.Background(), fastPolicy(3), func(context.Context) error {
		calls++
		if calls < 3 {
			return errTransient
		}
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 3, calls)
}

func TestDoExhaustsAttemptsAndWrapsLastError(t *testing.T) {
	calls := 0

	err := Do(context.Background(), fastPolicy(3), func(context.Context) error {
		calls++
		return errTransient
	})

	require.Error(t, err)
	assert.Equal(t, 3, calls)
	assert.ErrorIs(t, err, errTransient, "caller must still be able to match the underlying error")
	assert.Contains(t, err.Error(), "after 3 attempts")
}

func TestDoStopsOnPermanentError(t *testing.T) {
	sentinel := errors.New("invalid username or password")
	calls := 0

	err := Do(context.Background(), fastPolicy(5), func(context.Context) error {
		calls++
		return Permanent(sentinel)
	})

	require.Error(t, err)
	assert.Equal(t, 1, calls, "a definitive failure must not be retried")

	// The wrapper is stripped, so callers match their own sentinel and
	// never see retry's internal type.
	assert.Equal(t, sentinel, err)
	assert.False(t, IsPermanent(err))
}

func TestDoStopsOnPermanentErrorNestedInWrapping(t *testing.T) {
	sentinel := errors.New("forbidden")
	calls := 0

	err := Do(context.Background(), fastPolicy(5), func(context.Context) error {
		calls++
		return Permanent(sentinel)
	})

	require.Error(t, err)
	assert.Equal(t, 1, calls)
	assert.ErrorIs(t, err, sentinel)
}

func TestDoReturnsImmediatelyOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	err := Do(ctx, fastPolicy(3), func(context.Context) error {
		calls++
		return errTransient
	})

	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, calls, "must not call fn once the context is already done")
}

func TestDoStopsRetryingWhenContextIsCancelledMidFlight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	calls := 0
	err := Do(ctx, Policy{Attempts: 5, BaseDelay: time.Second, MaxDelay: time.Second},
		func(context.Context) error {
			calls++
			cancel()
			return errTransient
		})

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls, "should abandon the backoff rather than wait it out")
}

func TestDoTreatsZeroAttemptsAsOne(t *testing.T) {
	calls := 0

	err := Do(context.Background(), Policy{}, func(context.Context) error {
		calls++
		return errTransient
	})

	require.Error(t, err)
	assert.Equal(t, 1, calls)
}

func TestPermanentIgnoresNil(t *testing.T) {
	assert.NoError(t, Permanent(nil))
}

func TestBackoffGrowsAndStaysWithinBounds(t *testing.T) {
	p := Policy{Attempts: 6, BaseDelay: 100 * time.Millisecond, MaxDelay: time.Second}

	for attempt := 1; attempt <= 5; attempt++ {
		// Equal jitter: never below half the nominal delay, never above it.
		nominal := min(time.Duration(1<<(attempt-1))*p.BaseDelay, p.MaxDelay)

		for range 20 {
			d := backoff(p, attempt)
			assert.GreaterOrEqual(t, d, nominal/2, "attempt %d", attempt)
			assert.LessOrEqual(t, d, nominal, "attempt %d", attempt)
		}
	}
}

func TestBackoffIsCappedByMaxDelay(t *testing.T) {
	p := Policy{Attempts: 20, BaseDelay: time.Second, MaxDelay: 2 * time.Second}

	for range 20 {
		assert.LessOrEqual(t, backoff(p, 10), 2*time.Second)
	}
}

func TestDefaultPolicyFailsFastEnoughForAPrompt(t *testing.T) {
	p := DefaultPolicy()

	var worst time.Duration
	for attempt := 1; attempt < p.Attempts; attempt++ {
		worst += min(time.Duration(1<<(attempt-1))*p.BaseDelay, p.MaxDelay)
	}

	assert.Less(t, worst, 2*time.Second,
		"an engineer waiting at a password prompt should not think it hung")
}
