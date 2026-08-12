// Package styles holds every Lip Gloss style used by the TUI, so visual
// changes happen in one file rather than being scattered across views.
package styles

import "github.com/charmbracelet/lipgloss"

// Palette. Adaptive colors keep the UI readable on both light and dark
// terminals, which matters because engineers run this over SSH into
// whatever terminal they happen to have.
var (
	colorPrimary = lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#7D79F2"}
	colorSubtle  = lipgloss.AdaptiveColor{Light: "#6C6C6C", Dark: "#9B9B9B"}
	colorFaint   = lipgloss.AdaptiveColor{Light: "#A0A0A0", Dark: "#5C5C5C"}
	colorDanger  = lipgloss.AdaptiveColor{Light: "#B3261E", Dark: "#F2837E"}
	colorWarning = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#E3B341"}
	colorSuccess = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#5FD98A"}
	colorInverse = lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#1C1C1C"}
)

// Chrome.
var (
	Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(colorInverse).
		Background(colorPrimary).
		Padding(0, 1)

	Help = lipgloss.NewStyle().Foreground(colorFaint)

	StatusBar = lipgloss.NewStyle().
			Foreground(colorSubtle).
			Padding(0, 1).
			BorderTop(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(colorFaint)

	StatusKey = lipgloss.NewStyle().Foreground(colorFaint)

	StatusValue = lipgloss.NewStyle().Foreground(colorSubtle).Bold(true)

	Error = lipgloss.NewStyle().Foreground(colorDanger).Bold(true)

	Notice = lipgloss.NewStyle().Foreground(colorWarning)

	Body = lipgloss.NewStyle().Padding(1, 2)
)

// Environment badges. Production is deliberately the loudest thing on the
// screen — the whole point of a multi-cluster tool is that you always know
// which one you are about to touch.
var (
	BadgeProduction = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorInverse).
			Background(colorDanger).
			Padding(0, 1)

	BadgeTest = lipgloss.NewStyle().
			Foreground(colorInverse).
			Background(colorSuccess).
			Padding(0, 1)

	BadgeInactive = lipgloss.NewStyle().
			Foreground(colorInverse).
			Background(colorFaint).
			Padding(0, 1)
)

// Forms.
var (
	FieldLabel = lipgloss.NewStyle().Foreground(colorSubtle).Bold(true)

	FieldLabelFocused = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)

	FieldHint = lipgloss.NewStyle().Foreground(colorFaint).Italic(true)
)

// Token expiry states, used by the status bar countdown.
var (
	TokenHealthy  = lipgloss.NewStyle().Foreground(colorSuccess)
	TokenExpiring = lipgloss.NewStyle().Foreground(colorWarning).Bold(true)
	TokenExpired  = lipgloss.NewStyle().Foreground(colorDanger).Bold(true)
)

// Inactive dims a cluster that cannot be connected to.
var Inactive = lipgloss.NewStyle().Foreground(colorFaint).Strikethrough(true)
