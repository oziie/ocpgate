package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/oziie/ocpgate/internal/audit"
	"github.com/oziie/ocpgate/internal/auth"
	"github.com/oziie/ocpgate/internal/ocp"
	"github.com/oziie/ocpgate/internal/registry"
	"github.com/oziie/ocpgate/internal/session"
)

const (
	testUsername = "john.doe"
	testPassword = "s3cret"
	testToken    = "sha256~xf9Kq2LmN0pQrStUvWxYz"
)

// --- fakes -----------------------------------------------------------------

type fakeAuthenticator struct {
	result *auth.AuthResult
	err    error

	calls []auth.Credentials
}

func (f *fakeAuthenticator) Authenticate(_ context.Context, _ registry.ClusterEntry, creds auth.Credentials) (*auth.AuthResult, error) {
	f.calls = append(f.calls, creds)
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

type fakeSessions struct {
	namespace string
	started   *session.Session
	startErr  error
	ended     int
	expired   bool
}

func (f *fakeSessions) Start(cluster registry.ClusterEntry, result auth.AuthResult) (*session.Session, error) {
	if f.startErr != nil {
		return nil, f.startErr
	}
	f.started = &session.Session{
		ID:             "session-123",
		ClusterName:    cluster.Name,
		Environment:    cluster.Environment,
		Username:       result.Username,
		Namespace:      f.namespace,
		Dir:            "/tmp/ocpgate/session-123",
		KubeconfigPath: "/tmp/ocpgate/session-123/kubeconfig",
		StartedAt:      time.Now(),
		ExpiresAt:      result.ExpiresAt,
	}
	return f.started, nil
}

func (f *fakeSessions) End(*session.Session) error      { f.ended++; return nil }
func (f *fakeSessions) IsExpired(*session.Session) bool { return f.expired }

type captureAudit struct {
	events []audit.AuditEvent
}

func (c *captureAudit) Log(event audit.AuditEvent) { c.events = append(c.events, event) }

func (c *captureAudit) types() []string {
	out := make([]string, 0, len(c.events))
	for _, e := range c.events {
		out = append(out, string(e.EventType))
	}
	return out
}

// --- harness ---------------------------------------------------------------

func testClusters() []registry.ClusterEntry {
	return []registry.ClusterEntry{
		{
			Name: "prod-cluster-1", APIEndpoint: "https://api.prod-1.example.com:6443",
			Environment: registry.EnvProduction, Region: "eu-west", LDAPRealm: "PROD", Active: true,
		},
		{
			Name: "test-cluster-1", APIEndpoint: "https://api.test-1.example.com:6443",
			Environment: registry.EnvTest, Region: "eu-central", LDAPRealm: "TEST", Active: true,
		},
		{
			Name: "old-cluster", APIEndpoint: "https://api.old.example.com:6443",
			Environment: registry.EnvTest, Region: "eu-west", LDAPRealm: "TEST", Active: false,
		},
	}
}

type harness struct {
	model   Model
	auth    *fakeAuthenticator
	session *fakeSessions
	audit   *captureAudit
}

func newHarness(t *testing.T, opts ...func(*Deps)) *harness {
	t.Helper()

	authenticator := &fakeAuthenticator{
		result: &auth.AuthResult{
			Token:     testToken,
			Username:  testUsername,
			ExpiresAt: time.Now().Add(24 * time.Hour),
		},
	}
	sessions := &fakeSessions{}
	recorder := &captureAudit{}

	deps := Deps{
		Clusters:      testClusters(),
		Authenticator: authenticator,
		NewSessions: func(namespace string) (session.Manager, error) {
			sessions.namespace = namespace
			return sessions, nil
		},
		ListNamespaces: func(context.Context, registry.ClusterEntry, string) ([]string, error) {
			return []string{"default", "team-a", "team-b"}, nil
		},
		Audit:            recorder,
		DefaultUsername:  testUsername,
		DefaultNamespace: "default",
		ShowBadge:        true,
	}
	for _, apply := range opts {
		apply(&deps)
	}

	h := &harness{
		model:   New(deps),
		auth:    authenticator,
		session: sessions,
		audit:   recorder,
	}
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})

	return h
}

// send applies a message and keeps the resulting model.
func (h *harness) send(msg tea.Msg) tea.Cmd {
	next, cmd := h.model.Update(msg)
	h.model = next.(Model)
	return cmd
}

// run applies a message, then resolves the command it returned and applies
// that too — which is how the asynchronous steps (auth, namespace lookup,
// session start) advance in a test without a running event loop.
func (h *harness) run(msg tea.Msg) {
	cmd := h.send(msg)
	h.resolve(cmd)
}

// resolve follows only the messages that advance the flow. Cursor blink
// and the countdown tick re-issue themselves forever by design, so
// chasing every command would never terminate.
func (h *harness) resolve(cmd tea.Cmd) {
	for step := 0; cmd != nil && step < 10; step++ {
		switch msg := cmd().(type) {
		case authFinishedMsg, namespacesMsg, sessionStartedMsg:
			cmd = h.send(msg)
		default:
			return
		}
	}
}

