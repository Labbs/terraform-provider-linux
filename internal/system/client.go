// Package system implements operations on a Linux host (files, directories,
// systemd units...) as shell scripts run through a transport.Runner.
package system

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync"

	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/labbs/terraform-provider-linux/internal/transport"
)

// exitNotFound is the exit status scripts use to report a missing file.
const exitNotFound = 44

// preamble makes command output independent of the host locale.
const preamble = "LC_ALL=C; export LC_ALL\n"

// Client performs operations on a Linux host.
type Client struct {
	runner transport.Runner

	// locks holds a *sync.Mutex per key, see lock.
	locks sync.Map

	packageManagerMu sync.Mutex
	packageManager   string
}

// New returns a Client running its scripts through runner.
func New(runner transport.Runner) *Client {
	return &Client{runner: runner}
}

func (c *Client) String() string {
	return c.runner.String()
}

// Run executes a user-provided script as is.
func (c *Client) Run(ctx context.Context, script string, stdin io.Reader) (*transport.Result, error) {
	tflog.Trace(ctx, "running script", map[string]any{"target": c.runner.String(), "script": script})
	res, err := c.runner.Run(ctx, script, stdin)
	if err == nil {
		tflog.Trace(ctx, "script succeeded", map[string]any{"target": c.runner.String()})
	}
	return res, err
}

// exec runs one of the provider's own scripts and returns its stdout.
func (c *Client) exec(ctx context.Context, script string, stdin io.Reader) ([]byte, error) {
	res, err := c.Run(ctx, preamble+script, stdin)
	if err != nil {
		if code, ok := transport.ExitCode(err); ok && code == exitNotFound {
			return nil, fs.ErrNotExist
		}
		return nil, err
	}
	return res.Stdout, nil
}

// lock serializes the operations on key, such as the edits of a file, and
// returns the function releasing it. Terraform applies resources in
// parallel, and two resources editing the same file must not interleave.
func (c *Client) lock(key string) func() {
	value, _ := c.locks.LoadOrStore(key, &sync.Mutex{})
	mu, _ := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// IsNotExist reports whether err means that a file or unit does not exist.
func IsNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

// script joins lines into a shell script. Values interpolated in the lines
// must be quoted with q.
func script(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func q(s string) string {
	return transport.Quote(s)
}

func wrap(err error, op, target string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s %s: %w", op, target, err)
}
