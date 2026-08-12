package audit

import "time"

// EventType enumerates the auditable actions ocpgate emits.
type EventType string

const (
	EventAuthAttempt  EventType = "auth_attempt"
	EventAuthFailure  EventType = "auth_failure"
	EventSessionStart EventType = "session_start"
	EventSessionEnd   EventType = "session_end"
	EventRegistrySync EventType = "registry_sync"
	EventTokenExpired EventType = "token_expired"
)

// Outcome records whether the audited action succeeded.
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
)

// AuditEvent is a single auditable action. Fields left zero are either
// omitted from the emitted JSON (cluster/session/user blocks) or filled in
// by the logger (Timestamp, SourceHost, SourceIP).
//
// There is deliberately no field for credentials: the password never
// leaves the auth module, and the token never leaves the session module.
type AuditEvent struct {
	Timestamp   time.Time
	EventType   EventType
	Username    string
	ClusterName string
	Environment string
	APIEndpoint string
	SessionID   string
	TokenExpiry time.Time
	Outcome     Outcome
	Message     string
	SourceHost  string
	SourceIP    string
}

// Logger receives audit events. Implementations must be safe for
// concurrent use and must never block the caller on I/O failure — auditing
// is observability, not control flow, so a broken sink degrades to silence
// rather than failing the user's session.
type Logger interface {
	Log(event AuditEvent)
}
