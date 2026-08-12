package main

import (
	"context"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/oziie/ocpgate/internal/auth"
	"github.com/oziie/ocpgate/internal/ocp"
	"github.com/oziie/ocpgate/internal/registry"
	"github.com/oziie/ocpgate/internal/session"
	"github.com/oziie/ocpgate/internal/tui"
)

// runTUI launches the interactive interface, which is what `ocpgate` with
// no subcommand does. Without a terminal there is nothing to render, so it
// falls back to help rather than failing on a raw-mode error — that is the
// path a CI job or an invocation in a pipe takes.
//
// Both ends are checked: stdin because the TUI reads keys from it, and
// stderr because that is what the TUI renders to. Checking only stdin
// would let `ocpgate 2>audit-run.log` get past the guard and then fail
// inside Bubble Tea with an opaque terminal error.
func (a *app) runTUI(cmd *cobra.Command) error {
	if !isTerminal(os.Stdin) || !isTerminal(os.Stderr) {
		return cmd.Help()
	}

	ctx := cmd.Context()

	reg, err := a.registryClient()
	if err != nil {
		return err
	}
	if err := a.syncRegistry(ctx, reg, false); err != nil {
		return err
	}

	clusters, err := reg.List()
	if err != nil {
		return err
	}

	return tui.Run(ctx, tui.Deps{
		Clusters: clusters,
		Authenticator: auth.NewOCPAuthenticator(
			auth.WithInsecureSkipTLSVerify(a.insecureSkipTLSVerify),
		),
		NewSessions: func(namespace string) (session.Manager, error) {
			return session.NewManager(
				session.WithNamespace(namespace),
				session.WithInsecureSkipTLSVerify(a.insecureSkipTLSVerify),
			)
		},
		ListNamespaces: func(ctx context.Context, cluster registry.ClusterEntry, token string) ([]string, error) {
			return ocp.ListNamespaces(ctx, cluster, token, a.insecureSkipTLSVerify)
		},
		Audit:            a.audit,
		DefaultUsername:  defaultUsername(),
		DefaultNamespace: a.cfg.TUI.DefaultNamespace,
		ShowBadge:        a.cfg.TUI.ShowEnvironmentBadge,
	})
}

func isTerminal(f *os.File) bool {
	return f != nil && term.IsTerminal(int(f.Fd()))
}
