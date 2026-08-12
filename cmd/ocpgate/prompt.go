package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"

	"golang.org/x/term"
)

// prompter reads credentials from a single buffered view of stdin.
//
// The buffering has to be shared: a fresh bufio.Reader per prompt would
// read ahead past the username and discard the password along with the
// rest of its buffer, which breaks every non-terminal caller (a pipe, a
// script, a test).
type prompter struct {
	in     *bufio.Reader
	inFile *os.File
	out    io.Writer
}

func newPrompter(in *os.File, out io.Writer) *prompter {
	return &prompter{in: bufio.NewReader(in), inFile: in, out: out}
}

// defaultUsername guesses the LDAP username from the local account, which
// is right often enough to be worth offering as the prompt default.
func defaultUsername() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

// Username reads a username, offering def as the default. Prompts go to
// the prompter's writer — stderr in practice — so stdout stays a clean
// audit stream.
func (p *prompter) Username(def string) (string, error) {
	if def != "" {
		fmt.Fprintf(p.out, "Username [%s]: ", def)
	} else {
		fmt.Fprint(p.out, "Username: ")
	}

	line, err := p.readLine()
	if err != nil {
		return "", fmt.Errorf("read username: %w", err)
	}

	username := strings.TrimSpace(line)
	if username == "" {
		username = def
	}
	if username == "" {
		return "", fmt.Errorf("username is required")
	}
	return username, nil
}

// Password reads a password without echoing it. When stdin is not a
// terminal — a pipe in a script or a test — it reads a line instead, since
// there is no terminal to disable echo on.
func (p *prompter) Password() (string, error) {
	if !p.isTerminal() {
		line, err := p.readLine()
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	fmt.Fprint(p.out, "Password: ")
	raw, err := term.ReadPassword(int(p.inFile.Fd()))
	fmt.Fprintln(p.out)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(raw), nil
}

func (p *prompter) isTerminal() bool {
	return p.inFile != nil && term.IsTerminal(int(p.inFile.Fd()))
}

// readLine returns the next line, treating a final unterminated line as
// valid input rather than an error.
func (p *prompter) readLine() (string, error) {
	line, err := p.in.ReadString('\n')
	if err == io.EOF && line != "" {
		return line, nil
	}
	if err != nil {
		return "", err
	}
	return line, nil
}
