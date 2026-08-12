// Package tui implements the Bubble Tea interface: a searchable cluster
// list, a masked credential prompt, a namespace selector, and an active
// session view with a token expiry countdown.
//
// The TUI renders to stderr, not stdout. stdout stays reserved for the
// audit JSON stream, so `ocpgate 1>>audit.log` keeps working while the
// interface is on screen.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/oziie/ocpgate/internal/audit"
	"github.com/oziie/ocpgate/internal/auth"
	"github.com/oziie/ocpgate/internal/ocp"
	"github.com/oziie/ocpgate/internal/registry"
	"github.com/oziie/ocpgate/internal/session"
	"github.com/oziie/ocpgate/internal/tui/keys"
	"github.com/oziie/ocpgate/internal/tui/styles"
	"github.com/oziie/ocpgate/internal/tui/views"
)

// state is the current step of the cluster → credentials → namespace →
// session flow.
type state int

const (
	stateClusters state = iota
	stateCredentials
	stateNamespaces
	stateSession
)

// NamespaceLister looks up the namespaces a token can see.
type NamespaceLister func(ctx context.Context, cluster registry.ClusterEntry, token string) ([]string, error)

// SessionFactory builds a session manager bound to a chosen namespace. The
// namespace is not known until the selector runs, which is why this is a
// factory rather than a ready-made manager.
type SessionFactory func(namespace string) (session.Manager, error)

// Deps are the collaborators the TUI drives. They are injected rather than
// constructed here so the model stays testable without a cluster.
type Deps struct {
	Clusters         []registry.ClusterEntry
	Authenticator    auth.Authenticator
	NewSessions      SessionFactory
	ListNamespaces   NamespaceLister
	Audit            audit.Logger
	DefaultUsername  string
	DefaultNamespace string
	ShowBadge        bool
}

// Model is the root Bubble Tea model.
type Model struct {
	deps Deps
	keys keys.KeyMap

	state         state
	width, height int

	clusters    views.ClusterList
	credentials views.Credentials
	namespaces  views.Namespace
	status      views.StatusBar

	cluster    *registry.ClusterEntry
	authResult *auth.AuthResult
	sessions   session.Manager
	session    *session.Session

	notice string
	fatal  error
}

// Messages produced by the asynchronous steps.
type (
	authFinishedMsg struct {
		result *auth.AuthResult
		err    error
	}
	namespacesMsg struct {
		names []string
		err   error
	}
	sessionStartedMsg struct {
		sess *session.Session
		err  error
	}
	shellFinishedMsg struct{ err error }
	tickMsg          time.Time
)

// New builds the root model.
func New(deps Deps) Model {
	km := keys.Default()

	return Model{
		deps:     deps,
		keys:     km,
		state:    stateClusters,
		clusters: views.NewClusterList(deps.Clusters, deps.ShowBadge),
	}
}

// Init starts the countdown ticker.
func (m Model) Init() tea.Cmd {
	return tick()
}

// Session exposes the live session so the caller can guarantee cleanup
// even when the program exits through Ctrl-C.
func (m Model) Session() *session.Session { return m.session }

// Sessions exposes the manager that owns the live session.
func (m Model) Sessions() session.Manager { return m.sessions }

// Cluster exposes the connected cluster, for the closing audit event.
func (m Model) Cluster() *registry.ClusterEntry { return m.cluster }

// Err reports a fatal error that should become the process exit status.
func (m Model) Err() error { return m.fatal }

// Update routes messages to the active view.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.status.Width = msg.Width
		m.resizeViews()
		return m, nil

	case tea.KeyMsg:
		// Ctrl-C always quits, except while a filter input is capturing
		// keys — there the list component owns the keyboard.
		if key.Matches(msg, m.keys.Global.Quit) {
			return m, tea.Quit
		}
		return m.handleKey(msg)

	case authFinishedMsg:
		return m.handleAuthFinished(msg)

	case namespacesMsg:
		return m.handleNamespaces(msg)

	case sessionStartedMsg:
		return m.handleSessionStarted(msg)

	case shellFinishedMsg:
		if msg.err != nil {
			m.notice = fmt.Sprintf("shell exited: %v", msg.err)
		}
		return m, tick()

	case tickMsg:
		return m, tick()
	}

	return m.updateActiveView(msg)
}

