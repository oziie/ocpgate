package views

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/oziie/ocpgate/internal/session"
	"github.com/oziie/ocpgate/internal/tui/styles"
)

// expiryWarning is how much token life remains before the countdown starts
// drawing attention to itself.
const expiryWarning = 15 * time.Minute

// StatusBar is the persistent bottom bar. Every field is empty until the
// corresponding step completes, so it doubles as a progress indicator
// through the cluster → credentials → namespace → session flow.
type StatusBar struct {
	Width       int
	Username    string
	Cluster     string
	Environment string
	Namespace   string
	ExpiresAt   time.Time
}

// View renders the bar as of now.
func (s StatusBar) View(now time.Time) string {
	fields := []struct{ key, value string }{
		{"user", or(s.Username)},
		{"cluster", or(s.Cluster)},
		{"namespace", or(s.Namespace)},
	}

	parts := make([]string, 0, len(fields)+1)
	for _, f := range fields {
		parts = append(parts, styles.StatusKey.Render(f.key+": ")+styles.StatusValue.Render(f.value))
	}
	parts = append(parts, styles.StatusKey.Render("token: ")+s.token(now))

	bar := strings.Join(parts, styles.StatusKey.Render("   "))
	if s.Width > 0 {
		return styles.StatusBar.Width(s.Width).Render(bar)
	}
	return styles.StatusBar.Render(bar)
}

// token renders the expiry countdown, escalating in color as it runs down.
func (s StatusBar) token(now time.Time) string {
	if s.ExpiresAt.IsZero() {
		return styles.StatusValue.Render("—")
	}

	remaining := s.ExpiresAt.Sub(now)
	switch {
	case remaining <= 0:
		return styles.TokenExpired.Render("expired")
	case remaining <= expiryWarning:
		return styles.TokenExpiring.Render(FormatRemaining(remaining))
	default:
		return styles.TokenHealthy.Render(FormatRemaining(remaining))
	}
}

// FormatRemaining renders a duration as a compact countdown. It is defined
// in the session package, which owns token lifetime; re-exported here so
// views do not reach across for a formatting helper.
func FormatRemaining(d time.Duration) string {
	return session.FormatRemaining(d)
}

// Help renders a hint line above the status bar.
func Help(width int, hints ...string) string {
	line := styles.Help.Render(strings.Join(hints, styles.Help.Render(" · ")))
	if width > 0 {
		return lipgloss.NewStyle().Width(width).Render(line)
	}
	return line
}

func or(value string) string {
	if value == "" {
		return "—"
	}
	return value
}