func (h *harness) key(k tea.KeyType) tea.Cmd {
	return h.send(tea.KeyMsg{Type: k})
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// selectFirstCluster walks the cluster list to the credential prompt.
func (h *harness) selectFirstCluster(t *testing.T) {
	t.Helper()
	h.key(tea.KeyEnter)
	require.Equal(t, stateCredentials, h.model.state)
}

// authenticate types the password and drives the auth round trip.
func (h *harness) authenticate(t *testing.T) {
	t.Helper()
	h.typeText(testPassword)
	h.run(tea.KeyMsg{Type: tea.KeyEnter})
}

// --- tests -----------------------------------------------------------------

func TestFullFlowToActiveSession(t *testing.T) {
	h := newHarness(t)

	h.selectFirstCluster(t)
	h.authenticate(t)
	require.Equal(t, stateNamespaces, h.model.state)

	// Confirm the namespace, which starts the session.
	h.run(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, stateSession, h.model.state)

	// The credentials reached the authenticator intact.
	require.Len(t, h.auth.calls, 1)
	assert.Equal(t, testUsername, h.auth.calls[0].Username)
	assert.Equal(t, testPassword, h.auth.calls[0].Password)

	require.NotNil(t, h.model.session)
	assert.Equal(t, "prod-cluster-1", h.model.session.ClusterName)
	assert.Equal(t, "default", h.session.namespace, "selector default should reach the session manager")

	assert.Equal(t, []string{"auth_attempt", "session_start"}, h.audit.types())

	start := h.audit.events[1]
	assert.Equal(t, audit.OutcomeSuccess, start.Outcome)
	assert.Equal(t, "prod-cluster-1", start.ClusterName)
	assert.Equal(t, "production", start.Environment)
	assert.Equal(t, "session-123", start.SessionID)
}

func TestSessionEndsThroughRunCleanup(t *testing.T) {
	h := newHarness(t)

	h.selectFirstCluster(t)
	h.authenticate(t)
	h.run(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, stateSession, h.model.state)

	// This is what Run does after the program loop exits, including on
	// Ctrl-C — the kubeconfig must not outlive the process either way.
	endSession(h.model, h.audit)

	assert.Equal(t, 1, h.session.ended, "session directory should be removed")
	assert.Equal(t, []string{"auth_attempt", "session_start", "session_end"}, h.audit.types())
}

func TestExpiredTokenIsAuditedSeparately(t *testing.T) {
	h := newHarness(t)
	h.session.expired = true

	h.selectFirstCluster(t)
	h.authenticate(t)
	h.run(tea.KeyMsg{Type: tea.KeyEnter})

	endSession(h.model, h.audit)

	assert.Equal(t,
		[]string{"auth_attempt", "session_start", "session_end", "token_expired"},
		h.audit.types())
}

func TestTokenExpiryIsRecordedOnceWhileSessionIsOpen(t *testing.T) {
	h := newHarness(t)

	h.selectFirstCluster(t)
	h.authenticate(t)
	h.run(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, stateSession, h.model.state)

	// The token lapses while the engineer is still in the session view.
	h.session.expired = true
	h.send(tickMsg(time.Now()))

	assert.Equal(t, []string{"auth_attempt", "session_start", "token_expired"}, h.audit.types())
	assert.Contains(t, h.model.View(), "token expired")

	// Later ticks must not repeat it.
	h.send(tickMsg(time.Now()))
	h.send(tickMsg(time.Now()))
	assert.Equal(t, []string{"auth_attempt", "session_start", "token_expired"}, h.audit.types())

	// Nor may teardown, which checks expiry too.
	endSession(h.model, h.audit)
	assert.Equal(t,
		[]string{"auth_attempt", "session_start", "token_expired", "session_end"},
		h.audit.types())
}

func TestTickDoesNotRecordExpiryBeforeTheSessionStarts(t *testing.T) {
	h := newHarness(t)
	h.session.expired = true

	h.selectFirstCluster(t)
	h.send(tickMsg(time.Now()))

	assert.Empty(t, h.audit.types(), "no session, nothing to expire")
}

func TestAuthFailureKeepsUserOnFormAndAuditsFailure(t *testing.T) {
	h := newHarness(t, func(d *Deps) {
		d.Authenticator = &fakeAuthenticator{err: auth.ErrInvalidCredentials}
	})

	h.selectFirstCluster(t)
	h.typeText("wrong-password")
	h.run(tea.KeyMsg{Type: tea.KeyEnter})

	assert.Equal(t, stateCredentials, h.model.state, "should stay on the credential form")
	assert.Nil(t, h.model.session)
	assert.Equal(t, []string{"auth_attempt", "auth_failure"}, h.audit.types())
	assert.Equal(t, audit.OutcomeFailure, h.audit.events[1].Outcome)

	// The failed password must not survive into the retry.
	view := h.model.View()
	assert.NotContains(t, view, "wrong-password")
	assert.Contains(t, view, "invalid username or password")
}

func TestInactiveClusterIsRefusedBeforeCredentials(t *testing.T) {
	h := newHarness(t)

	// Third entry is the inactive cluster.
	h.key(tea.KeyDown)
	h.key(tea.KeyDown)
	require.Equal(t, "old-cluster", h.model.clusters.Selected().Name)

	h.key(tea.KeyEnter)

	assert.Equal(t, stateClusters, h.model.state, "must not advance to the password prompt")
	assert.Contains(t, h.model.notice, "inactive")
	assert.Empty(t, h.audit.events, "a refused cluster produces no auth attempt")
}

func TestPasswordIsNeverRendered(t *testing.T) {
	h := newHarness(t)

	h.selectFirstCluster(t)
	h.typeText(testPassword)

	view := h.model.View()
	assert.NotContains(t, view, testPassword)
	assert.Contains(t, view, strings.Repeat("•", len(testPassword)))
}

func TestEscapeReturnsToClusterListAndDropsToken(t *testing.T) {
	h := newHarness(t)

	h.selectFirstCluster(t)
	h.authenticate(t)
	require.Equal(t, stateNamespaces, h.model.state)
	require.NotNil(t, h.model.authResult)

	h.key(tea.KeyEsc)

	assert.Equal(t, stateClusters, h.model.state)
	assert.Nil(t, h.model.authResult, "backing out must discard the issued token")
	assert.Nil(t, h.model.cluster)
	assert.Empty(t, h.model.status.Username)
}

func TestNamespaceListingForbiddenFallsBackToManualEntry(t *testing.T) {
	h := newHarness(t, func(d *Deps) {
		d.ListNamespaces = func(context.Context, registry.ClusterEntry, string) ([]string, error) {
			return nil, ocp.ErrNamespacesForbidden
		}
	})

	h.selectFirstCluster(t)
	h.authenticate(t)

	require.Equal(t, stateNamespaces, h.model.state)
	assert.True(t, h.model.namespaces.Manual(), "should offer a text field instead of a list")

	// The default namespace is pre-filled, so enter still works.
	assert.Equal(t, "default", h.model.namespaces.Selected())

	view := h.model.View()
	assert.Contains(t, view, "cannot list namespaces")
	assert.NotContains(t, view, "Forbidden", "a normal permission level should not read as an error")
}

func TestNamespaceListingErrorFallsBackWithReason(t *testing.T) {
	h := newHarness(t, func(d *Deps) {
		d.ListNamespaces = func(context.Context, registry.ClusterEntry, string) ([]string, error) {
			return nil, errors.New("connection refused")
		}
	})

	h.selectFirstCluster(t)
	h.authenticate(t)

	require.Equal(t, stateNamespaces, h.model.state)
	assert.Contains(t, h.model.View(), "connection refused")
}

func TestSelectedNamespaceReachesSessionManager(t *testing.T) {
	h := newHarness(t)

	h.selectFirstCluster(t)
	h.authenticate(t)

	// Move from "default" to "team-a".
	h.key(tea.KeyDown)
	h.run(tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, stateSession, h.model.state)
	assert.Equal(t, "team-a", h.session.namespace)
	assert.Equal(t, "team-a", h.model.status.Namespace)
}

func TestEnvironmentFilterCyclesThroughEnvironments(t *testing.T) {
	h := newHarness(t)

	assert.Empty(t, h.model.clusters.EnvironmentFilter())

	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	assert.Equal(t, registry.EnvProduction, h.model.clusters.EnvironmentFilter())
	assert.Equal(t, "prod-cluster-1", h.model.clusters.Selected().Name)

	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	assert.Equal(t, registry.EnvTest, h.model.clusters.EnvironmentFilter())
	assert.Equal(t, "test-cluster-1", h.model.clusters.Selected().Name)

	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	assert.Empty(t, h.model.clusters.EnvironmentFilter())
}

func TestStatusBarTracksProgressThroughFlow(t *testing.T) {
	h := newHarness(t)

	assert.Empty(t, h.model.status.Cluster)

	h.selectFirstCluster(t)
	assert.Equal(t, "prod-cluster-1", h.model.status.Cluster)
	assert.Empty(t, h.model.status.Username)

	h.authenticate(t)
	assert.Equal(t, testUsername, h.model.status.Username)
	assert.True(t, h.model.status.ExpiresAt.IsZero(), "no token expiry until the session starts")

	h.run(tea.KeyMsg{Type: tea.KeyEnter})
	assert.False(t, h.model.status.ExpiresAt.IsZero())
}

func TestSessionStartFailureIsFatal(t *testing.T) {
	h := newHarness(t)
	h.session.startErr = errors.New("disk full")

	h.selectFirstCluster(t)
	h.authenticate(t)
	h.run(tea.KeyMsg{Type: tea.KeyEnter})

	require.Error(t, h.model.Err())
	assert.Contains(t, h.model.Err().Error(), "disk full")
	assert.Nil(t, h.model.session)
}

func TestRunRefusesEmptyRegistry(t *testing.T) {
	err := Run(context.Background(), Deps{Audit: &captureAudit{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no clusters in the registry")
}
