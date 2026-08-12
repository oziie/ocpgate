// Command ocpgate is the entrypoint for the ocpgate CLI.
//
// Output convention: stdout carries audit JSON, one object per line, for
// Fluentd to collect. Human-facing status and prompts go to stderr, so
// piping stdout into a log collector never mixes prose into the stream.
// The exception is data output — the cluster table — which goes to stdout
// because that is what a user piping `ocpgate clusters list` wants.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// A signal during the pre-session phase (registry sync, credential
	// prompt) should abort cleanly. Once a session's subshell is running,
	// connect installs its own handling so the shell owns the terminal.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}
