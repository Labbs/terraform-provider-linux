package system

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

// FileType is the kind of a file.
type FileType string

const (
	TypeFile      FileType = "file"
	TypeDirectory FileType = "directory"
	TypeSymlink   FileType = "symlink"
	TypeOther     FileType = "other"
)

// FileInfo describes a file. Symbolic links are not followed.
type FileInfo struct {
	Type FileType
	// Mode holds the permission bits as four octal digits, e.g. "0644".
	Mode string
	// Owner and Group are names, or IDs when the host has no name for them.
	Owner string
	Group string
	UID   string
	GID   string
	Size  int64
}

// Attributes are the ownership and permissions of a file. Empty fields are
// left as they are.
type Attributes struct {
	Mode  string
	Owner string
	Group string
}

// commands returns the commands applying the attributes to target, a
// quoted shell word.
func (a Attributes) commands(target string) []string {
	var lines []string
	// chown may clear setuid and setgid bits, so it runs before chmod.
	switch {
	case a.Owner != "" && a.Group != "":
		lines = append(lines, "chown -- "+q(a.Owner+":"+a.Group)+" "+target)
	case a.Owner != "":
		lines = append(lines, "chown -- "+q(a.Owner)+" "+target)
	case a.Group != "":
		lines = append(lines, "chgrp -- "+q(a.Group)+" "+target)
	}
	if a.Mode != "" {
		lines = append(lines, "chmod -- "+q(a.Mode)+" "+target)
	}
	return lines
}

// CheckPath reports whether p is a path the provider may manage: absolute,
// in canonical form and not the root directory.
func CheckPath(p string) error {
	switch {
	case !path.IsAbs(p):
		return errors.New("must be an absolute path")
	case path.Clean(p) != p:
		return fmt.Errorf("must be in canonical form (%s)", path.Clean(p))
	case p == "/":
		return errors.New("must not be the root directory")
	}
	return nil
}

// ParseMode parses permission bits written in octal, such as "644" or
// "0755".
func ParseMode(s string) (uint32, error) {
	if len(s) < 3 || len(s) > 4 {
		return 0, fmt.Errorf("invalid mode %q: expected 3 or 4 octal digits", s)
	}
	mode, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid mode %q: expected 3 or 4 octal digits", s)
	}
	return uint32(mode), nil
}

// FormatMode formats permission bits as four octal digits.
func FormatMode(mode uint32) string {
	return fmt.Sprintf("%04o", mode)
}

// Stat describes the file at p, or returns an error satisfying IsNotExist.
// A symbolic link is described itself.
func (c *Client) Stat(ctx context.Context, p string) (*FileInfo, error) {
	return c.stat(ctx, p, false)
}

// StatFollow describes the file at p, following symbolic links.
func (c *Client) StatFollow(ctx context.Context, p string) (*FileInfo, error) {
	return c.stat(ctx, p, true)
}

func (c *Client) stat(ctx context.Context, p string, follow bool) (*FileInfo, error) {
	exists, stat := `[ -e "$p" ] || [ -L "$p" ]`, "stat"
	if follow {
		exists, stat = `[ -e "$p" ]`, "stat -L"
	}
	out, err := c.exec(ctx, script(
		"p="+q(p),
		fmt.Sprintf(`%s || exit %d`, exists, exitNotFound),
		`exec `+stat+` -c '%F|%a|%U|%u|%G|%g|%s' -- "$p"`,
	), nil)
	if err != nil {
		return nil, wrap(err, "stat", p)
	}
	info, err := parseStat(string(out))
	return info, wrap(err, "stat", p)
}

func parseStat(out string) (*FileInfo, error) {
	fields := strings.Split(strings.TrimSpace(out), "|")
	if len(fields) != 7 {
		return nil, fmt.Errorf("unexpected stat output %q", out)
	}
	mode, err := strconv.ParseUint(fields[1], 8, 32)
	if err != nil {
		return nil, fmt.Errorf("unexpected stat mode %q", fields[1])
	}
	size, err := strconv.ParseInt(fields[6], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("unexpected stat size %q", fields[6])
	}
	info := &FileInfo{
		Mode:  FormatMode(uint32(mode)),
		Owner: fields[2],
		UID:   fields[3],
		Group: fields[4],
		GID:   fields[5],
		Size:  size,
	}
	if info.Owner == "UNKNOWN" {
		info.Owner = info.UID
	}
	if info.Group == "UNKNOWN" {
		info.Group = info.GID
	}
	switch fields[0] {
	case "regular file", "regular empty file":
		info.Type = TypeFile
	case "directory":
		info.Type = TypeDirectory
	case "symbolic link":
		info.Type = TypeSymlink
	default:
		info.Type = TypeOther
	}
	return info, nil
}

// ReadFile returns the content of the file at p.
func (c *Client) ReadFile(ctx context.Context, p string) ([]byte, error) {
	out, err := c.exec(ctx, script(
		"p="+q(p),
		fmt.Sprintf(`[ -e "$p" ] || exit %d`, exitNotFound),
		`exec cat -- "$p"`,
	), nil)
	return out, wrap(err, "read", p)
}

// FileSHA256 returns the hex-encoded SHA-256 digest of the file at p.
func (c *Client) FileSHA256(ctx context.Context, p string) (string, error) {
	out, err := c.exec(ctx, script(
		"p="+q(p),
		fmt.Sprintf(`[ -e "$p" ] || exit %d`, exitNotFound),
		`sha256sum < "$p"`,
	), nil)
	if err != nil {
		return "", wrap(err, "hash", p)
	}
	sum, _, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	if len(sum) != 64 {
		return "", fmt.Errorf("hash %s: unexpected sha256sum output %q", p, out)
	}
	return sum, nil
}

