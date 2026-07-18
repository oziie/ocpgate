package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Cache persists the last-synced cluster set to disk as JSON so the tool
// remains usable (read-only) when GitLab is unreachable.
type Cache struct {
	Path string
}

// cacheFile is the on-disk representation written to Cache.Path.
type cacheFile struct {
	SyncedAt time.Time      `json:"synced_at"`
	Clusters []ClusterEntry `json:"clusters"`
}

// NewCache builds a Cache rooted at path.
func NewCache(path string) *Cache {
	return &Cache{Path: path}
}

// Load reads the cached cluster set. If no cache file exists yet, it
// returns an empty slice and a zero time without error.
func (c *Cache) Load() ([]ClusterEntry, time.Time, error) {
	data, err := os.ReadFile(c.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, time.Time{}, nil
		}
		return nil, time.Time{}, fmt.Errorf("read cache %s: %w", c.Path, err)
	}

	var cf cacheFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil, time.Time{}, fmt.Errorf("parse cache %s: %w", c.Path, err)
	}
	return cf.Clusters, cf.SyncedAt, nil
}

// Save writes the cluster set to disk, creating parent directories as
// needed, and replaces the previous cache atomically via rename.
func (c *Cache) Save(clusters []ClusterEntry, syncedAt time.Time) error {
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}

	data, err := json.MarshalIndent(cacheFile{SyncedAt: syncedAt, Clusters: clusters}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}

	tmp := c.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write cache: %w", err)
	}
	if err := os.Rename(tmp, c.Path); err != nil {
		return fmt.Errorf("finalize cache write: %w", err)
	}
	return nil
}
