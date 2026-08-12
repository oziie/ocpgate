package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/oziie/ocpgate/internal/audit"
	"github.com/oziie/ocpgate/internal/registry"
	"github.com/oziie/ocpgate/pkg/config"
	"github.com/oziie/ocpgate/pkg/version"
)

// app holds the dependencies shared by every subcommand. It is populated
// once in the root command's PersistentPreRunE so each command can assume
// config and auditing are ready.
type app struct {
	configPath            string
	insecureSkipTLSVerify bool

	cfg   *config.Config
	audit audit.Logger

	// gitlabTokenErr records why the GitLab token was unavailable, if it
	// was. This is not fatal on its own: the local cluster cache keeps the
	// tool usable read-only, so it degrades a sync into a warning.
	gitlabTokenErr error
}

func newRootCmd() *cobra.Command {
	a := &app{}

	root := &cobra.Command{
		Use:   "ocpgate",
		Short: "Audited, single-entrypoint access to OCP clusters",
		Long: `ocpgate is a single entrypoint for accessing OpenShift clusters.

It reads the cluster list from a GitOps-managed GitLab registry, authenticates
you against the selected cluster's LDAP-backed OAuth endpoint, and drops you
into a shell with a temporary kubeconfig that is deleted when you exit. Every
access is emitted as a structured JSON audit event on stdout.

No kubeconfig, password, or token is ever written to a permanent location.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return a.setup()
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runTUI(cmd)
		},
	}

	root.PersistentFlags().StringVar(&a.configPath, "config", "",
		"path to config file (default ~/.config/ocpgate/config.yaml)")
	root.PersistentFlags().BoolVar(&a.insecureSkipTLSVerify, "insecure-skip-tls-verify", false,
		"skip verification of the cluster's certificate chain")

	root.AddCommand(
		newVersionCmd(),
		newClustersCmd(a),
		newConnectCmd(a),
		newSessionsCmd(a),
	)

	return root
}

// setup loads configuration and builds the audit logger.
func (a *app) setup() error {
	cfg, err := config.Load(a.configPath)
	if err != nil {
		return err
	}
	a.cfg = cfg

	if cfg.Audit.Enabled {
		a.audit = audit.NewStdoutLogger()
	} else {
		a.audit = audit.NopLogger{}
	}
	return nil
}

// registryClient builds the cluster registry. A missing GitLab token is
// remembered rather than returned, so read-only commands can still serve
// the local cache when the token is absent.
func (a *app) registryClient() (*registry.GitLabRegistry, error) {
	if a.cfg.Registry.ProjectID == "" {
		path := a.configPath
		if path == "" {
			path, _ = config.DefaultConfigPath()
		}
		return nil, fmt.Errorf("registry.project_id is not set; configure the cluster registry in %s", path)
	}

	token, err := a.cfg.GitLabToken()
	a.gitlabTokenErr = err

	return registry.NewGitLabRegistry(a.cfg.Registry, token)
}

// syncRegistry refreshes the cluster list from GitLab. When force is
// false, the sync is advisory: a failure falls back to the cached cluster
// list with a warning, because an unreachable GitLab should not stop an
// engineer from reaching a cluster they already know about. When force is
// true (an explicit `clusters sync`), failures are returned.
func (a *app) syncRegistry(ctx context.Context, reg *registry.GitLabRegistry, force bool) error {
	if !force && !a.cfg.Registry.SyncOnStart {
		return nil
	}

	if a.gitlabTokenErr != nil {
		if force {
			return fmt.Errorf("cannot sync cluster registry: %w", a.gitlabTokenErr)
		}
		a.warnf("skipping registry sync: %v", a.gitlabTokenErr)
		return nil
	}

	err := reg.Sync(ctx)

	event := audit.AuditEvent{EventType: audit.EventRegistrySync, Outcome: audit.OutcomeSuccess}
	if err != nil {
		event.Outcome = audit.OutcomeFailure
		event.Message = err.Error()
	}
	a.audit.Log(event)

	if err != nil {
		cached, listErr := reg.List()
		if !force && listErr == nil && len(cached) > 0 {
			a.warnf("registry sync failed, using cached cluster list: %v", err)
			return nil
		}
		return err
	}
	return nil
}

// warnf writes a status line to stderr, keeping stdout clean for audit
// JSON and data output.
func (a *app) warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
}

// infof writes an informational line to stderr, for the same reason.
func (a *app) infof(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the ocpgate version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version.String())
			return err
		},
	}
}
