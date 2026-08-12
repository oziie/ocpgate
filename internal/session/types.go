package session

import "time"

// Session is one engineer's temporary access to one cluster. Everything it
// describes is disposable: the directory holding the kubeconfig is created
// at Start and removed at End, and the token inside it expires on its own
// even if cleanup never runs.
type Session struct {
	ID          string
	ClusterName string
	Environment string
	Username    string
	Namespace   string

	// Dir is the per-session directory under the sessions base dir. It is
	// removed wholesale by End.
	Dir            string
	KubeconfigPath string

	StartedAt time.Time
	ExpiresAt time.Time
}

// TimeRemaining reports how long the session's token stays valid. A zero
// ExpiresAt (cluster reported no expiry) yields zero.
func (s *Session) TimeRemaining(now time.Time) time.Duration {
	if s.ExpiresAt.IsZero() {
		return 0
	}
	remaining := s.ExpiresAt.Sub(now)
	if remaining < 0 {
		return 0
	}
	return remaining
}
