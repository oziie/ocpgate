package audit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeOne parses the single JSON line the logger is expected to write.
func decodeOne(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 1, "expected exactly one JSON line")

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &got))
	return got
}

func TestStdoutLoggerSessionStartShape(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf)

	started := time.Date(2026, 5, 2, 14, 32, 1, 0, time.UTC)
	logger.Log(AuditEvent{
		Timestamp:   started,
		EventType:   EventSessionStart,
		Username:    "john.doe",
		ClusterName: "prod-cluster-1",
		Environment: "production",
		APIEndpoint: "https://api.prod-cluster-1.example.com:6443",
		SessionID:   "550e8400-e29b-41d4-a716-446655440000",
		TokenExpiry: started.Add(time.Hour),
		Outcome:     OutcomeSuccess,
		SourceHost:  "johns-macbook",
		SourceIP:    "10.10.1.45",
	})

	got := decodeOne(t, &buf)

	assert.Equal(t, "2026-05-02T14:32:01Z", got["@timestamp"])
	assert.Equal(t, "ocpgate", got["tool"])
	assert.Equal(t, "session_start", got["event_type"])
	assert.Equal(t, "success", got["outcome"])
	assert.NotEmpty(t, got["version"])

	assert.Equal(t, map[string]any{"username": "john.doe"}, got["user"])
	assert.Equal(t, map[string]any{
		"name":         "prod-cluster-1",
		"environment":  "production",
		"api_endpoint": "https://api.prod-cluster-1.example.com:6443",
	}, got["cluster"])
	assert.Equal(t, map[string]any{
		"id":           "550e8400-e29b-41d4-a716-446655440000",
		"token_expiry": "2026-05-02T15:32:01Z",
	}, got["session"])
	assert.Equal(t, map[string]any{
		"hostname": "johns-macbook",
		"ip":       "10.10.1.45",
	}, got["source"])

	// A severity level would be meaningless on an audit record.
	assert.NotContains(t, got, "level")
}

func TestStdoutLoggerFillsTimestampAndSource(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf)
	logger.source = Source{Hostname: "detected-host", IP: "192.0.2.10"}

	before := time.Now().Add(-time.Second)
	logger.Log(AuditEvent{EventType: EventRegistrySync, Outcome: OutcomeSuccess})
	got := decodeOne(t, &buf)

	ts, err := time.Parse(time.RFC3339, got["@timestamp"].(string))
	require.NoError(t, err)
	assert.False(t, ts.Before(before), "timestamp should default to now")

	assert.Equal(t, map[string]any{
		"hostname": "detected-host",
		"ip":       "192.0.2.10",
	}, got["source"])
}

func TestStdoutLoggerOmitsEmptyBlocks(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf)
	logger.source = Source{}

	logger.Log(AuditEvent{
		EventType: EventRegistrySync,
		Outcome:   OutcomeFailure,
		Message:   "gitlab unreachable",
	})

	got := decodeOne(t, &buf)

	assert.Equal(t, "gitlab unreachable", got["message"])
	for _, key := range []string{"user", "cluster", "session", "source"} {
		assert.NotContains(t, got, key, "empty %s block should be omitted", key)
	}
}

func TestStdoutLoggerWritesOneLinePerEvent(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf)

	logger.Log(AuditEvent{EventType: EventAuthAttempt, Username: "a", Outcome: OutcomeSuccess})
	logger.Log(AuditEvent{EventType: EventAuthFailure, Username: "b", Outcome: OutcomeFailure})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 2)
	for _, line := range lines {
		assert.NoError(t, json.Unmarshal([]byte(line), &map[string]any{}))
	}
}

func TestNopLoggerImplementsLogger(t *testing.T) {
	var logger Logger = NopLogger{}
	assert.NotPanics(t, func() { logger.Log(AuditEvent{EventType: EventSessionEnd}) })
}
