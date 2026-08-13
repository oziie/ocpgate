package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oziie/ocpgate/internal/audit"
)

// Run starts the TUI and blocks until the engineer quits.
//
// The interface renders to stderr so stdout stays reserved for the audit
// JSON stream. Session cleanup happens here rather than inside the model
// because Bubble Tea offers no teardown hook, and the temporary kubeconfig
// has to be removed even when the program exits through Ctrl-C.
func Run(ctx context.Context, deps Deps) error {
	if len(deps.Clusters) == 0 {
		return fmt.Errorf("no clusters in the registry; run `ocpgate clusters sync`")
	}

	program := tea.NewProgram(
		New(deps),
		tea.WithContext(ctx),
		tea.WithOutput(os.Stderr),
		tea.WithAltScreen(),
	)

	finalModel, runErr := program.Run()

	model, ok := finalModel.(Model)
	if ok {
		endSession(model, deps.Audit)
	}

	if runErr != nil && !errors.Is(runErr, tea.ErrProgramKilled) {
		return runErr
	}
	if ok {
		return model.Err()
	}
	return nil
}

// endSession removes the temporary kubeconfig and closes the audit trail.
func endSession(model Model, logger audit.Logger) {
	sess := model.Session()
	manager := model.Sessions()
	if sess == nil || manager == nil {
		return
	}

	cluster := model.Cluster()
	event := audit.AuditEvent{
		EventType:   audit.EventSessionEnd,
		Username:    sess.Username,
		SessionID:   sess.ID,
		TokenExpiry: sess.ExpiresAt,
		Outcome:     audit.OutcomeSuccess,
		Message:     fmt.Sprintf("session lasted %s", time.Since(sess.StartedAt).Round(time.Second)),
	}
	if cluster != nil {
		event.ClusterName = cluster.Name
		event.Environment = cluster.Environment
		event.APIEndpoint = cluster.APIEndpoint
	}

	// Already recorded if the countdown noticed the lapse while the
	// session was still open.
	expired := manager.IsExpired(sess) && !model.TokenExpiryLogged()

	if err := manager.End(sess); err != nil {
		event.Outcome = audit.OutcomeFailure
		event.Message = err.Error()
		fmt.Fprintf(os.Stderr, "warning: could not remove temporary kubeconfig: %v\n", err)
	}
	logger.Log(event)

	if expired {
		logger.Log(audit.AuditEvent{
			EventType:   audit.EventTokenExpired,
			Username:    sess.Username,
			ClusterName: event.ClusterName,
			Environment: event.Environment,
			SessionID:   sess.ID,
			TokenExpiry: sess.ExpiresAt,
			Outcome:     audit.OutcomeSuccess,
			Message:     "token expired before the session ended",
		})
	}

	fmt.Fprintf(os.Stderr, "Session %s ended; temporary kubeconfig removed.\n", sess.ID)
}
