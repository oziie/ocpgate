package views

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/oziie/ocpgate/internal/tui/keys"
	"github.com/oziie/ocpgate/internal/tui/styles"
)

// namespaceItem is one selectable namespace.
type namespaceItem string

func (i namespaceItem) FilterValue() string { return string(i) }

type namespaceDelegate struct{}

func (namespaceDelegate) Height() int                         { return 1 }
func (namespaceDelegate) Spacing() int                        { return 0 }
func (namespaceDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (namespaceDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	name, ok := item.(namespaceItem)
	if !ok {
		return
	}

	line := "  " + string(name)
	if index == m.Index() {
		line = "> " + lipgloss.NewStyle().Bold(true).Render(string(name))
	}
	fmt.Fprint(w, line)
}

// Namespace is the namespace selector. It has two shapes: a searchable
// list when the token may list namespaces, and a free-text field when it
// may not — which is the normal case for an ordinary LDAP user on OCP,
// where cluster-wide namespace listing is usually not granted.
type Namespace struct {
	list   list.Model
	input  textinput.Model
	manual bool
	notice string
	keys   keys.KeyMap

	// ready distinguishes a constructed view from the zero value. The
	// root model resizes its views whenever the window changes — which
	// Bubble Tea reports at startup, long before a namespace has been
	// looked up — so the zero value has to tolerate being resized.
	ready bool
}

// NewNamespaceList builds the list form of the selector.
func NewNamespaceList(names []string, defaultNamespace string, km keys.KeyMap) Namespace {
	items := make([]list.Item, 0, len(names))
	selected := 0
	for i, name := range names {
		if name == defaultNamespace {
			selected = i
		}
		items = append(items, namespaceItem(name))
	}

	l := list.New(items, namespaceDelegate{}, 0, 0)
	l.Title = "namespace"
	l.Styles.Title = styles.Title
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(true)
	l.DisableQuitKeybindings()
	l.Select(selected)

	return Namespace{list: l, keys: km, ready: true}
}

// NewNamespaceManual builds the free-text form, explaining why the list is
// unavailable so the engineer does not read it as a failure.
func NewNamespaceManual(defaultNamespace, notice string, km keys.KeyMap) Namespace {
	input := textinput.New()
	input.Placeholder = "namespace"
	input.CharLimit = 253 // RFC 1123 label limit, as enforced by Kubernetes
	input.Width = 40
	input.Prompt = ""
	input.SetValue(defaultNamespace)
	input.Focus()

	return Namespace{input: input, manual: true, notice: notice, keys: km, ready: true}
}

// Init starts the cursor blinking when the view is a text field.
func (v Namespace) Init() tea.Cmd {
	if v.manual {
		return textinput.Blink
	}
	return nil
}

// SetSize resizes the underlying list. It is a no-op before the view has
// been built, and for the free-text form which has no list to size.
func (v *Namespace) SetSize(width, height int) {
	if !v.ready || v.manual {
		return
	}
	v.list.SetSize(width, height)
}

// Update handles selection or text entry.
func (v Namespace) Update(msg tea.Msg) (Namespace, tea.Cmd) {
	var cmd tea.Cmd
	if v.manual {
		v.input, cmd = v.input.Update(msg)
		return v, cmd
	}

	v.list, cmd = v.list.Update(msg)
	return v, cmd
}

// View renders the selector.
func (v Namespace) View() string {
	if !v.manual {
		return v.list.View()
	}

	var b strings.Builder
	b.WriteString(styles.Title.Render("namespace"))
	b.WriteString("\n\n")
	if v.notice != "" {
		b.WriteString("  " + styles.Notice.Render(v.notice))
		b.WriteString("\n\n")
	}
	b.WriteString("  " + styles.FieldLabelFocused.Render(padRight("Namespace:", 12)))
	b.WriteString(v.input.View())
	b.WriteString("\n\n")
	b.WriteString(styles.FieldHint.Render("  enter to start the session"))
	b.WriteString("\n")

	return b.String()
}

// Selected returns the chosen namespace.
func (v Namespace) Selected() string {
	if v.manual {
		return strings.TrimSpace(v.input.Value())
	}
	name, ok := v.list.SelectedItem().(namespaceItem)
	if !ok {
		return ""
	}
	return string(name)
}

// Filtering reports whether the search input is capturing keys.
func (v Namespace) Filtering() bool {
	return !v.manual && v.list.FilterState() == list.Filtering
}

// Manual reports whether the view is the free-text fallback rather than a
// selectable list.
func (v Namespace) Manual() bool { return v.manual }
