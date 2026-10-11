package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/labbs/terraform-provider-linux/internal/transport"
)

func TestParseStat(t *testing.T) {
	info, err := parseStat("regular file|4755|UNKNOWN|1234|wheel|10|42\n")
	if err != nil {
		t.Fatal(err)
	}
	want := FileInfo{Type: TypeFile, Mode: "4755", Owner: "1234", UID: "1234", Group: "wheel", GID: "10", Size: 42}
	if *info != want {
		t.Errorf("got %+v, want %+v", *info, want)
	}

	info, err = parseStat("directory|755|root|0|root|0|4096")
	if err != nil {
		t.Fatal(err)
	}
	if info.Type != TypeDirectory || info.Mode != "0755" {
		t.Errorf("got %+v", *info)
	}

	if _, err := parseStat("garbage"); err == nil {
		t.Error("expected an error")
	}
}

func TestParseUnitStatus(t *testing.T) {
	status := parseUnitStatus("LoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\n")
	if !status.Exists() || !status.Active() || !status.Enabled() || status.Masked() || !status.CanBeEnabled() {
		t.Errorf("unexpected %+v", *status)
	}

	status = parseUnitStatus("LoadState=not-found\nActiveState=inactive\nSubState=dead\nUnitFileState=\n")
	if status.Exists() || status.Active() {
		t.Errorf("unexpected %+v", *status)
	}

	status = parseUnitStatus("LoadState=masked\nActiveState=inactive\nSubState=dead\nUnitFileState=masked\n")
	if !status.Masked() || status.CanBeEnabled() {
		t.Errorf("unexpected %+v", *status)
	}

	status = parseUnitStatus("LoadState=loaded\nActiveState=active\nSubState=exited\nUnitFileState=static\n")
	if status.Enabled() || status.CanBeEnabled() {
		t.Errorf("unexpected %+v", *status)
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]uint32{"644": 0o644, "0755": 0o755, "1777": 0o1777, "4755": 0o4755} {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Errorf("ParseMode(%q) = %o, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "64", "888", "u+x", "07777", "-644"} {
		if _, err := ParseMode(in); err == nil {
			t.Errorf("ParseMode(%q): expected an error", in)
		}
	}
}

func TestCheckPath(t *testing.T) {
	for _, p := range []string{"/etc/hosts", "/opt/my app"} {
		if err := CheckPath(p); err != nil {
			t.Errorf("CheckPath(%q) = %v", p, err)
		}
	}
	for _, p := range []string{"", "/", "relative/path", "/etc/../etc", "/etc/", "//etc"} {
		if err := CheckPath(p); err == nil {
			t.Errorf("CheckPath(%q): expected an error", p)
		}
	}
}

func TestAttributesCommands(t *testing.T) {
	got := Attributes{Mode: "0640", Owner: "www-data", Group: "adm"}.commands(`"$p"`)
	want := []string{`chown -- www-data:adm "$p"`, `chmod -- 0640 "$p"`}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got %q", got)
	}
	got = Attributes{Group: "adm"}.commands(`"$p"`)
	if len(got) != 1 || got[0] != `chgrp -- adm "$p"` {
		t.Errorf("got %q", got)
	}
}

