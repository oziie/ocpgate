package session

import (
	"os"
	"strings"
)

// Environ returns base with the session's kubeconfig applied, for handing
// to a subshell or a one-shot command.
//
// Any inherited KUBECONFIG is dropped rather than appended after: which
// entry wins when a variable appears twice is not guaranteed across
// platforms, and pointing kubectl at the wrong cluster is exactly the
// failure this tool exists to prevent.
func Environ(base []string, s *Session) []string {
	out := make([]string, 0, len(base)+3)
	for _, kv := range base {
		if strings.HasPrefix(kv, "KUBECONFIG=") {
			continue
		}
		out = append(out, kv)
	}

	return append(out,
		"KUBECONFIG="+s.KubeconfigPath,
		"OCPGATE_SESSION_ID="+s.ID,
		"OCPGATE_CLUSTER="+s.ClusterName,
	)
}

// LoginShell returns the engineer's shell, falling back to /bin/sh.
func LoginShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}
