package registry

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/xanzy/go-gitlab"
	"gopkg.in/yaml.v3"

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

	mu         sync.RWMutex
	clusters   []ClusterEntry
	lastSynced time.Time
}

// NewGitLabRegistry builds a registry client against cfg.GitLabURL /
// cfg.ProjectID, authenticated with token, and preloads the in-memory
// snapshot from the local disk cache so List/Get work before Sync runs.
func NewGitLabRegistry(cfg config.RegistryConfig, token string) (*GitLabRegistry, error) {
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("registry.project_id is not configured")
	}
	if cfg.CachePath == "" {
		return nil, fmt.Errorf("registry.cache_path is not configured")
	}

	opts := []gitlab.ClientOptionFunc{}
	if cfg.GitLabURL != "" {
		opts = append(opts, gitlab.WithBaseURL(cfg.GitLabURL))
	}

	client, err := gitlab.NewClient(token, opts...)
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

	return &GitLabRegistry{
		client:     client,
		projectID:  cfg.ProjectID,
		branch:     branch,
		cache:      cache,
		clusters:   clusters,
		lastSynced: syncedAt,
	}, nil
}

// Sync lists clusters/*.yaml in the configured branch, fetches and parses
// each file, validates the result, and atomically replaces both the
// in-memory snapshot and the on-disk cache.
func (r *GitLabRegistry) Sync(ctx context.Context) error {
	tree, _, err := r.client.Repositories.ListTree(r.projectID, &gitlab.ListTreeOptions{
		Path:      gitlab.Ptr(clustersDir),
		Ref:       gitlab.Ptr(r.branch),
		Recursive: gitlab.Ptr(false),
	}, gitlab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("list cluster-registry tree: %w", err)
	}

	entries := make([]ClusterEntry, 0, len(tree))
	for _, node := range tree {
		if node.Type != "blob" || !isYAML(node.Name) {
			continue
		}

		raw, _, err := r.client.RepositoryFiles.GetRawFile(r.projectID, node.Path, &gitlab.GetRawFileOptions{
			Ref: gitlab.Ptr(r.branch),
		}, gitlab.WithContext(ctx))
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