// handleKey applies the bindings for the current state.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {
	case stateClusters:
		// While filtering, enter and esc belong to the filter input.
		if m.clusters.Filtering() {
			return m.updateActiveView(msg)
		}
		if msg.Type == tea.KeyEnter {
			return m.selectCluster()
		}

	case stateCredentials:
		if key.Matches(msg, m.keys.Global.Back) {
			return m.backToClusters(), nil
		}
		if key.Matches(msg, m.keys.Form.Submit) && m.credentials.Submitted() {
			return m.startAuthentication()
		}

	case stateNamespaces:
		if m.namespaces.Filtering() {
			return m.updateActiveView(msg)
		}
		if key.Matches(msg, m.keys.Global.Back) {
			return m.backToClusters(), nil
		}
		if key.Matches(msg, m.keys.Form.Confirm) {
			return m.startSession()
		}

	case stateSession:
		if key.Matches(msg, m.keys.Session.Shell) {
			return m, m.openShell()
		}
		if key.Matches(msg, m.keys.Session.End) {
			return m, tea.Quit
		}
		return m, nil
	}

	return m.updateActiveView(msg)
}

// selectCluster moves from the cluster list to the credential prompt.
func (m Model) selectCluster() (tea.Model, tea.Cmd) {
	cluster := m.clusters.Selected()
	if cluster == nil {
		return m, nil
	}
	// Inactive clusters stay visible — engineers need to know they exist
	// — but the flow stops here rather than after a password is typed.
	if !cluster.Active {
		m.notice = fmt.Sprintf("%s is marked inactive; authentication is disabled", cluster.Name)
		return m, nil
	}

	m.cluster = cluster
	m.notice = ""
	m.status.Cluster = cluster.Name
	m.status.Environment = cluster.Environment
	m.credentials = views.NewCredentials(*cluster, m.deps.DefaultUsername, m.keys)
	m.state = stateCredentials

	return m, m.credentials.Init()
}

// backToClusters abandons the in-progress authentication. The token, if
// one was already issued, is dropped with it.
func (m Model) backToClusters() Model {
	m.state = stateClusters
	m.cluster = nil
	m.authResult = nil
	m.notice = ""
	m.status.Cluster = ""
	m.status.Environment = ""
	m.status.Username = ""

	return m
}

// startAuthentication kicks off the OAuth exchange.
func (m Model) startAuthentication() (tea.Model, tea.Cmd) {
	creds := m.credentials.Credentials()
	cluster := *m.cluster

	m.credentials.SetBusy(true)
	m.deps.Audit.Log(audit.AuditEvent{
		EventType:   audit.EventAuthAttempt,
		Username:    creds.Username,
		ClusterName: cluster.Name,
		Environment: cluster.Environment,
		APIEndpoint: cluster.APIEndpoint,
	})

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := m.deps.Authenticator.Authenticate(ctx, cluster, creds)
		return authFinishedMsg{result: result, err: err}
	}
}

func (m Model) handleAuthFinished(msg authFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.deps.Audit.Log(audit.AuditEvent{
			EventType:   audit.EventAuthFailure,
			Username:    m.credentials.Credentials().Username,
			ClusterName: m.cluster.Name,
			Environment: m.cluster.Environment,
			APIEndpoint: m.cluster.APIEndpoint,
			Outcome:     audit.OutcomeFailure,
			Message:     msg.err.Error(),
		})
		m.credentials.SetError(msg.err)
		return m, m.credentials.Init()
	}

	m.authResult = msg.result
	m.status.Username = msg.result.Username

	cluster := *m.cluster
	token := msg.result.Token

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		names, err := m.deps.ListNamespaces(ctx, cluster, token)
		return namespacesMsg{names: names, err: err}
	}
}

// handleNamespaces builds the selector, falling back to a free-text field
// when the token may not list namespaces — the usual case for an ordinary
// OCP user, so it is presented as normal rather than as an error.
func (m Model) handleNamespaces(msg namespacesMsg) (tea.Model, tea.Cmd) {
	m.credentials.SetBusy(false)
	m.state = stateNamespaces

	if msg.err != nil || len(msg.names) == 0 {
		notice := "this account cannot list namespaces — type one to continue"
		if msg.err != nil && !errors.Is(msg.err, ocp.ErrNamespacesForbidden) {
			notice = fmt.Sprintf("could not list namespaces (%v) — type one to continue", msg.err)
		}
		m.namespaces = views.NewNamespaceManual(m.deps.DefaultNamespace, notice, m.keys)
	} else {
		m.namespaces = views.NewNamespaceList(msg.names, m.deps.DefaultNamespace, m.keys)
	}
	m.resizeViews()

	return m, m.namespaces.Init()
}

