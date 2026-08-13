package registry

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/oziie/ocpgate/internal/retry"
	"github.com/oziie/ocpgate/pkg/config"
)

// fakeGitLab serves the two endpoints Sync uses: the repository tree and
// raw file contents.
type fakeGitLab struct {
	files map[string]string // path -> YAML content

	treeCalls int
	fileCalls int

	// transientFailures makes the next N tree requests return 503.
	transientFailures int
	// treeStatus, when non-zero, is returned instead of the tree.
	treeStatus int
}

func (f *fakeGitLab) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/repository/tree"):
		f.treeCalls++

		if f.transientFailures > 0 {
			f.transientFailures--
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if f.treeStatus != 0 {
			w.WriteHeader(f.treeStatus)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		nodes := make([]string, 0, len(f.files))
		for path := range f.files {
			nodes = append(nodes, fmt.Sprintf(
				`{"id":"a1","name":%q,"type":"blob","path":%q,"mode":"100644"}`,
				filepath.Base(path), path))
		}
		fmt.Fprintf(w, "[%s]", strings.Join(nodes, ","))

	case strings.HasSuffix(r.URL.Path, "/raw"):
		f.fileCalls++

		for path, content := range f.files {
			if strings.Contains(r.URL.Path, strings.ReplaceAll(path, "/", "%2F")) ||
				strings.Contains(r.URL.Path, path) {
				fmt.Fprint(w, content)
				return
			}
		}
		http.NotFound(w, r)

	default:
		http.NotFound(w, r)
	}
}

func clusterYAML(name, environment string, active bool) string {
	return fmt.Sprintf(`name: %s
api_endpoint: https://api.%s.example.com:6443
environment: %s
region: eu-west
ldap_realm: PROD
active: %t
`, name, name, environment, active)
}

func newTestRegistry(t *testing.T, fake *fakeGitLab) (*GitLabRegistry, string) {
	t.Helper()

	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	cachePath := filepath.Join(t.TempDir(), "clusters.json")
	reg, err := NewGitLabRegistry(config.RegistryConfig{
		GitLabURL: server.URL,
		ProjectID: "123",
		Branch:    "main",
		CachePath: cachePath,
	}, "test-token",
		// Same attempt count as production, without the real backoff.
		WithRetryPolicy(retry.Policy{Attempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}),
	)
	require.NoError(t, err)

	return reg, cachePath
}

func TestSyncFetchesValidatesAndCaches(t *testing.T) {
	fake := &fakeGitLab{files: map[string]string{
		"clusters/prod-cluster-1.yaml": clusterYAML("prod-cluster-1", EnvProduction, true),
		"clusters/test-cluster-1.yaml": clusterYAML("test-cluster-1", EnvTest, false),
	}}
	reg, cachePath := newTestRegistry(t, fake)

	require.NoError(t, reg.Sync(context.Background()))

	clusters, err := reg.List()
	require.NoError(t, err)
	require.Len(t, clusters, 2)

	// Sorted by name, so the list view is stable between syncs.
	assert.Equal(t, "prod-cluster-1", clusters[0].Name)
	assert.Equal(t, "test-cluster-1", clusters[1].Name)
	assert.Equal(t, "https://api.prod-cluster-1.example.com:6443", clusters[0].APIEndpoint)
	assert.True(t, clusters[0].Active)
	assert.False(t, clusters[1].Active)

	assert.False(t, reg.LastSynced().IsZero())

	// The cache is written so the next run works offline.
	cached, syncedAt, err := NewCache(cachePath).Load()
	require.NoError(t, err)
	assert.Len(t, cached, 2)
	assert.False(t, syncedAt.IsZero())
}

func TestSyncIgnoresNonYAMLFiles(t *testing.T) {
	fake := &fakeGitLab{files: map[string]string{
		"clusters/prod-cluster-1.yaml": clusterYAML("prod-cluster-1", EnvProduction, true),
		"clusters/README.md":           "not a cluster",
	}}
	reg, _ := newTestRegistry(t, fake)

	require.NoError(t, reg.Sync(context.Background()))

	clusters, err := reg.List()
	require.NoError(t, err)
	assert.Len(t, clusters, 1)
}

