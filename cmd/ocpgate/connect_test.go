package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/oziie/ocpgate/internal/registry"
	"github.com/oziie/ocpgate/internal/session"
)

const (
	e2eUser  = "john.doe"
	e2ePass  = "s3cret"
	e2eToken = "sha256~xf9Kq2LmN0pQrStUvWxYz"
)

// newFakeCluster serves the two endpoints connect depends on: OAuth
// discovery and the challenging-client authorize endpoint.
func newFakeCluster(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)

	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q}`, server.URL, server.URL+"/oauth/authorize")
	})

	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != e2eUser || pass != e2ePass {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Location", fmt.Sprintf(
			"https://oauth.example.com/implicit#access_token=%s&expires_in=86400&token_type=Bearer", e2eToken))
		w.WriteHeader(http.StatusFound)
	})

	return server
}

// e2eEnv is an isolated ocpgate installation: its own HOME (so session and
// cache paths stay inside the test), a config file, and a pre-seeded
// cluster cache so no GitLab access is needed.
type e2eEnv struct {
	home       string
	configPath string
	cluster    registry.ClusterEntry
}

func setupE2E(t *testing.T, apiEndpoint string, active bool) *e2eEnv {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	// Ensure a stale token in the developer's own environment cannot
	// influence the test.
	t.Setenv("OCPGATE_GITLAB_TOKEN", "")

	cachePath := filepath.Join(home, ".cache", "ocpgate", "clusters.json")
	cluster := registry.ClusterEntry{
		Name:        "prod-cluster-1",
		APIEndpoint: apiEndpoint,
		Environment: registry.EnvProduction,
		Region:      "eu-west",
		LDAPRealm:   "PROD",
		Active:      active,
	}
	require.NoError(t, registry.NewCache(cachePath).Save([]registry.ClusterEntry{cluster}, time.Now().UTC()))

	configPath := filepath.Join(home, "config.yaml")
	configYAML := fmt.Sprintf(`registry:
  gitlab_url: https://gitlab.example.com
  project_id: "123"
  cache_path: %s
  sync_on_start: false
audit:
  enabled: true
  writer: stdout
tui:
  default_namespace: default