// startSession writes the temporary kubeconfig.
func (m Model) startSession() (tea.Model, tea.Cmd) {
	namespace := m.namespaces.Selected()
	cluster := *m.cluster
	result := *m.authResult

	manager, err := m.deps.NewSessions(namespace)
	if err != nil {
		m.fatal = err
		return m, tea.Quit
	}
	m.sessions = manager
	m.status.Namespace = namespace

	return m, func() tea.Msg {
		sess, err := manager.Start(cluster, result)
		return sessionStartedMsg{sess: sess, err: err}
	}
}

func (m Model) handleSessionStarted(msg sessionStartedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.fatal = msg.err
		return m, tea.Quit
	}

	m.session = msg.sess
	m.status.ExpiresAt = msg.sess.ExpiresAt
	m.state = stateSession

	m.deps.Audit.Log(audit.AuditEvent{
		EventType:   audit.EventSessionStart,
		Username:    msg.sess.Username,
		ClusterName: m.cluster.Name,
		Environment: m.cluster.Environment,
		APIEndpoint: m.cluster.APIEndpoint,
		SessionID:   msg.sess.ID,
		TokenExpiry: msg.sess.ExpiresAt,
		Outcome:     audit.OutcomeSuccess,
	})

	return m, tick()
}

// openShell suspends the TUI, hands the terminal to the engineer's shell,
// and resumes when it exits.
func (m Model) openShell() tea.Cmd {
	cmd := exec.Command(session.LoginShell())
	cmd.Env = session.Environ(os.Environ(), m.session)

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		// A non-zero exit is the engineer's result, not our failure.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			err = nil
		}
		return shellFinishedMsg{err: err}
	})
}

// updateActiveView forwards a message to the view that owns the keyboard.
func (m Model) updateActiveView(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch m.state {
	case stateClusters:
		m.clusters, cmd = m.clusters.Update(msg)
	case stateCredentials:
		m.credentials, cmd = m.credentials.Update(msg)
	case stateNamespaces:
		m.namespaces, cmd = m.namespaces.Update(msg)
	}

	return m, cmd
}

// resizeViews gives the list views the space left over after the chrome.
func (m *Model) resizeViews() {
	if m.width == 0 || m.height == 0 {
		return
	}

	// Status bar (1 line plus its top border) and the notice line.
	const chrome = 4

	body := m.height - chrome
	if body < 3 {
		body = 3
	}

	m.clusters.SetSize(m.width, body)
	m.namespaces.SetSize(m.width, body)
}

// View renders the active step above the persistent status bar.
func (m Model) View() string {
	var body string

	switch m.state {
	case stateClusters:
		body = m.clusters.View()
	case stateCredentials:
		body = m.credentials.View()
	case stateNamespaces:
		body = m.namespaces.View()
	case stateSession:
		body = m.sessionView()
	}

	footer := ""
	if m.notice != "" {
		footer = styles.Notice.Render("  " + m.notice)
	}

	return body + "\n" + footer + "\n" + m.status.View(time.Now())
}

// sessionView is the active-session screen.
func (m Model) sessionView() string {
	remaining := "no expiry reported"
	if !m.session.ExpiresAt.IsZero() {
		remaining = views.FormatRemaining(time.Until(m.session.ExpiresAt))
	}

	return styles.Body.Render(
		styles.Title.Render("session active") + "\n\n" +
			"  cluster:   " + m.cluster.Name + "  " + views.EnvironmentBadge(*m.cluster) + "\n" +
			"  user:      " + m.session.Username + "\n" +
			"  namespace: " + orDash(m.session.Namespace) + "\n" +
			"  token:     " + remaining + "\n" +
			"  session:   " + m.session.ID + "\n\n" +
			styles.Help.Render("  enter  open a shell with KUBECONFIG set") + "\n" +
			styles.Help.Render("  q      end the session and delete the kubeconfig") + "\n",
	)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}
