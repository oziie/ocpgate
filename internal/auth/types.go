package auth

import (
	"fmt"
	"time"
)

// Credentials are the LDAP credentials an engineer types into the prompt.
// They live only as long as the Authenticate call that consumes them: they
// are never written to disk, never placed in an audit event, and never
// included in an error message.
type Credentials struct {
	Username string
	Password string
}

// String redacts the password so that an accidental %v or %s on
// Credentials — in a log line, an error, or a debugger — cannot leak it.
func (c Credentials) String() string {
	return fmt.Sprintf("Credentials{Username: %q, Password: REDACTED}", c.Username)
}

// GoString redacts the password for the %#v verb.
func (c Credentials) GoString() string {
	return c.String()
}

// Validate rejects empty credentials before a pointless network round trip.
func (c Credentials) Validate() error {
	if c.Username == "" {
		return fmt.Errorf("username is required")
	}
	if c.Password == "" {
		return fmt.Errorf("password is required")
	}
	return nil
}

// AuthResult is a successful authentication: a short-lived Bearer token
// scoped to one cluster, plus the expiry OCP reported for it.
type AuthResult struct {
	Token     string
	ExpiresAt time.Time
	Username  string
}

// String redacts the token, for the same reason Credentials redacts the
// password — the token is a live credential for the cluster.
func (r AuthResult) String() string {
	return fmt.Sprintf("AuthResult{Username: %q, ExpiresAt: %s, Token: REDACTED}",
		r.Username, r.ExpiresAt.Format(time.RFC3339))
}

// GoString redacts the token for the %#v verb.
func (r AuthResult) GoString() string {
	return r.String()
}

// IsExpired reports whether the token has expired as of now. A zero
// ExpiresAt means OCP did not report an expiry, which is treated as
// not-expired — the cluster remains the authority on token validity.
func (r AuthResult) IsExpired(now time.Time) bool {
	if r.ExpiresAt.IsZero() {
		return false
	}
	return !now.Before(r.ExpiresAt)
}
