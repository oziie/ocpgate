package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/oziie/ocpgate/internal/audit"
	"github.com/oziie/ocpgate/internal/auth"
	"github.com/oziie/ocpgate/internal/registry"
	"github.com/oziie/ocpgate/internal/session"
)

func newConnectCmd(a *app) *cobra.Command {
	var username, namespace string

	cmd := &cobra.Command{
		Use:   "connect <cluster> [-- command [args...]]",
		Short: "Authenticate to a cluster and open an audited session",
		Long: `Authenticate to a cluster and open a session.

You are prompted for LDAP credentials, which are exchanged for a short-lived
Bearer token from the cluster's OAuth endpoint. A temporary kubeconfig is
written to ~/.cache/ocpgate/sessions/<session-id>/ and KUBECONFIG is pointed
at it for a subshell. When the shell exits the kubeconfig is deleted.

With a trailing command after --, that command runs in place of the shell and
the session ends as soon as it returns.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runConnect(cmd, args[0], args[1:], username, namespace)
		},
	}

	cmd.Flags().StringVarP(&username, "username", "u", "", "LDAP username (prompted if omitted)")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "namespace to select in the generated kubeconfig")

	return cmd
}

func (a *app) runConnect(cmd *cobra.Command, clusterName string, command []string, username, namespace string) error {
	ctx := cmd.Context()

	reg, err := a.registryClient()
	if err != nil {
		return err
	}
	if err := a.syncRegistry(ctx, reg, false); err != nil {
		return err
	}

	cluster, err := reg.Get(clusterName)
	if err != nil {
		return err
	}
	// Checked here as well as in the authenticator so an inactive cluster
	// fails before the engineer is asked to type a password.
	if !cluster.Active {
		return &auth.ErrClusterInactive{Name: cluster.Name}
	}

	creds, err := a.collectCredentials(username, *cluster)
	if err != nil {
		return err
	}

	result, err := a.authenticate(ctx, *cluster, creds)
	if err != nil {
		return err
	}

	if namespace == "" {
		namespace = a.cfg.TUI.DefaultNamespace
	}

	manager, err := session.NewManager(
		session.WithNamespace(namespace),
		session.WithInsecureSkipTLSVerify(a.insecureSkipTLSVerify),
	)
	if err != nil {
		return err
	}

	sess, err := manager.Start(*cluster, *result)
	if err != nil {
		return err
	}

	a.audit.Log(audit.AuditEvent{
		EventType:   audit.EventSessionStart,
		Username:    sess.Username,
		ClusterName: cluster.Name,
		Environment: cluster.Environment,
		APIEndpoint: cluster.APIEndpoint,
		SessionID:   sess.ID,
		TokenExpiry: sess.ExpiresAt,
		Outcome:     audit.OutcomeSuccess,
	})

	// Cleanup runs regardless of how the session body exits, so the
	// kubeconfig never outlives the process that created it.
	defer a.endSession(manager, sess, *cluster)

	a.printSessionBanner(sess, *cluster, len(command) > 0)

	return runInSession(sess, command)
}

// collectCredentials fills in whatever the flags did not supply.
func (a *app) collectCredentials(username string, cluster registry.ClusterEntry) (auth.Credentials, error) {
	a.infof("Connecting to %s (%s)", cluster.Name, cluster.Environment)

	// One prompter for both reads, so a piped password survives the
	// username read's buffering.
	p := newPrompter(os.Stdin, os.Stderr)

	var err error
	if username == "" {
		username, err = p.Username(defaultUsername())
		if err != nil {
			return auth.Credentials{}, err
		}
	}

	password, err := p.Password()
	if err != nil {
		return auth.Credentials{}, err
	}

	return auth.Credentials{Username: username, Password: password}, nil
}

// authenticate performs the OAuth exchange and audits the outcome. The
// password is confined to this call: it is never placed in an audit event
// and never included in a returned error.
func (a *app) authenticate(ctx context.Context, cluster registry.ClusterEntry, creds auth.Credentials) (*auth.AuthResult, error) {
	a.audit.Log(audit.AuditEvent{
		EventType:   audit.EventAuthAttempt,
		Username:    creds.Username,
		ClusterName: cluster.Name,
		Environment: cluster.Environment,
		APIEndpoint: cluster.APIEndpoint,
	})

	authenticator := auth.NewOCPAuthenticator(
		auth.WithInsecureSkipTLSVerify(a.insecureSkipTLSVerify),
	)

	result, err := authenticator.Authenticate(ctx, cluster, creds)
	if err != nil {
		a.audit.Log(audit.AuditEvent{
			EventType:   audit.EventAuthFailure,
			Username:    creds.Username,
			ClusterName: cluster.Name,
			Environment: cluster.Environment,
			APIEndpoint: cluster.APIEndpoint,
			Outcome:     audit.OutcomeFailure,
			Message:     err.Error(),
		})
		return nil, err
	}
	return result, nil
}

// endSession removes the temporary kubeconfig and closes the audit trail.
func (a *app) endSession(manager session.Manager, sess *session.Session, cluster registry.ClusterEntry) {
	expired := manager.IsExpired(sess)

	event := audit.AuditEvent{
		EventType:   audit.EventSessionEnd,
		Username:    sess.Username,
		ClusterName: cluster.Name,
		Environment: cluster.Environment,
		APIEndpoint: cluster.APIEndpoint,
		SessionID:   sess.ID,
		TokenExpiry: sess.ExpiresAt,
		Outcome:     audit.OutcomeSuccess,
		Message:     fmt.Sprintf("session lasted %s", time.Since(sess.StartedAt).Round(time.Second)),
	}

	if err := manager.End(sess); err != nil {
		event.Outcome = audit.OutcomeFailure
		event.Message = err.Error()
		a.warnf("could not remove temporary kubeconfig: %v", err)
	}
	a.audit.Log(event)

	// Recorded separately so an expiry is searchable in OpenSearch
	// without parsing session durations.
	if expired {
		a.audit.Log(audit.AuditEvent{
			EventType:   audit.EventTokenExpired,
			Username:    sess.Username,
			ClusterName: cluster.Name,
			Environment: cluster.Environment,
			SessionID:   sess.ID,
			TokenExpiry: sess.ExpiresAt,
			Outcome:     audit.OutcomeSuccess,
			Message:     "token expired before the session ended",
		})
	}

	a.infof("Session %s ended; temporary kubeconfig removed.", sess.ID)
}

func (a *app) printSessionBanner(sess *session.Session, cluster registry.ClusterEntry, oneShot bool) {
	a.infof("")
	a.infof("  cluster:   %s (%s)", cluster.Name, cluster.Environment)
	a.infof("  user:      %s", sess.Username)
	if sess.Namespace != "" {
		a.infof("  namespace: %s", sess.Namespace)
	}
	if !sess.ExpiresAt.IsZero() {
		a.infof("  token:     expires in %s (%s)",
			sess.TimeRemaining(time.Now()).Round(time.Minute),
			sess.ExpiresAt.Local().Format(time.RFC1123))
	}
	a.infof("  session:   %s", sess.ID)
	if !oneShot {
		a.infof("")
		a.infof("Type `exit` to end the session and delete the temporary kubeconfig.")
	}
	a.infof("")
}

// runInSession runs the engineer's shell — or the trailing command — with
// KUBECONFIG pointed at the session's temporary config.
func runInSession(sess *session.Session, command []string) error {
	var cmd *exec.Cmd
	if len(command) > 0 {
		cmd = exec.Command(command[0], command[1:]...)
	} else {
		cmd = exec.Command(session.LoginShell())
	}

	cmd.Env = session.Environ(os.Environ(), sess)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// While the child owns the terminal, Ctrl-C belongs to it: the parent
	// must not tear down the kubeconfig out from under a running command.
	// SIGTERM is still honored, forwarded so the child can exit cleanly.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case sig := <-signals:
				if sig == syscall.SIGTERM && cmd.Process != nil {
					_ = cmd.Process.Signal(sig)
				}
			case <-done:
				return
			}
		}
	}()

	err := cmd.Run()

	// A non-zero exit from the engineer's own shell or command is their
	// result, not an ocpgate failure — the session still ends normally.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}
