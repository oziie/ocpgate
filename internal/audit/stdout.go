package audit

import (
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"

	"github.com/oziie/ocpgate/pkg/version"
)

// toolName is the discriminator Fluentd routes on to pick the
// ocpgate-audit-* index out of the shared stdout stream.
const toolName = "ocpgate"

// StdoutLogger writes audit events as newline-delimited JSON. zerolog does
// the encoding because it serializes each event as a single atomic Write,
// so concurrent goroutines cannot interleave half-written objects into the
// stream Fluentd is tailing.
type StdoutLogger struct {
	logger zerolog.Logger
	source Source
}

// NewStdoutLogger writes events to stdout. Note that this is the same
// stream Bubble Tea renders to; the TUI will pass an alternate writer via
// NewLogger once it owns the terminal.
func NewStdoutLogger() *StdoutLogger {
	return NewLogger(os.Stdout)
}

// NewLogger writes events to w.
func NewLogger(w io.Writer) *StdoutLogger {
	return &StdoutLogger{
		logger: zerolog.New(w),
		source: DetectSource(),
	}
}

// Log emits one JSON object per event, in the shape the OpenSearch index
// template expects. Zero-valued Timestamp and source fields are filled in
// here so callers only supply what is specific to the event.
func (l *StdoutLogger) Log(event AuditEvent) {
	ts := event.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	if event.SourceHost == "" {
		event.SourceHost = l.source.Hostname
	}
	if event.SourceIP == "" {
		event.SourceIP = l.source.IP
	}

	// .Log() rather than .Info(): audit records carry an outcome, not a
	// severity, so a "level" field would be noise in the index.
	e := l.logger.Log().
		Str("@timestamp", ts.UTC().Format(time.RFC3339)).
		Str("tool", toolName).
		Str("version", version.Version).
		Str("event_type", string(event.EventType))

	if event.Username != "" {
		e = e.Dict("user", zerolog.Dict().Str("username", event.Username))
	}

	if event.ClusterName != "" {
		cluster := zerolog.Dict().Str("name", event.ClusterName)
		if event.Environment != "" {
			cluster = cluster.Str("environment", event.Environment)
		}
		if event.APIEndpoint != "" {
			cluster = cluster.Str("api_endpoint", event.APIEndpoint)
		}
		e = e.Dict("cluster", cluster)
	}

	if event.SessionID != "" {
		session := zerolog.Dict().Str("id", event.SessionID)
		if !event.TokenExpiry.IsZero() {
			session = session.Str("token_expiry", event.TokenExpiry.UTC().Format(time.RFC3339))
		}
		e = e.Dict("session", session)
	}

	if event.SourceHost != "" || event.SourceIP != "" {
		e = e.Dict("source", zerolog.Dict().
			Str("hostname", event.SourceHost).
			Str("ip", event.SourceIP))
	}

	if event.Outcome != "" {
		e = e.Str("outcome", string(event.Outcome))
	}

	e.Msg(event.Message)
}
