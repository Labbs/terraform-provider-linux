// Package transport runs shell scripts on a Linux host, either on the machine
// running Terraform or on a remote host over SSH, optionally through sudo.
package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Runner executes shell scripts on a target host.
type Runner interface {
	// Run executes script with /bin/sh on the target host, feeding it stdin
	// (which may be nil). A non-zero exit status is reported as an
	// *ExitError alongside the result; any other error means the script
	// could not be run to completion.
	Run(ctx context.Context, script string, stdin io.Reader) (*Result, error)

	// Close releases the resources held by the runner.
	Close() error

	// String describes the target host, for logs and error messages.
	String() string
}

// Result holds the outcome of a script.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// ExitError reports a script that exited with a non-zero status.
type ExitError struct {
	*Result
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(string(e.Stderr))
	if msg == "" {
		return fmt.Sprintf("exit status %d", e.ExitCode)
	}
	return fmt.Sprintf("exit status %d: %s", e.ExitCode, msg)
}

// ExitCode returns the exit status carried by err, if err is an *ExitError.
func ExitCode(err error) (int, bool) {
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode, true
	}
	return 0, false
}

// Unavailable is a Runner that always fails with the same error. It stands in
// for a real runner when the provider configuration cannot be resolved yet.
type Unavailable struct {
	Err error
}

func (u Unavailable) Run(context.Context, string, io.Reader) (*Result, error) {
	return nil, u.Err
}

func (Unavailable) Close() error { return nil }

func (Unavailable) String() string { return "unavailable" }
