package views

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/oziie/ocpgate/internal/registry"
	"github.com/oziie/ocpgate/internal/tui/styles"
)

// ClusterItem adapts a ClusterEntry to the list component.
type ClusterItem struct {
	Entry registry.ClusterEntry
}

// FilterValue is what `/` searches against. Region and environment are
// included so "eu-west" or "production" narrow the list too, not just the
// cluster name.
func (i ClusterItem) FilterValue() string {
	return strings.Join([]string{i.Entry.Name, i.Entry.Environment, i.Entry.Region}, " ")
}

// clusterDelegate renders one cluster row: a status dot, the name, an
// environment badge, and the region.
type clusterDelegate struct {
	showBadge bool
}

func (d clusterDelegate) Height() int  { return 1 }
func (d clusterDelegate) Spacing() int { return 0 }

func (d clusterDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d clusterDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	cluster, ok := item.(ClusterItem)
	if !ok {
		return
	}
	entry := cluster.Entry

	dot := "○"
	if entry.Environment == registry.EnvProduction {
		dot = "●"
	}

	name := entry.Name
	if !entry.Active {
		name = styles.Inactive.Render(name)
	}

	cursor := "  "
	if index == m.Index() {
		cursor = "> "
		name = lipgloss.NewStyle().Bold(true).Render(name)
	}

	parts := []string{cursor + dot + " " + padRight(name, 28)}
	if d.showBadge {
		parts = append(parts, padRight(EnvironmentBadge(entry), 14))
	}
	parts = append(parts, entry.Region)

	if !entry.Active {
		parts = append(parts, styles.Notice.Render("(auth disabled)"))
	}

	fmt.Fprint(w, strings.Join(parts, " "))
}

// EnvironmentBadge renders the colored environment tag for a cluster.
func EnvironmentBadge(entry registry.ClusterEntry) string {
	if !entry.Active {
		return styles.BadgeInactive.Render("INACTIVE")
	}
	if entry.Environment == registry.EnvProduction {
		return styles.BadgeProduction.Render("PRODUCTION")
	}
	return styles.BadgeTest.Render(strings.ToUpper(entry.Environment))
}

// padRight pads to width using the rendered width, so ANSI color codes in
// the string do not throw the column alignment off.
func padRight(s string, width int) string {
	if pad := width - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// ClusterList is the cluster selection view.
type ClusterList struct {
	list       list.Model
	clusters   []registry.ClusterEntry
	envFilter  string
	showBadge  bool
	filterKey  key.Binding
	lastSynced string
}

// NewClusterList builds the cluster selection view.
func NewClusterList(clusters []registry.ClusterEntry, showBadge bool) ClusterList {
	delegate := clusterDelegate{showBadge: showBadge}

	l := list.New(nil, delegate, 0, 0)
	l.Title = "clusters"
	l.Styles.Title = styles.Title
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(true)
	// The root model owns quitting, so the list must not swallow those keys.
	l.DisableQuitKeybindings()

	filterKey := key.NewBinding(
		key.WithKeys("e"),
		key.WithHelp("e", "cycle environment"),
	)
	l.AdditionalShortHelpKeys = func() []key.Binding { return []key.Binding{filterKey} }

	view := ClusterList{
		list:      l,
		clusters:  clusters,
		showBadge: showBadge,
		filterKey: filterKey,
	}
	view.applyFilter()

	return view
}

// SetSize resizes the underlying list.
func (v *ClusterList) SetSize(width, height int) {
	v.list.SetSize(width, height)
}

// Update handles list navigation, search, and the environment filter.
func (v ClusterList) Update(msg tea.Msg) (ClusterList, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		// While the filter input is open every keystroke is search text,
		// so the environment shortcut must not steal "e".
		if v.list.FilterState() != list.Filtering && key.Matches(keyMsg, v.filterKey) {
			v.cycleEnvironment()
			return v, nil
		}
	}

	var cmd tea.Cmd
	v.list, cmd = v.list.Update(msg)
	return v, cmd
}

// View renders the cluster list.
func (v ClusterList) View() string {
	return v.list.View()
}

// Selected returns the highlighted cluster, or nil when the list is empty.
func (v ClusterList) Selected() *registry.ClusterEntry {
	item, ok := v.list.SelectedItem().(ClusterItem)
	if !ok {
		return nil
	}
	entry := item.Entry
	return &entry
}

// Filtering reports whether the search input is currently capturing keys.
func (v ClusterList) Filtering() bool {
	return v.list.FilterState() == list.Filtering
}

// EnvironmentFilter reports the active environment filter, empty for all.
func (v ClusterList) EnvironmentFilter() string {
	return v.envFilter
}

// cycleEnvironment rotates through all → production → test.
func (v *ClusterList) cycleEnvironment() {
	switch v.envFilter {
	case "":
		v.envFilter = registry.EnvProduction
	case registry.EnvProduction:
		v.envFilter = registry.EnvTest
	default:
		v.envFilter = ""
	}
	v.applyFilter()
}

func (v *ClusterList) applyFilter() {
	items := make([]list.Item, 0, len(v.clusters))
	for _, c := range v.clusters {
		if v.envFilter != "" && c.Environment != v.envFilter {
			continue
		}
		items = append(items, ClusterItem{Entry: c})
	}
	v.list.SetItems(items)

	title := "clusters"
	if v.envFilter != "" {
		title = "clusters · " + v.envFilter
	}
	v.list.Title = title
}