// WriteFile replaces the file at p with content, atomically: readers see
// either the old or the new content. The mode defaults to 0644.
func (c *Client) WriteFile(ctx context.Context, p string, content io.Reader, attrs Attributes, createParents bool) error {
	if attrs.Mode == "" {
		attrs.Mode = "0644"
	}
	lines := []string{
		"set -e",
		"p=" + q(p),
		"d=" + q(path.Dir(p)),
		`if [ -d "$p" ]; then echo "$p is a directory" >&2; exit 1; fi`,
	}
	if createParents {
		lines = append(lines, `mkdir -p -- "$d"`)
	}
	lines = append(lines,
		// The temporary file lives next to p, so that mv is a rename.
		`tmp=$(mktemp "$d/.terraform-provider-linux.XXXXXX")`,
		`trap 'rm -f -- "$tmp"' EXIT`,
		`cat > "$tmp"`,
	)
	lines = append(lines, attrs.commands(`"$tmp"`)...)
	lines = append(lines, `mv -f -- "$tmp" "$p"`, "trap - EXIT")
	_, err := c.exec(ctx, script(lines...), content)
	return wrap(err, "write", p)
}

// SetAttributes changes the ownership and permissions of the file at p.
func (c *Client) SetAttributes(ctx context.Context, p string, attrs Attributes) error {
	lines := []string{
		"set -e",
		"p=" + q(p),
		fmt.Sprintf(`[ -e "$p" ] || exit %d`, exitNotFound),
	}
	lines = append(lines, attrs.commands(`"$p"`)...)
	_, err := c.exec(ctx, script(lines...), nil)
	return wrap(err, "chmod/chown", p)
}

// Mkdir creates the directory p unless it exists, then applies attrs.
func (c *Client) Mkdir(ctx context.Context, p string, attrs Attributes, createParents bool) error {
	mkdir := `mkdir -- "$p"`
	if createParents {
		mkdir = `mkdir -p -- "$p"`
	}
	lines := []string{
		"set -e",
		"p=" + q(p),
		`[ -d "$p" ] || ` + mkdir,
	}
	lines = append(lines, attrs.commands(`"$p"`)...)
	_, err := c.exec(ctx, script(lines...), nil)
	return wrap(err, "mkdir", p)
}

// Remove deletes the file at p, if it exists.
func (c *Client) Remove(ctx context.Context, p string) error {
	if err := CheckPath(p); err != nil {
		return wrap(err, "remove", p)
	}
	_, err := c.exec(ctx, "exec rm -f -- "+q(p), nil)
	return wrap(err, "remove", p)
}

// RemoveDir deletes the directory p, if it exists. Unless recursive, the
// directory must be empty.
func (c *Client) RemoveDir(ctx context.Context, p string, recursive bool) error {
	if err := CheckPath(p); err != nil {
		return wrap(err, "remove", p)
	}
	rm := `exec rmdir -- "$p"`
	if recursive {
		rm = `exec rm -rf -- "$p"`
	}
	_, err := c.exec(ctx, script(
		"p="+q(p),
		`[ -e "$p" ] || exit 0`,
		rm,
	), nil)
	return wrap(err, "remove", p)
}

// EditFile replaces the content of the file at p with edit(content), keeping
// its permissions and owner, and reports whether the content changed. When
// the file does not exist, it is created with the attributes create, or
// EditFile fails if create is nil. Edits of a file are serialized.
func (c *Client) EditFile(ctx context.Context, p string, create *Attributes, edit func(content []byte) ([]byte, error)) (bool, error) {
	defer c.lock("file:" + p)()

	content, err := c.ReadFile(ctx, p)
	var attrs Attributes
	created := false
	switch {
	case IsNotExist(err) && create != nil:
		attrs, created = *create, true
	case err != nil:
		return false, err
	default:
		info, err := c.Stat(ctx, p)
		if err != nil {
			return false, err
		}
		attrs = Attributes{Mode: info.Mode, Owner: info.UID, Group: info.GID}
	}

	edited, err := edit(content)
	if err != nil {
		return false, wrap(err, "edit", p)
	}
	if !created && bytes.Equal(edited, content) {
		return false, nil
	}
	return true, c.WriteFile(ctx, p, bytes.NewReader(edited), attrs, create != nil)
}

// Readlink returns the target of the symbolic link at p.
func (c *Client) Readlink(ctx context.Context, p string) (string, error) {
	out, err := c.exec(ctx, script(
		"p="+q(p),
		fmt.Sprintf(`[ -L "$p" ] || exit %d`, exitNotFound),
		`exec readlink -- "$p"`,
	), nil)
	if err != nil {
		return "", wrap(err, "readlink", p)
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

// Symlink makes p a symbolic link to target, replacing a file or a link
// already at p, but not a directory.
func (c *Client) Symlink(ctx context.Context, target, p string, createParents bool) error {
	lines := []string{
		"set -e",
		"p=" + q(p),
		`if [ -d "$p" ] && [ ! -L "$p" ]; then echo "$p is a directory" >&2; exit 1; fi`,
	}
	if createParents {
		lines = append(lines, "mkdir -p -- "+q(path.Dir(p)))
	}
	lines = append(lines, "ln -sfn -- "+q(target)+` "$p"`)
	_, err := c.exec(ctx, script(lines...), nil)
	return wrap(err, "symlink", p)
}
