package session

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// DefaultExpiryThresholds are the points at which an engineer is warned
// that the session's token is running out. They are spaced so there is
// time to react: finish the task, or exit and reconnect.
func DefaultExpiryThresholds() []time.Duration {
	return []time.Duration{15 * time.Minute, 5 * time.Minute, time.Minute}
}

// ExpiryWatcher warns as a session's token approaches expiry, then fires
// once when it lapses.
//
// This exists because the token expires silently: kubectl simply starts
// returning 401, which reads like a broken cluster rather than a finished
// session. A shell opened by `ocpgate connect` has no status bar to show a
// countdown, so the warning has to come to the engineer.
type ExpiryWatcher struct {
	ExpiresAt  time.Time
	Thresholds []time.Duration

	// OnWarn is called with the actual time remaining, which is at most
	// the threshold that triggered it.
	OnWarn func(remaining time.Duration)
	// OnExpire is called once, when the token lapses.
	OnExpire func()
}

// Run blocks until the token expires or ctx ends. A zero ExpiresAt means
// the cluster reported no expiry, so there is nothing to watch.
//
// Thresholds already in the past when Run starts are skipped rather than
// fired immediately: a short-lived token should not open with a burst of
// warnings the engineer cannot act on.
func (w ExpiryWatcher) Run(ctx context.Context) {
	if w.ExpiresAt.IsZero() {
		return
	}

	thresholds := append([]time.Duration(nil), w.Thresholds...)
	sort.Slice(thresholds, func(i, j int) bool { return thresholds[i] > thresholds[j] })

	for _, threshold := range thresholds {
		deadline := w.ExpiresAt.Add(-threshold)
		if time.Until(deadline) <= 0 {
			continue
		}
		if !sleepUntil(ctx, deadline) {
			return
		}
		if w.OnWarn != nil {
			w.OnWarn(remainingUntil(w.ExpiresAt))
		}
	}

	if time.Until(w.ExpiresAt) > 0 && !sleepUntil(ctx, w.ExpiresAt) {
		return
	}
	if w.OnExpire != nil {
		w.OnExpire()
	}
}

// sleepUntil waits for deadline, reporting false if ctx ended first.
func sleepUntil(ctx context.Context, deadline time.Time) bool {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func remainingUntil(t time.Time) time.Duration {
	if remaining := time.Until(t); remaining > 0 {
		return remaining
	}
	return 0
}

// FormatRemaining renders a duration as a compact countdown: 2h04m, 14m09s,
// or 45s. Seconds appear under an hour so a session nearing its end
// visibly ticks rather than sitting on a stale minute count.
func FormatRemaining(d time.Duration) string {
	if d <= 0 {
		return "expired"
	}

	d = d.Round(time.Second)
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	switch {
	case hours > 0:
		return fmt.Sprintf("%dh%02dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}
