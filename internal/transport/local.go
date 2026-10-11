package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

// waitDelay bounds how long a finished or cancelled command may keep its
// output pipes open, e.g. because it left a background process behind.
const waitDelay = 10 * time.Second

// Local runs scripts on the machine running Terraform.
type Local struct{}

// NewLocal returns a Runner for the local machine.
func NewLocal() *Local {
	return &Local{}
}

func (*Local) Run(ctx context.Context, script string, stdin io.Reader) (*Result, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	cmd.Stdin = stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay

	err := cmd.Run()
	res := &Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err != nil && ctx.Err() != nil {
		return res, ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, &ExitError{Result: res}
	}
	return res, err
}

func (*Local) Close() error { return nil }

func (*Local) String() string { return "local" }
