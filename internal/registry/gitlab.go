package registry

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xanzy/go-gitlab"
	"gopkg.in/yaml.v3"

	"github.com/oziie/ocpgate/internal/retry"
	"github.com/oziie/ocpgate/pkg/config"
)

// clustersDir is the path, relative to the repo root, that holds cluster
// YAML files in the cluster-registry GitOps repo.
const clustersDir = "clusters"

// GitLabRegistry implements Registry against a GitLab-hosted, GitOps
// managed cluster-registry repository. Reads always come from an
// in-memory snapshot (seeded from local cache at construction, refreshed
// by Sync) so List/Get never block on the network.
type GitLabRegistry struct {
	client    *gitlab.Client
	projectID string
	branch    string
	cache     *Cache
	retry     retry.Policy

	mu         sync.RWMutex
	clusters   []ClusterEntry
	lastSynced time.Time
}

// Option customizes a GitLabRegistry.
type Option func(*GitLabRegistry)

// WithRetryPolicy overrides how transient GitLab failures are retried.
func WithRetryPolicy(p retry.Policy) Option {
	return func(r *GitLabRegistry) { r.retry = p }
}

// NewGitLabRegistry builds a registry client against cfg.GitLabURL /
// cfg.ProjectID, authenticated with token, and preloads the in-memory
// snapshot from the local disk cache so List/Get work before Sync runs.
func NewGitLabRegistry(cfg config.RegistryConfig, token string, opts ...Option) (*GitLabRegistry, error) {
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("registry.project_id is not configured")
	}
	if cfg.CachePath == "" {
		return nil, fmt.Errorf("registry.cache_path is not configured")
	}

	// go-gitlab wraps its transport in retryablehttp with RetryMax 5 and a
	// backoff running to 30s. Left on, that nests inside our own retry
	// loop — up to 15 attempts and minutes of sleeping behind a prompt —
	// and it has no notion of which failures are permanent, so it would
	// happily re-send a request the server already rejected outright.
	// One retry layer, ours, with the transient/definitive distinction.
	clientOpts := []gitlab.ClientOptionFunc{gitlab.WithoutRetries()}
	if cfg.GitLabURL != "" {
		clientOpts = append(clientOpts, gitlab.WithBaseURL(cfg.GitLabURL))
	}

	client, err := gitlab.NewClient(token, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("create gitlab client: %w", err)
	}

	branch := cfg.Branch
	if branch == "" {
		branch = "main"
	}

	cache := NewCache(cfg.CachePath)
	clusters, syncedAt, err := cache.Load()
	if err != nil {
		return nil, err
	}

	r := &GitLabRegistry{
		client:     client,
		projectID:  cfg.ProjectID,
		branch:     branch,
		cache:      cache,
		retry:      retry.DefaultPolicy(),
		clusters:   clusters,
		lastSynced: syncedAt,
	}
	for _, apply := range opts {
		apply(r)
	}

	return r, nil
}

// classifyGitLabError marks failures that another attempt cannot fix. A
// bad token or a missing project stays broken no matter how many times it
// is asked; rate limiting and 5xx are worth waiting out.
func classifyGitLabError(resp *gitlab.Response, err error) error {
	if err == nil {
		return nil
	}
	if resp != nil && resp.Response != nil {
		status := resp.StatusCode
		if status != http.StatusTooManyRequests && status < http.StatusInternalServerError {
			return retry.Permanent(err)
		}
	}
	return err
}

// Sync lists clusters/*.yaml in the configured branch, fetches and parses
// each file, validates the result, and atomically replaces both the
// in-memory snapshot and the on-disk cache.
func (r *GitLabRegistry) Sync(ctx context.Context) error {
	var tree []*gitlab.TreeNode
	err := retry.Do(ctx, r.retry, func(ctx context.Context) error {
		nodes, resp, err := r.client.Repositories.ListTree(r.projectID, &gitlab.ListTreeOptions{
			Path:      gitlab.Ptr(clustersDir),
			Ref:       gitlab.Ptr(r.branch),
			Recursive: gitlab.Ptr(false),
		}, gitlab.WithContext(ctx))
		if err != nil {
			return classifyGitLabError(resp, err)
		}
		tree = nodes
		return nil
	})
	if err != nil {
		return fmt.Errorf("list cluster-registry tree: %w", err)
	}

	entries := make([]ClusterEntry, 0, len(tree))
	for _, node := range tree {
		if node.Type != "blob" || !isYAML(node.Name) {
			continue
		}

		var raw []byte
		err := retry.Do(ctx, r.retry, func(ctx context.Context) error {
			content, resp, err := r.client.RepositoryFiles.GetRawFile(r.projectID, node.Path, &gitlab.GetRawFileOptions{
				Ref: gitlab.Ptr(r.branch),
			}, gitlab.WithContext(ctx))
			if err != nil {
				return classifyGitLabError(resp, err)
			}
			raw = content
			return nil
		})
		if err != nil {
			return fmt.Errorf("fetch %s: %w", node.Path, err)
		}

		var entry ClusterEntry
		if err := yaml.Unmarshal(raw, &entry); err != nil {
			return fmt.Errorf("parse %s: %w", node.Path, err)
		}
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("%s: %w", node.Path, err)
		}
		entries = append(entries, entry)
	}

	if err := validateUnique(entries); err != nil {
		return fmt.Errorf("cluster-registry validation failed: %w", err)
	}
	sortByName(entries)

	syncedAt := time.Now().UTC()
	if err := r.cache.Save(entries, syncedAt); err != nil {
		return fmt.Errorf("save cluster cache: %w", err)
	}

	r.mu.Lock()
	r.clusters = entries
	r.lastSynced = syncedAt
	r.mu.Unlock()

	return nil
}

// List returns all known clusters, sorted by name.
func (r *GitLabRegistry) List() ([]ClusterEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ClusterEntry, len(r.clusters))
	copy(out, r.clusters)
	return out, nil
}

// Get returns a single cluster by exact, case-insensitive name match.
func (r *GitLabRegistry) Get(name string) (*ClusterEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, c := range r.clusters {
		if strings.EqualFold(c.Name, name) {
			entry := c
			return &entry, nil
		}
	}
	return nil, &ErrClusterNotFound{Name: name}
}

// LastSynced reports when Sync last completed successfully.
func (r *GitLabRegistry) LastSynced() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastSynced
}

func isYAML(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}