func TestRunCommand(t *testing.T) {
	c := New(transport.NewLocal())
	dir := t.TempDir()

	res, err := c.RunCommand(context.Background(), Command{
		Script: `printf '%s|%s|%s|' "$GREETING" "$(pwd -P)" "$__terraform_script"; cat; echo end`,
		Env:    map[string]string{"GREETING": "it's $HOME `x`"},
		Dir:    dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantDir, _ := filepath.EvalSymlinks(dir)
	if got, want := string(res.Stdout), "it's $HOME `x`|"+wantDir+"||end\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}

	_, err = c.RunCommand(context.Background(), Command{Script: "echo failed >&2\nexit 4"})
	if code, ok := transport.ExitCode(err); !ok || code != 4 {
		t.Fatalf("expected exit status 4, got %v", err)
	}

	if _, err := c.RunCommand(context.Background(), Command{Script: "true", Env: map[string]string{"BAD-NAME": "x"}}); err == nil {
		t.Fatal("expected an error for an invalid variable name")
	}
}

// The following tests need GNU or BusyBox userland.

func linuxClient(t *testing.T) *Client {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("requires a Linux host")
	}
	return New(transport.NewLocal())
}

func TestFiles(t *testing.T) {
	c := linuxClient(t)
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "sub", "dir", "file.txt")

	if _, err := c.Stat(ctx, p); !IsNotExist(err) {
		t.Fatalf("Stat on a missing file: %v", err)
	}
	if err := c.WriteFile(ctx, p, strings.NewReader("hello\n"), Attributes{Mode: "0600"}, false); err == nil {
		t.Fatal("expected an error without createParents")
	}
	if err := c.WriteFile(ctx, p, strings.NewReader("hello\n"), Attributes{Mode: "0640"}, true); err != nil {
		t.Fatal(err)
	}
	info, err := c.Stat(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Type != TypeFile || info.Mode != "0640" || info.Size != 6 {
		t.Errorf("unexpected %+v", *info)
	}
	data, err := c.ReadFile(ctx, p)
	if err != nil || string(data) != "hello\n" {
		t.Fatalf("ReadFile = %q, %v", data, err)
	}
	sum, err := c.FileSHA256(ctx, p)
	if err != nil || sum != "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03" {
		t.Fatalf("FileSHA256 = %s, %v", sum, err)
	}
	if err := c.SetAttributes(ctx, p, Attributes{Mode: "0604"}); err != nil {
		t.Fatal(err)
	}
	if info, _ := c.Stat(ctx, p); info.Mode != "0604" {
		t.Errorf("mode = %s", info.Mode)
	}

	dir := filepath.Dir(p)
	if err := c.RemoveDir(ctx, dir, false); err == nil {
		t.Fatal("expected an error removing a non-empty directory")
	}
	if err := c.Remove(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveDir(ctx, dir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("directory still exists: %v", err)
	}
}

func TestMkdir(t *testing.T) {
	c := linuxClient(t)
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "a", "b")

	if err := c.Mkdir(ctx, p, Attributes{Mode: "0750"}, false); err == nil {
		t.Fatal("expected an error without createParents")
	}
	if err := c.Mkdir(ctx, p, Attributes{Mode: "0750"}, true); err != nil {
		t.Fatal(err)
	}
	// Creating an existing directory only applies the attributes.
	if err := c.Mkdir(ctx, p, Attributes{Mode: "0700"}, false); err != nil {
		t.Fatal(err)
	}
	info, err := c.Stat(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Type != TypeDirectory || info.Mode != "0700" {
		t.Errorf("unexpected %+v", *info)
	}
	if err := c.RemoveDir(ctx, filepath.Dir(p), true); err != nil {
		t.Fatal(err)
	}
}

func TestSed(t *testing.T) {
	c := linuxClient(t)
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "sshd_config")
	if err := os.WriteFile(p, []byte("Port 22\n#PermitRootLogin yes\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	exprs := []string{`s/^#?PermitRootLogin .*/PermitRootLogin no/`}

	pending, err := c.SedPending(ctx, p, exprs, true)
	if err != nil || !pending {
		t.Fatalf("SedPending = %v, %v", pending, err)
	}
	res, err := c.Sed(ctx, p, exprs, true)
	if err != nil || res != (SedResult{Changed: true, Idempotent: true}) {
		t.Fatalf("Sed = %+v, %v", res, err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "Port 22\nPermitRootLogin no\n" {
		t.Errorf("content = %q", data)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode changed to %o", fi.Mode().Perm())
	}
	if pending, _ := c.SedPending(ctx, p, exprs, true); pending {
		t.Error("expected no pending change")
	}

	res, err = c.Sed(ctx, p, []string{"s/22/222/"}, false)
	if err != nil || res != (SedResult{Changed: true}) {
		t.Fatalf("non-idempotent Sed = %+v, %v", res, err)
	}
}
