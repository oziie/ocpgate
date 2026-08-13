package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorder collects watcher callbacks from the watcher's goroutine.
type recorder struct {
	mu      sync.Mutex
	warns   []time.Duration
	expired int
}

func (r *recorder) warn(remaining time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warns = append(r.warns, remaining)
}

func (r *recorder) expire() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expired++
}

func (r *recorder) snapshot() ([]time.Duration, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Duration(nil), r.warns...), r.expired
}

func (r *recorder) watcher(expiresAt time.Time, thresholds ...time.Duration) ExpiryWatcher {
	return ExpiryWatcher{
		ExpiresAt:  expiresAt,
		Thresholds: thresholds,
		OnWarn:     r.warn,
		OnExpire:   r.expire,
	}
}

func TestExpiryWatcherWarnsThenExpires(t *testing.T) {
	rec := &recorder{}
	// Compressed timeline: warn at 200ms and 100ms before a 300ms expiry.
	watcher := rec.watcher(time.Now().Add(300*time.Millisecond), 200*time.Millisecond, 100*time.Millisecond)

	watcher.Run(context.Background())

	warns, expired := rec.snapshot()
	require.Len(t, warns, 2)
	assert.Equal(t, 1, expired)

	// Each warning reports the real time left, and they count down.
	assert.Greater(t, warns[0], warns[1])
	assert.LessOrEqual(t, warns[0], 200*time.Millisecond)
	assert.LessOrEqual(t, warns[1], 100*time.Millisecond)
}

func TestExpiryWatcherOrdersThresholdsRegardlessOfInput(t *testing.T) {
	rec := &recorder{}
	// Deliberately out of order.
	watcher := rec.watcher(time.Now().Add(300*time.Millisecond), 100*time.Millisecond, 200*time.Millisecond)

	watcher.Run(context.Background())

	warns, _ := rec.snapshot()
	require.Len(t, warns, 2)
	assert.Greater(t, warns[0], warns[1], "warnings must fire furthest-out first")
}

func TestExpiryWatcherSkipsThresholdsAlreadyPassed(t *testing.T) {
	rec := &recorder{}
	// Only 150ms left, so the 10-minute threshold is long gone.
	watcher := rec.watcher(time.Now().Add(150*time.Millisecond), 10*time.Minute, 100*time.Millisecond)

	watcher.Run(context.Background())

	warns, expired := rec.snapshot()
	assert.Len(t, warns, 1, "a passed threshold must not fire a warning the engineer cannot act on")
	assert.Equal(t, 1, expired)
}

func TestExpiryWatcherFiresImmediatelyWhenAlreadyExpired(t *testing.T) {
	rec := &recorder{}
	watcher := rec.watcher(time.Now().Add(-time.Minute), 15*time.Minute)

	start := time.Now()
	watcher.Run(context.Background())

	warns, expired := rec.snapshot()
	assert.Empty(t, warns)
	assert.Equal(t, 1, expired)
	assert.Less(t, time.Since(start), 100*time.Millisecond, "should not wait on a token that is already gone")
}

func TestExpiryWatcherStopsOnContextCancel(t *testing.T) {
	rec := &recorder{}
	watcher := rec.watcher(time.Now().Add(10*time.Second), 5*time.Second)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		watcher.Run(ctx)
		close(done)
	}()

	// The shell exited, so the watcher should stop rather than warn about
	// a session that has already ended.
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not stop after context cancellation")
	}

	warns, expired := rec.snapshot()
	assert.Empty(t, warns)
	assert.Zero(t, expired)
}

func TestExpiryWatcherIgnoresZeroExpiry(t *testing.T) {
	rec := &recorder{}
	watcher := rec.watcher(time.Time{}, time.Second)

	start := time.Now()
	watcher.Run(context.Background())

	warns, expired := rec.snapshot()
	assert.Empty(t, warns)
	assert.Zero(t, expired, "no reported expiry means nothing to watch")
	assert.Less(t, time.Since(start), 100*time.Millisecond)
}

func TestExpiryWatcherToleratesNilCallbacks(t *testing.T) {
	watcher := ExpiryWatcher{
		ExpiresAt:  time.Now().Add(50 * time.Millisecond),
		Thresholds: []time.Duration{25 * time.Millisecond},
	}
	assert.NotPanics(t, func() { watcher.Run(context.Background()) })
}

func TestDefaultExpiryThresholdsDescend(t *testing.T) {
	thresholds := DefaultExpiryThresholds()
	require.NotEmpty(t, thresholds)

	for i := 1; i < len(thresholds); i++ {
		assert.Less(t, thresholds[i], thresholds[i-1])
	}
}

func TestFormatRemaining(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want string
	}{
		{"hours pad minutes", 2*time.Hour + 4*time.Minute, "2h04m"},
		{"just under a day", 23*time.Hour + 59*time.Minute, "23h59m"},
		{"minutes show seconds", 14*time.Minute + 9*time.Second, "14m09s"},
		{"under a minute", 45 * time.Second, "45s"},
		{"zero", 0, "expired"},
		{"negative", -time.Minute, "expired"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, FormatRemaining(tc.in))
		})
	}
}
