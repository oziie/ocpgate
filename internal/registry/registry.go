// Package registry provides access to the ocpgate cluster registry: a
// GitLab-hosted, GitOps-managed set of ClusterEntry YAML files, synced to
// a local disk cache so the TUI has something to show even when GitLab is
// unreachable.
package registry

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Registry is the contract the TUI and CLI depend on for cluster lookup.
type Registry interface {
	// Sync fetches the latest cluster set from the source of truth and
	// refreshes the local cache.
	Sync(ctx context.Context) error
	// List returns all known clusters, sorted by name.
	List() ([]ClusterEntry, error)
	// Get returns a single cluster by exact name.
	Get(name string) (*ClusterEntry, error)
	// LastSynced reports when Sync last completed successfully.
	LastSynced() time.Time
}

// ErrClusterNotFound is returned by Get when no cluster matches the name.
type ErrClusterNotFound struct {
	Name string
}

func (e *ErrClusterNotFound) Error() string {
	return fmt.Sprintf("cluster %q not found in registry", e.Name)
}

func validateAPIEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid api_endpoint: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("invalid api_endpoint %q: scheme must be https", endpoint)
	}
	if u.Port() == "" {
		return fmt.Errorf("invalid api_endpoint %q: must include a port", endpoint)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("invalid api_endpoint %q: missing host", endpoint)
	}
	return nil
}

// validateUnique checks that no two entries share a name, matching the
// cluster-registry schema rule that names must be unique across all files.
func validateUnique(entries []ClusterEntry) error {
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		key := strings.ToLower(e.Name)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate cluster name: %q", e.Name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func sortByName(entries []ClusterEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
}
