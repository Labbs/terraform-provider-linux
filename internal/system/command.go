package system

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/labbs/terraform-provider-linux/internal/transport"
)

// Command is a user-provided shell script.
type Command struct {
	Script string
	// Env holds variables exported to the script.
	Env map[string]string
	// Dir is the directory the script runs in, when set.
	Dir string
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidEnvName reports whether name can be exported by a shell.
func ValidEnvName(name string) bool {
	return envName.MatchString(name)
}

// commandWrapper runs the script it reads on stdin, so that neither the
// script nor its environment, which may hold secrets, shows up on a command
// line other users of the host can read. The script gets an empty stdin.
const commandWrapper = `__terraform_script=$(cat) || exit 125
exec </dev/null
eval "unset __terraform_script
$__terraform_script"`

// RunCommand runs cmd with /bin/sh. A non-zero exit status is reported as a
// *transport.ExitError, along with the result.
func (c *Client) RunCommand(ctx context.Context, cmd Command) (*transport.Result, error) {
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(cmd.Env)) {
		if !ValidEnvName(name) {
			return nil, fmt.Errorf("invalid environment variable name %q", name)
		}
		fmt.Fprintf(&b, "%s=%s; export %s\n", name, q(cmd.Env[name]), name)
	}
	if cmd.Dir != "" {
		fmt.Fprintf(&b, "cd -- %s || exit\n", q(cmd.Dir))
	}
	b.WriteString(cmd.Script)
	return c.Run(ctx, commandWrapper, strings.NewReader(b.String()))
}
