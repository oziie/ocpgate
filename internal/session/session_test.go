package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/oziie/ocpgate/internal/auth"
	"github.com/oziie/ocpgate/internal/registry"
)

const testToken = "sha256~xf9Kq2LmN0pQrStUvWxYz"

func testCluster() registry.ClusterEntry {
	return registry.ClusterEntry{
		Name:        "prod-cluster-1",
		APIEndpoint: "https://api.prod-cluster-1.example.com:6443",
		Environment: registry.EnvProduction,
		Region:      "eu-west",
		LDAPRealm:   "PROD",
		Active:      true,
	}
}

func testAuthResult() auth.AuthResult {
	return auth.AuthResult{
		Token:     testToken,
		Username:  "john.doe",
		ExpiresAt: time.Now().Add(time.Hour).UTC(),
	}
}

func newTestManager(t *testing.T, opts ...Option) *FileManager {
	t.Helper()
	opts = append([]Option{WithBaseDir(t.TempDir())}, opts...)
	m, err := NewManager(opts...)
	require.NoError(t, err)
	return m
}

func TestStartWritesUsableKubeconfig(t *testing.T) {
	m := newTestManager(t, WithNamespace("team-a"))

	s, err := m.Start(testCluster(), testAuthResult())
	require.NoError(t, err)

	assert.NotEmpty(t, s.ID)
	assert.Equal(t, "prod-cluster-1", s.ClusterName)
	assert.Equal(t, "production", s.Environment)
	assert.Equal(t, "john.doe", s.Username)
	assert.Equal(t, "team-a", s.Namespace)
	assert.Equal(t, filepath.Join(s.Dir, kubeconfigName), s.KubeconfigPath)
	assert.False(t, s.StartedAt.IsZero())

	cfg, err := clientcmd.LoadFromFile(s.KubeconfigPath)
	require.NoError(t, err)

	assert.Equal(t, "prod-cluster-1", cfg.CurrentContext)
	require.Contains(t, cfg.Clusters, "prod-cluster-1")
	assert.Equal(t, "https://api.prod-cluster-1.example.com:6443", cfg.Clusters["prod-cluster-1"].Server)
	assert.False(t, cfg.Clusters["prod-cluster-1"].InsecureSkipTLSVerify)

	require.Contains(t, cfg.AuthInfos, "john.doe")
	assert.Equal(t, testToken, cfg.AuthInfos["john.doe"].Token)

	require.Contains(t, cfg.Contexts, "prod-cluster-1")
	assert.Equal(t, "team-a", cfg.Contexts["prod-cluster-1"].Namespace)
}

func TestStartUsesOwnerOnlyPermissions(t *testing.T) {
	m := newTestManager(t)

	s, err := m.Start(testCluster(), testAuthResult())
	require.NoError(t, err)

	// The kubeconfig holds a live cluster token; nothing outside the
	// owner may read it, and nothing may traverse into the directory.
	fileInfo, err := os.Stat(s.KubeconfigPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())

	dirInfo, err := os.Stat(s.Dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
}

func TestStartHonorsInsecureSkipTLSVerify(t *testing.T) {
	m := newTestManager(t, WithInsecureSkipTLSVerify(true))

	s, err := m.Start(testCluster(), testAuthResult())
	require.NoError(t, err)

	cfg, err := clientcmd.LoadFromFile(s.KubeconfigPath)
	require.NoError(t, err)
	assert.True(t, cfg.Clusters["prod-cluster-1"].InsecureSkipTLSVerify)
}

func TestStartGeneratesDistinctSessions(t *testing.T) {
	m := newTestManager(t)

	first, err := m.Start(testCluster(), testAuthResult())
	require.NoError(t, err)
	second, err := m.Start(testCluster(), testAuthResult())
	require.NoError(t, err)

	assert.NotEqual(t, first.ID, second.ID)
	assert.NotEqual(t, first.Dir, second.Dir)
}

func TestStartRejectsMissingToken(t *testing.T) {
	m := newTestManager(t)

	result := testAuthResult()
	result.Token = ""

	_, err := m.Start(testCluster(), result)
	require.ErrorContains(t, err, "no token")

	// A failed Start must not leave a directory behind.
	entries, err := os.ReadDir(m.BaseDir())
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestEndRemovesSessionAndIsIdempotent(t *testing.T) {
	m := newTestManager(t)

	s, err := m.Start(testCluster(), testAuthResult())
	require.NoError(t, err)

	require.NoError(t, m.End(s))
	_, err = os.Stat(s.Dir)
	assert.True(t, os.IsNotExist(err), "session directory should be gone")

	// Deferred cleanup and the signal handler may both call End.
	assert.NoError(t, m.End(s))
	assert.NoError(t, m.End(nil))
}

func TestEndRefusesPathsOutsideBaseDir(t *testing.T) {
	m := newTestManager(t)

	outside := t.TempDir()
	canary := filepath.Join(outside, "important.txt")
	require.NoError(t, os.WriteFile(canary, []byte("do not delete"), 0o600))

	err := m.End(&Session{ID: "x", Dir: outside})
	require.ErrorContains(t, err, "outside session directory")

	assert.FileExists(t, canary, "End must not delete directories it does not own")

	err = m.End(&Session{ID: "x", Dir: m.BaseDir()})
	require.ErrorContains(t, err, "refusing to remove session base directory")
	assert.DirExists(t, m.BaseDir())
}

func TestIsExpired(t *testing.T) {
	m := newTestManager(t)

	assert.False(t, m.IsExpired(&Session{ExpiresAt: time.Now().Add(time.Minute)}))
	assert.True(t, m.IsExpired(&Session{ExpiresAt: time.Now().Add(-time.Minute)}))
	assert.False(t, m.IsExpired(&Session{}), "zero expiry means the cluster decides")
	assert.False(t, m.IsExpired(nil))
}

func TestTimeRemaining(t *testing.T) {
	now := time.Now()

	s := &Session{ExpiresAt: now.Add(30 * time.Minute)}
	assert.InDelta(t, (30 * time.Minute).Seconds(), s.TimeRemaining(now).Seconds(), 1)

	assert.Zero(t, (&Session{ExpiresAt: now.Add(-time.Minute)}).TimeRemaining(now))
	assert.Zero(t, (&Session{}).TimeRemaining(now))
}

func TestPruneStale(t *testing.T) {
	m := newTestManager(t)

	stale, err := m.Start(testCluster(), testAuthResult())
	require.NoError(t, err)
	fresh, err := m.Start(testCluster(), testAuthResult())
	require.NoError(t, err)

	// Simulate a session left behind by a process killed with SIGKILL.
	old := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.Chtimes(stale.Dir, old, old))

	removed, err := m.PruneStale(24 * time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	_, err = os.Stat(stale.Dir)
	assert.True(t, os.IsNotExist(err))
	assert.DirExists(t, fresh.Dir)
}

func TestPruneStaleToleratesMissingBaseDir(t *testing.T) {
	m, err := NewManager(WithBaseDir(filepath.Join(t.TempDir(), "never-created")))
	require.NoError(t, err)

	removed, err := m.PruneStale(time.Hour)
	require.NoError(t, err)
	assert.Zero(t, removed)
}

func TestFileManagerSatisfiesManager(t *testing.T) {
	var _ Manager = (*FileManager)(nil)
}