`, cachePath)
	require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0o600))

	return &e2eEnv{home: home, configPath: configPath, cluster: cluster}
}

// run executes the CLI with stdin fed from input, capturing stdout (the
// audit stream) and stderr (human status) by swapping the process handles
// the command writes to directly.
func (e *e2eEnv) run(t *testing.T, input string, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	stdinFile := filepath.Join(t.TempDir(), "stdin")
	require.NoError(t, os.WriteFile(stdinFile, []byte(input), 0o600))

	in, err := os.Open(stdinFile)
	require.NoError(t, err)
	defer in.Close()

	outFile, errFile := swapStdio(t, in)

	root := newRootCmd()
	root.SetArgs(append([]string{"--config", e.configPath}, args...))
	runErr := root.ExecuteContext(context.Background())

	return readFile(t, outFile), readFile(t, errFile), runErr
}

// swapStdio redirects os.Stdin/Stdout/Stderr to test-controlled files and
// restores them when the test ends. Files rather than pipes, so a chatty
// child process cannot deadlock on a full pipe buffer.
func swapStdio(t *testing.T, in *os.File) (stdoutPath, stderrPath string) {
	t.Helper()

	dir := t.TempDir()
	stdoutPath = filepath.Join(dir, "stdout")
	stderrPath = filepath.Join(dir, "stderr")

	out, err := os.Create(stdoutPath)
	require.NoError(t, err)
	errOut, err := os.Create(stderrPath)
	require.NoError(t, err)

	origIn, origOut, origErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = in, out, errOut

	t.Cleanup(func() {
		os.Stdin, os.Stdout, os.Stderr = origIn, origOut, origErr
		out.Close()
		errOut.Close()
	})

	return stdoutPath, stderrPath
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// auditEvents parses the JSON lines ocpgate wrote to stdout, keyed by
// event_type in the order they were emitted.
func auditEvents(t *testing.T, stdout string) []map[string]any {
	t.Helper()

	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var event map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &event), "line: %s", line)
		events = append(events, event)
	}
	return events
}

func eventTypes(events []map[string]any) []string {
	types := make([]string, 0, len(events))
	for _, e := range events {
		types = append(types, fmt.Sprint(e["event_type"]))
	}
	return types
}

func TestConnectEndToEnd(t *testing.T) {
	server := newFakeCluster(t)
	env := setupE2E(t, server.URL, true)

	// The session's child process records what it actually saw, which is
	// the only real proof the kubeconfig was usable while it ran.
	captureDir := t.TempDir()
	capturedKubeconfig := filepath.Join(captureDir, "kubeconfig.yaml")
	capturedSessionID := filepath.Join(captureDir, "session-id")
	script := fmt.Sprintf(`cat "$KUBECONFIG" > %q; printf '%%s' "$OCPGATE_SESSION_ID" > %q`,
		capturedKubeconfig, capturedSessionID)

	stdout, stderr, err := env.run(t, e2ePass+"\n",
		"connect", "prod-cluster-1",
		"--username", e2eUser,
		"--namespace", "team-a",
		"--insecure-skip-tls-verify",
		"--", "sh", "-c", script)
	require.NoError(t, err, "stderr: %s", stderr)

	// The child saw a working kubeconfig for the right cluster.
	cfg, err := clientcmd.LoadFromFile(capturedKubeconfig)
	require.NoError(t, err)
	assert.Equal(t, "prod-cluster-1", cfg.CurrentContext)
	assert.Equal(t, server.URL, cfg.Clusters["prod-cluster-1"].Server)
	assert.Equal(t, e2eToken, cfg.AuthInfos[e2eUser].Token)
	assert.Equal(t, "team-a", cfg.Contexts["prod-cluster-1"].Namespace)

	sessionID := readFile(t, capturedSessionID)
	assert.NotEmpty(t, sessionID, "child should see OCPGATE_SESSION_ID")

	// The temporary kubeconfig is gone once the session ends.
	sessionDir := filepath.Join(env.home, ".cache", "ocpgate", "sessions", sessionID)
	_, statErr := os.Stat(sessionDir)
	assert.True(t, os.IsNotExist(statErr), "session directory should be deleted on exit")

	// The audit trail records the full arc of the session.
	events := auditEvents(t, stdout)
	assert.Equal(t, []string{"auth_attempt", "session_start", "session_end"}, eventTypes(events))

	start := events[1]
	assert.Equal(t, "success", start["outcome"])
	assert.Equal(t, map[string]any{"username": e2eUser}, start["user"])
	assert.Equal(t, "prod-cluster-1", start["cluster"].(map[string]any)["name"])
	assert.Equal(t, "production", start["cluster"].(map[string]any)["environment"])
	assert.Equal(t, sessionID, start["session"].(map[string]any)["id"])
	assert.NotEmpty(t, start["session"].(map[string]any)["token_expiry"])
	assert.Equal(t, sessionID, events[2]["session"].(map[string]any)["id"])

	// Neither the password nor the token may appear anywhere in the audit
	// stream or the user-facing output.
	for _, stream := range []string{stdout, stderr} {
		assert.NotContains(t, stream, e2ePass)
		assert.NotContains(t, stream, e2eToken)
	}
}

func TestConnectPromptsForUsernameWhenNotSupplied(t *testing.T) {
	server := newFakeCluster(t)
	env := setupE2E(t, server.URL, true)

	stdout, stderr, err := env.run(t, e2eUser+"\n"+e2ePass+"\n",
		"connect", "prod-cluster-1", "--insecure-skip-tls-verify", "--", "true")
	require.NoError(t, err, "stderr: %s", stderr)

	assert.Contains(t, stderr, "Username")
	assert.Equal(t, []string{"auth_attempt", "session_start", "session_end"}, eventTypes(auditEvents(t, stdout)))
}

func TestConnectInvalidCredentialsAuditsFailure(t *testing.T) {
	server := newFakeCluster(t)
	env := setupE2E(t, server.URL, true)

	stdout, _, err := env.run(t, "wrong-password\n",
		"connect", "prod-cluster-1", "--username", e2eUser, "--insecure-skip-tls-verify", "--", "true")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication failed")

	events := auditEvents(t, stdout)
	require.Equal(t, []string{"auth_attempt", "auth_failure"}, eventTypes(events))
	assert.Equal(t, "failure", events[1]["outcome"])
	assert.NotContains(t, stdout, "wrong-password")

	// No session may exist after a failed authentication.
	sessions, _ := os.ReadDir(filepath.Join(env.home, ".cache", "ocpgate", "sessions"))
	assert.Empty(t, sessions)
}

func TestConnectRefusesInactiveClusterBeforePrompting(t *testing.T) {
	server := newFakeCluster(t)
	env := setupE2E(t, server.URL, false)

	stdout, stderr, err := env.run(t, "",
		"connect", "prod-cluster-1", "--username", e2eUser, "--insecure-skip-tls-verify", "--", "true")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inactive")

	assert.NotContains(t, stderr, "Password", "must not ask for credentials it will refuse to use")
	assert.Empty(t, auditEvents(t, stdout))
}

func TestConnectUnknownCluster(t *testing.T) {
	server := newFakeCluster(t)
	env := setupE2E(t, server.URL, true)

	_, _, err := env.run(t, "", "connect", "no-such-cluster", "--", "true")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in registry")
}

func TestConnectCleansUpWhenChildCommandFails(t *testing.T) {
	server := newFakeCluster(t)
	env := setupE2E(t, server.URL, true)

	// A failing command is the engineer's result, not an ocpgate error,
	// but the session must still be torn down.
	stdout, stderr, err := env.run(t, e2ePass+"\n",
		"connect", "prod-cluster-1", "--username", e2eUser, "--insecure-skip-tls-verify", "--", "false")
	require.NoError(t, err, "stderr: %s", stderr)

	assert.Equal(t, []string{"auth_attempt", "session_start", "session_end"}, eventTypes(auditEvents(t, stdout)))

	sessions, _ := os.ReadDir(filepath.Join(env.home, ".cache", "ocpgate", "sessions"))
	assert.Empty(t, sessions, "session directory should be removed even when the command fails")
}

func TestClustersListReadsCache(t *testing.T) {
	server := newFakeCluster(t)
	env := setupE2E(t, server.URL, true)

	stdout, _, err := env.run(t, "", "clusters", "list")
	require.NoError(t, err)

	assert.Contains(t, stdout, "prod-cluster-1")
	assert.Contains(t, stdout, "production")
	assert.Contains(t, stdout, "eu-west")

	stdout, _, err = env.run(t, "", "clusters", "list", "--environment", "test")
	require.NoError(t, err)
	assert.NotContains(t, stdout, "prod-cluster-1")
}

func TestSessionEnvOverridesInheritedKubeconfig(t *testing.T) {
	env := session.Environ(
		[]string{"PATH=/usr/bin", "KUBECONFIG=/home/me/.kube/config"},
		&session.Session{
			ID:             "session-123",
			ClusterName:    "prod-cluster-1",
			KubeconfigPath: "/tmp/session/kubeconfig",
		},
	)

	assert.Contains(t, env, "PATH=/usr/bin")
	assert.Contains(t, env, "KUBECONFIG=/tmp/session/kubeconfig")
	assert.NotContains(t, env, "KUBECONFIG=/home/me/.kube/config")
	assert.Contains(t, env, "OCPGATE_SESSION_ID=session-123")
	assert.Contains(t, env, "OCPGATE_CLUSTER=prod-cluster-1")
}
