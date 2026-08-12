package main

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/oziie/ocpgate/internal/session"
)

func newSessionsCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "sessions",
		Aliases: []string{"session"},
		Short:   "Manage temporary session state",
	}
	cmd.AddCommand(newSessionsPruneCmd(a))
	return cmd
}

// newSessionsPruneCmd cleans up after sessions whose process died without
// running its own cleanup — SIGKILL, a crash, or a lost power cable. The
// tokens inside are expired by then, but the files should not linger.
func newSessionsPruneCmd(a *app) *cobra.Command {
	var olderThan time.Duration

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove leftover session directories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			manager, err := session.NewManager()
			if err != nil {
				return err
			}

			removed, err := manager.PruneStale(olderThan)
			if err != nil {
				return err
			}

			a.infof("removed %d stale session director%s from %s",
				removed, plural(removed), manager.BaseDir())
			return nil
		},
	}

	cmd.Flags().DurationVar(&olderThan, "older-than", 24*time.Hour,
		"only remove sessions untouched for longer than this")

	return cmd
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
