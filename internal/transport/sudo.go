package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Sudo wraps a Runner so that every script runs as root through sudo.
type Sudo struct {
	inner    Runner
	password string
}

// NewSudo wraps inner with sudo. When password is empty, sudo must not ask
// for one (NOPASSWD rule or cached credentials).
func NewSudo(inner Runner, password string) (*Sudo, error) {
	if strings.ContainsAny(password, "\r\n") {
		return nil, errors.New("sudo password must not contain line breaks")
	}
	return &Sudo{inner: inner, password: password}, nil
}

func (s *Sudo) Run(ctx context.Context, script string, stdin io.Reader) (*Result, error) {
	if s.password == "" {
		return s.inner.Run(ctx, "exec sudo -n -- /bin/sh -c "+Quote(script), stdin)
	}
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	// The password travels on stdin, never on a command line, where any
	// user of the host could read it.
	stdin = io.MultiReader(strings.NewReader(s.password+"\n"), stdin)
	return s.inner.Run(ctx, fmt.Sprintf(sudoPasswordScript, Quote(script)), stdin)
}

func (s *Sudo) Close() error { return s.inner.Close() }

func (s *Sudo) String() string { return s.inner.String() + " (sudo)" }

// sudoPasswordScript reads the password from the first line of stdin, then
// runs the script through sudo with the rest of stdin. The password is only
// sent to sudo when sudo actually needs it, otherwise it would end up on the
// script's stdin.
const sudoPasswordScript = `IFS= read -r __sudo_password || exit 125
if sudo -n true 2>/dev/null; then
	unset __sudo_password
	exec sudo -n -- /bin/sh -c %[1]s
fi
{ printf '%%s\n' "$__sudo_password"; unset __sudo_password; exec cat; } | sudo -S -k -p '' -- /bin/sh -c %[1]s`
