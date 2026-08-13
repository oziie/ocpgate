// Package retry provides bounded retries with exponential backoff for the
// network calls ocpgate cannot avoid: GitLab registry syncs, the OCP OAuth
// exchange, and cluster API queries.
//
// The distinction that matters here is transient versus definitive. A 503
// from an OAuth server is worth another attempt; a 401 is the server
// telling you the password is wrong, and retrying it is both useless and
// a good way to trip an account lockout. Callers mark the latter with
// Permanent.
package retry

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// Policy bounds how hard a call is retried.
type Policy struct {
	// Attempts is the total number of tries, including the first.
	Attempts int
	// BaseDelay is the backoff before the second attempt; it doubles
	// after each failure.
	BaseDelay time.Duration
	// MaxDelay caps the backoff.
	MaxDelay time.Duration
}

// DefaultPolicy is tuned for a human waiting at a prompt: enough retries
// to ride out a brief blip, few enough that a genuinely down service fails
// in about a second rather than making the engineer wonder if it hung.
func DefaultPolicy() Policy {
	return Policy{
		Attempts:  3,
		BaseDelay: 200 * time.Millisecond,
		MaxDelay:  2 * time.Second,
	}
}

// permanentError marks a failure that must not be retried.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent marks err as definitive, stopping the retry loop. The wrapper
// is unwrapped before the error reaches the caller, so callers still match
// on their own sentinel errors.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent reports whether err was marked with Permanent.
func IsPermanent(err error) bool {
	var perm *permanentError
	return errors.As(err, &perm)
}

// Do calls fn until it succeeds, returns a Permanent error, exhausts the
// policy's attempts, or the context is done.
func Do(ctx context.Context, p Policy, fn func(context.Context) error) error {
	if p.Attempts < 1 {
		p.Attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= p.Attempts; attempt++ {
		// Checked before each attempt so a cancelled context surfaces as
		// cancellation rather than as whatever the doomed call returned.
		if err := ctx.Err(); err != nil {
			return err
		}

		err := fn(ctx)
		if err == nil {
			return nil
		}

		var perm *permanentError
		if errors.As(err, &perm) {
			return perm.err
		}

		lastErr = err
		if attempt == p.Attempts {
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff(p, attempt)):
		}
	}

	return fmt.Errorf("after %d attempts: %w", p.Attempts, lastErr)
}

// backoff returns the delay before the attempt following a failed one,
// with jitter. Engineers tend to retry in unison after an outage, so the
// jitter keeps a recovering GitLab or OAuth server from being hit by a
// synchronized wave.
func backoff(p Policy, attempt int) time.Duration {
	delay := p.BaseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if p.MaxDelay > 0 && delay >= p.MaxDelay {
			delay = p.MaxDelay
			break
		}
	}
	if delay <= 0 {
		return 0
	}

	// Equal jitter: half the delay fixed, half random, so backoff still
	// grows but never collapses to zero.
	half := delay / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}
