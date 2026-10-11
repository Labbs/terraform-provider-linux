package transport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQuote(t *testing.T) {
	inputs := []string{
		"", "simple", "/etc/nginx/nginx.conf", "with space", "it's", `"double"`,
		"$HOME", "`id`", "a\nb", "semi;colon", "glob*", "tab\there", "--", "-n", "x=y",
	}
	runner := NewLocal()
	for _, in := range inputs {
		res, err := runner.Run(context.Background(), "printf '%s' "+Quote(in), nil)
		if err != nil {
			t.Fatalf("Quote(%q) = %s: %v", in, Quote(in), err)
		}
		if got := string(res.Stdout); got != in {
			t.Errorf("Quote(%q) = %s, read back as %q", in, Quote(in), got)
		}
	}
	if got := Quote("plain/path-1.2_3"); got != "plain/path-1.2_3" {
		t.Errorf("safe words should not be quoted, got %s", got)
	}
}

func TestJoin(t *testing.T) {
	if got, want := Join("systemctl", "restart", "my unit"), `systemctl restart 'my unit'`; got != want {
		t.Errorf("Join = %s, want %s", got, want)
	}
}

func TestLocalRun(t *testing.T) {
	runner := NewLocal()
	res, err := runner.Run(context.Background(), `cat; echo err >&2`, strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "hello" || string(res.Stderr) != "err\n" {
		t.Errorf("got stdout %q stderr %q", res.Stdout, res.Stderr)
	}

	res, err = runner.Run(context.Background(), "echo bad >&2; exit 7", nil)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode != 7 || res.ExitCode != 7 {
		t.Fatalf("expected exit status 7, got %v", err)
	}
	if err.Error() != "exit status 7: bad" {
		t.Errorf("error = %q", err)
	}
}

func TestLocalCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := NewLocal().Run(ctx, "sleep 10", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected a deadline error, got %v", err)
	}
}

// fakeSudo mimics the parts of sudo the Sudo runner relies on. A password
// is required when $FAKE_SUDO_PASSWORD is set.
const fakeSudo = `#!/bin/sh
nonint= stdin=
while [ $# -gt 0 ]; do
	case $1 in
	-n) nonint=1 ;;
	-S) stdin=1 ;;
	-k) ;;
	-p) shift ;;
	--) shift; break ;;
	*) break ;;
	esac
	shift
done
if [ -n "$FAKE_SUDO_PASSWORD" ]; then
	if [ -n "$nonint" ] || [ -z "$stdin" ]; then
		echo "sudo: a password is required" >&2
		exit 1
	fi
	IFS= read -r pw
	if [ "$pw" != "$FAKE_SUDO_PASSWORD" ]; then
		echo "sudo: incorrect password" >&2
		exit 1
	fi
fi
FAKE_SUDO=1 exec "$@"
`

func TestSudo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(fakeSudo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	const script = `printf '%s:' "$FAKE_SUDO"; cat`
	tests := []struct {
		name           string
		required       string // password sudo asks for, if any
		configured     string // password given to the runner
		wantStdout     string
		wantErrContain string
	}{
		{name: "passwordless", wantStdout: "1:payload\nline 2"},
		{name: "password", required: "pa ss'wd", configured: "pa ss'wd", wantStdout: "1:payload\nline 2"},
		{name: "password configured but not required", configured: "unused", wantStdout: "1:payload\nline 2"},
		{name: "wrong password", required: "right", configured: "wrong", wantErrContain: "incorrect password"},
		{name: "missing password", required: "right", wantErrContain: "password is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("FAKE_SUDO_PASSWORD", tt.required)
			runner, err := NewSudo(NewLocal(), tt.configured)
			if err != nil {
				t.Fatal(err)
			}
			res, err := runner.Run(context.Background(), script, strings.NewReader("payload\nline 2"))
			if tt.wantErrContain != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrContain) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrContain, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := string(res.Stdout); got != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", got, tt.wantStdout)
			}
		})
	}
}

func TestSudoRejectsMultilinePassword(t *testing.T) {
	if _, err := NewSudo(NewLocal(), "a\nb"); err == nil {
		t.Fatal("expected an error")
	}
}
