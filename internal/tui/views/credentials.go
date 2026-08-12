package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/oziie/ocpgate/internal/auth"
	"github.com/oziie/ocpgate/internal/registry"
	"github.com/oziie/ocpgate/internal/tui/keys"
	"github.com/oziie/ocpgate/internal/tui/styles"
)

const (
	fieldUsername = iota
	fieldPassword
	fieldCount
)

// Credentials is the LDAP credential prompt. The password input uses
// EchoPassword so the value is never rendered, and the entered password
// lives only in this model until it is handed to the authenticator.
type Credentials struct {
	cluster registry.ClusterEntry
	inputs  []textinput.Model
	focus   int
	keys    keys.KeyMap
	err     error
	busy    bool
}

// NewCredentials builds the credential prompt for a cluster, pre-filling
// the username when one is already known.
func NewCredentials(cluster registry.ClusterEntry, defaultUsername string, km keys.KeyMap) Credentials {
	username := textinput.New()
	username.Placeholder = "ldap username"
	username.CharLimit = 128
	username.Width = 40
	username.Prompt = ""
	username.SetValue(defaultUsername)

	password := textinput.New()
	password.Placeholder = "ldap password"
	password.CharLimit = 256
	password.Width = 40
	password.Prompt = ""
	password.EchoMode = textinput.EchoPassword
	password.EchoCharacter = '•'

	c := Credentials{
		cluster: cluster,
		inputs:  []textinput.Model{username, password},
		keys:    km,
	}

	// Start on the password when the username is already filled in, which
	// is the common case since it defaults to the local account name.
	if defaultUsername != "" {
		c.focus = fieldPassword
	}
	c.syncFocus()

	return c
}

// Init starts the cursor blinking on the focused field.
func (v Credentials) Init() tea.Cmd {
	return textinput.Blink
}

// SetBusy marks the view as waiting on the authentication round trip, so
// the form stops accepting input while a request is in flight.
func (v *Credentials) SetBusy(busy bool) {
	v.busy = busy
	if busy {
		v.err = nil
	}
}

// SetError shows an authentication failure above the form.
func (v *Credentials) SetError(err error) {
	v.err = err
	v.busy = false

	// Keep the username, clear the password: a retry almost always means
	// the password was mistyped, and a stale password must not linger.
	v.inputs[fieldPassword].Reset()
	v.focus = fieldPassword
	v.syncFocus()
}

// Submitted reports whether both fields are filled in.
func (v Credentials) Submitted() bool {
	return strings.TrimSpace(v.inputs[fieldUsername].Value()) != "" &&
		v.inputs[fieldPassword].Value() != ""
}

// Credentials returns what the engineer typed.
func (v Credentials) Credentials() auth.Credentials {
	return auth.Credentials{
		Username: strings.TrimSpace(v.inputs[fieldUsername].Value()),
		Password: v.inputs[fieldPassword].Value(),
	}
}

// Update handles field navigation and text entry.
func (v Credentials) Update(msg tea.Msg) (Credentials, tea.Cmd) {
	if v.busy {
		return v, nil
	}

	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch {
		case key.Matches(keyMsg, v.keys.Form.Next):
			v.focus = (v.focus + 1) % fieldCount
			v.syncFocus()
			return v, textinput.Blink

		case key.Matches(keyMsg, v.keys.Form.Prev):
			v.focus = (v.focus - 1 + fieldCount) % fieldCount
			v.syncFocus()
			return v, textinput.Blink

		case key.Matches(keyMsg, v.keys.Form.Submit):
			// Enter on the username field advances rather than submitting
			// a half-filled form.
			if v.focus == fieldUsername && v.inputs[fieldPassword].Value() == "" {
				v.focus = fieldPassword
				v.syncFocus()
				return v, textinput.Blink
			}
			return v, nil
		}
	}

	cmds := make([]tea.Cmd, len(v.inputs))
	for i := range v.inputs {
		v.inputs[i], cmds[i] = v.inputs[i].Update(msg)
	}
	return v, tea.Batch(cmds...)
}

// View renders the credential form.
func (v Credentials) View() string {
	var b strings.Builder

	b.WriteString(styles.Title.Render("authenticate"))
	b.WriteString("\n\n")
	b.WriteString("  " + v.cluster.Name + "  " + EnvironmentBadge(v.cluster))
	b.WriteString("\n")
	b.WriteString(styles.FieldHint.Render("  " + v.cluster.APIEndpoint))
	if v.cluster.LDAPRealm != "" {
		b.WriteString(styles.FieldHint.Render("   realm: " + v.cluster.LDAPRealm))
	}
	b.WriteString("\n\n")

	for i, label := range []string{"Username", "Password"} {
		style := styles.FieldLabel
		marker := "  "
		if i == v.focus {
			style = styles.FieldLabelFocused
			marker = "> "
		}
		b.WriteString(marker + style.Render(padRight(label+":", 10)))
		b.WriteString(v.inputs[i].View())
		b.WriteString("\n")
	}

	b.WriteString("\n")
	switch {
	case v.busy:
		b.WriteString("  " + styles.Notice.Render("authenticating…"))
	case v.err != nil:
		b.WriteString("  " + styles.Error.Render(v.err.Error()))
	default:
		b.WriteString(styles.FieldHint.Render("  credentials are sent to the cluster and never stored"))
	}
	b.WriteString("\n")

	return b.String()
}

// syncFocus focuses exactly one input and blurs the rest.
func (v *Credentials) syncFocus() {
	for i := range v.inputs {
		if i == v.focus {
			v.inputs[i].Focus()
			continue
		}
		v.inputs[i].Blur()
	}
}
