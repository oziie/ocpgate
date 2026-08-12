// Package session turns an OCP Bearer token into a working kubectl/oc
// environment: a temporary, owner-only kubeconfig under
// ~/.cache/ocpgate/sessions/<session-id>/ that is deleted when the session
// ends. Nothing here is designed to survive the process — the kubeconfig
// is a convenience wrapper around a token that expires on its own.
package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/oziie/ocpgate/internal/auth"
	"github.com/oziie/ocpgate/internal/registry"
)

// Manager is the session lifecycle contract used by the CLI and TUI.
type Manager interface {
	// Start writes a temporary kubeconfig for the authenticated cluster.
	Start(cluster registry.ClusterEntry, result auth.AuthResult) (*Session, error)
	// End removes the session's on-disk state. It is safe to call more
	// than once, so a deferred End and a signal handler can both fire.
	End(session *Session) error
	// IsExpired reports whether the session's token has expired.
	IsExpired(session *Session) bool
}

// FileManager implements Manager against the local filesystem.
type FileManager struct {
	baseDir               string
	namespace             string
	insecureSkipTLSVerify bool
}

type managerOptions struct {
	baseDir               string
	namespace             string
	insecureSkipTLSVerify bool
}

// Option customizes a FileManager.
type Option func(*managerOptions)

// WithBaseDir overrides the directory sessions are created under.
func WithBaseDir(dir string) Option {
	return func(o *managerOptions) { o.baseDir = dir }
}

// WithNamespace sets the namespace recorded in generated kubeconfigs.
func WithNamespace(ns string) Option {
	return func(o *managerOptions) { o.namespace = ns }
}

// WithInsecureSkipTLSVerify marks generated kubeconfigs as skipping
// certificate verification. This should mirror how authentication was
// performed, so kubectl behaves the same way ocpgate just did.
func WithInsecureSkipTLSVerify(skip bool) Option {
	return func(o *managerOptions) { o.insecureSkipTLSVerify = skip }
}

// DefaultBaseDir returns ~/.cache/ocpgate/sessions.
func DefaultBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "ocpgate", "sessions"), nil
}

// NewManager builds a FileManager, defaulting to ~/.cache/ocpgate/sessions.
func NewManager(opts ...Option) (*FileManager, error) {
	var o managerOptions
	for _, apply := range opts {
		apply(&o)
	}

	if o.baseDir == "" {
		dir, err := DefaultBaseDir()
		if err != nil {
			return nil, err
		}
		o.baseDir = dir
	}

	return &FileManager{
		baseDir:               filepath.Clean(o.baseDir),
		namespace:             o.namespace,
		insecureSkipTLSVerify: o.insecureSkipTLSVerify,
	}, nil
}

// BaseDir reports the directory sessions are created under.
func (m *FileManager) BaseDir() string { return m.baseDir }

// Start creates a session directory and writes the kubeconfig into it. If
// the kubeconfig cannot be written the directory is removed again, so a
// failed Start leaves nothing behind for the caller to clean up.
func (m *FileManager) Start(cluster registry.ClusterEntry, result auth.AuthResult) (*Session, error) {
	if result.Token == "" {
		return nil, fmt.Errorf("cannot start session: auth result has no token")
	}

	id := uuid.NewString()
	dir := filepath.Join(m.baseDir, id)

	// 0700 throughout: the kubeconfig below holds a live cluster token,
	// so other users on a shared machine must not be able to traverse in.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}

	kubeconfigPath := filepath.Join(dir, kubeconfigName)
	cfg := buildKubeconfig(cluster, result, kubeconfigOptions{
		namespace:             m.namespace,
		insecureSkipTLSVerify: m.insecureSkipTLSVerify,
	})
	if err := writeKubeconfig(kubeconfigPath, cfg); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}

	return &Session{
		ID:             id,
		ClusterName:    cluster.Name,
		Environment:    cluster.Environment,
		Username:       result.Username,
		Namespace:      m.namespace,
		Dir:            dir,
		KubeconfigPath: kubeconfigPath,
		StartedAt:      time.Now().UTC(),
		ExpiresAt:      result.ExpiresAt,
	}, nil
}

// End removes the session directory. It tolerates a nil session and an
// already-removed directory, because cleanup is driven from both a defer
// and a signal handler and either may win the race.
func (m *FileManager) End(s *Session) error {
	if s == nil || s.Dir == "" {
		return nil
	}
	if err := m.assertOwnedDir(s.Dir); err != nil {
		return err
	}

	if err := os.RemoveAll(s.Dir); err != nil {
		return fmt.Errorf("remove session directory %s: %w", s.Dir, err)
	}
	return nil
}

// IsExpired reports whether the session's token has expired.
func (m *FileManager) IsExpired(s *Session) bool {
	if s == nil || s.ExpiresAt.IsZero() {
		return false
	}
	return !time.Now().Before(s.ExpiresAt)
}

// PruneStale removes session directories older than maxAge, cleaning up
// after processes that were killed hard enough to skip their own cleanup
// (SIGKILL, power loss). It returns the number of directories removed.
// A missing base directory is not an error — nothing has run yet.
func (m *FileManager) PruneStale(maxAge time.Duration) (int, error) {
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read session directory: %w", err)
	}

	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(m.baseDir, entry.Name())); err == nil {
			removed++
		}
	}
	return removed, nil
}

// assertOwnedDir refuses to delete anything outside the manager's base
// directory. End takes a caller-supplied path and hands it to RemoveAll,
// so this is the guard that keeps a malformed Session from turning into a
// recursive delete of an arbitrary directory.
func (m *FileManager) assertOwnedDir(dir string) error {
	clean := filepath.Clean(dir)
	if clean == m.baseDir {
		return fmt.Errorf("refusing to remove session base directory %s", m.baseDir)
	}

	rel, err := filepath.Rel(m.baseDir, clean)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("refusing to remove %s: outside session directory %s", clean, m.baseDir)
	}
	return nil
}