func TestSyncRetriesTransientGitLabFailure(t *testing.T) {
	fake := &fakeGitLab{
		files:             map[string]string{"clusters/a.yaml": clusterYAML("a", EnvTest, true)},
		transientFailures: 2,
	}
	reg, _ := newTestRegistry(t, fake)

	require.NoError(t, reg.Sync(context.Background()))
	assert.Equal(t, 3, fake.treeCalls, "two failures then success")
}

func TestSyncDoesNotRetryAuthorizationFailure(t *testing.T) {
	fake := &fakeGitLab{
		files:      map[string]string{"clusters/a.yaml": clusterYAML("a", EnvTest, true)},
		treeStatus: http.StatusUnauthorized,
	}
	reg, _ := newTestRegistry(t, fake)

	err := reg.Sync(context.Background())
	require.Error(t, err)

	// A rejected token will still be rejected on the next attempt.
	assert.Equal(t, 1, fake.treeCalls)
}

func TestSyncGivesUpAfterRepeatedTransientFailures(t *testing.T) {
	fake := &fakeGitLab{
		files:             map[string]string{"clusters/a.yaml": clusterYAML("a", EnvTest, true)},
		transientFailures: 99,
	}
	reg, _ := newTestRegistry(t, fake)

	err := reg.Sync(context.Background())
	require.Error(t, err)
	assert.Equal(t, 3, fake.treeCalls)
}

func TestSyncRejectsInvalidClusterAndKeepsPreviousState(t *testing.T) {
	fake := &fakeGitLab{files: map[string]string{
		"clusters/good.yaml": clusterYAML("good", EnvProduction, true),
	}}
	reg, _ := newTestRegistry(t, fake)
	require.NoError(t, reg.Sync(context.Background()))

	// A bad entry lands in the registry repo.
	fake.files["clusters/bad.yaml"] = clusterYAML("bad", "staging", true)

	err := reg.Sync(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "environment")

	// The previously synced list must survive a bad sync, so one broken
	// merge request cannot cut everyone off from every cluster.
	clusters, listErr := reg.List()
	require.NoError(t, listErr)
	require.Len(t, clusters, 1)
	assert.Equal(t, "good", clusters[0].Name)
}

func TestSyncRejectsDuplicateNames(t *testing.T) {
	fake := &fakeGitLab{files: map[string]string{
		"clusters/one.yaml": clusterYAML("same-name", EnvProduction, true),
		"clusters/two.yaml": clusterYAML("same-name", EnvTest, true),
	}}
	reg, _ := newTestRegistry(t, fake)

	err := reg.Sync(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestGetIsCaseInsensitiveAndReportsMissing(t *testing.T) {
	fake := &fakeGitLab{files: map[string]string{
		"clusters/prod-cluster-1.yaml": clusterYAML("prod-cluster-1", EnvProduction, true),
	}}
	reg, _ := newTestRegistry(t, fake)
	require.NoError(t, reg.Sync(context.Background()))

	found, err := reg.Get("PROD-Cluster-1")
	require.NoError(t, err)
	assert.Equal(t, "prod-cluster-1", found.Name)

	_, err = reg.Get("nope")
	require.Error(t, err)

	var notFound *ErrClusterNotFound
	assert.ErrorAs(t, err, &notFound)
}

func TestRegistrySeedsFromCacheBeforeFirstSync(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "clusters.json")

	seeded := []ClusterEntry{{
		Name: "cached-cluster", APIEndpoint: "https://api.cached.example.com:6443",
		Environment: EnvProduction, Active: true,
	}}
	require.NoError(t, NewCache(cachePath).Save(seeded, time.Now().UTC()))

	// No server involved: this is the offline path.
	reg, err := NewGitLabRegistry(config.RegistryConfig{
		GitLabURL: "https://gitlab.invalid",
		ProjectID: "123",
		CachePath: cachePath,
	}, "")
	require.NoError(t, err)

	clusters, err := reg.List()
	require.NoError(t, err)
	require.Len(t, clusters, 1)
	assert.Equal(t, "cached-cluster", clusters[0].Name)
	assert.False(t, reg.LastSynced().IsZero())
}

func TestSyncRespectsContextCancellation(t *testing.T) {
	fake := &fakeGitLab{files: map[string]string{"clusters/a.yaml": clusterYAML("a", EnvTest, true)}}
	reg, _ := newTestRegistry(t, fake)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.Error(t, reg.Sync(ctx))
	assert.Zero(t, fake.treeCalls, "must not call GitLab once the context is done")
}
