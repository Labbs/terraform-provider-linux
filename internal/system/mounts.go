package system

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// DefaultFstab is the file system table of the host.
const DefaultFstab = "/etc/fstab"

// FstabEntry is a line of /etc/fstab. Swap entries have no mount point:
// Path is "none".
type FstabEntry struct {
	Device  string
	Path    string
	FSType  string
	Options string
	Dump    int
	Pass    int
}

// IsSwap reports whether the entry is a swap area.
func (e FstabEntry) IsSwap() bool {
	return e.FSType == "swap"
}

// Same reports whether e and other describe the same mount: the same mount
// point, or the same device for swap areas.
func (e FstabEntry) Same(other FstabEntry) bool {
	if e.IsSwap() || other.IsSwap() {
		return e.IsSwap() && other.IsSwap() && e.Device == other.Device
	}
	return e.Path == other.Path
}

// String formats the entry as an fstab line.
func (e FstabEntry) String() string {
	options := e.Options
	if options == "" {
		options = "defaults"
	}
	p := e.Path
	if e.IsSwap() {
		p = "none"
	}
	return strings.Join([]string{
		escapeFstab(e.Device), escapeFstab(p), escapeFstab(e.FSType), escapeFstab(options),
		strconv.Itoa(e.Dump), strconv.Itoa(e.Pass),
	}, " ")
}

// escapeFstab escapes the characters fstab fields cannot hold, as octal.
func escapeFstab(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\\', '#':
			fmt.Fprintf(&b, `\%03o`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// unescapeOctal decodes the \NNN escapes of fstab and /proc/mounts.
func unescapeOctal(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// parseFstabLine parses a line of fstab, or returns false for comments,
// blank and malformed lines.
func parseFstabLine(line string) (FstabEntry, bool) {
	fields := strings.Fields(line)
	if len(fields) < 3 || strings.HasPrefix(fields[0], "#") {
		return FstabEntry{}, false
	}
	e := FstabEntry{
		Device:  unescapeOctal(fields[0]),
		Path:    unescapeOctal(fields[1]),
		FSType:  unescapeOctal(fields[2]),
		Options: "defaults",
	}
	if len(fields) > 3 {
		e.Options = unescapeOctal(fields[3])
	}
	if len(fields) > 4 {
		e.Dump, _ = strconv.Atoi(fields[4])
	}
	if len(fields) > 5 {
		e.Pass, _ = strconv.Atoi(fields[5])
	}
	return e, true
}

// FindFstabEntry returns the entry of content describing the same mount as
// want.
func FindFstabEntry(content string, want FstabEntry) (FstabEntry, bool) {
	for _, line := range strings.Split(content, "\n") {
		if e, ok := parseFstabLine(line); ok && e.Same(want) {
			return e, true
		}
	}
	return FstabEntry{}, false
}

// SetFstabEntry returns content with the entry for the mount of entry
// replaced, or appended when there is none. Duplicate entries for that
// mount are removed. With remove, the entries are only removed.
func SetFstabEntry(content string, entry FstabEntry, remove bool) string {
	var out []string
	written := remove
	lines := strings.Split(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		e, ok := parseFstabLine(line)
		if !ok || !e.Same(entry) {
			out = append(out, line)
			continue
		}
		if !written {
			out = append(out, entry.String())
			written = true
		}
	}
	if !written {
		out = append(out, entry.String())
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// Fstab returns the content of the file system table file, empty when it
// does not exist.
func (c *Client) Fstab(ctx context.Context, file string) (string, error) {
	content, err := c.ReadFile(ctx, file)
	if IsNotExist(err) {
		return "", nil
	}
	return string(content), err
}

// EditFstab sets, or removes, the entry for the mount of entry in the file
// system table file, and reports whether the file changed. When the file
// is /etc/fstab, systemd is told about the change.
func (c *Client) EditFstab(ctx context.Context, file string, entry FstabEntry, remove bool) (bool, error) {
	changed, err := c.EditFile(ctx, file, &Attributes{Mode: "0644"}, func(content []byte) ([]byte, error) {
		return []byte(SetFstabEntry(string(content), entry, remove)), nil
	})
	if err != nil || !changed || file != DefaultFstab {
		return changed, err
	}
	// systemd generates mount units from fstab, and warns until reloaded.
	_, err = c.exec(ctx, "if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then systemctl daemon-reload; fi", nil)
	return true, wrap(err, "reload systemd after editing", file)
}

// MountInfo describes what is mounted on a mount point.
type MountInfo struct {
	Device  string
	FSType  string
	Options string
}

// parseMounts returns the last mount on each mount point of /proc/mounts:
// the visible one.
func parseMounts(content string) map[string]MountInfo {
	mounts := map[string]MountInfo{}
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		mounts[unescapeOctal(fields[1])] = MountInfo{
			Device:  unescapeOctal(fields[0]),
			FSType:  fields[2],
			Options: fields[3],
		}
	}
	return mounts
}

// Mounted returns what is mounted on the mount point p, or nil.
func (c *Client) Mounted(ctx context.Context, p string) (*MountInfo, error) {
	out, err := c.exec(ctx, "exec cat /proc/self/mounts", nil)
	if err != nil {
		return nil, fmt.Errorf("reading mounts: %w", err)
	}
	if m, ok := parseMounts(string(out))[p]; ok {
		return &m, nil
	}
	return nil, nil
}

// resolveDevice turns the device spec "$dev", such as UUID=..., into a
// device path in $resolved.
const resolveDevice = `case $dev in
  *=*) resolved=$(findfs "$dev" 2>/dev/null || blkid -o device -t "$dev" 2>/dev/null | head -n 1) || resolved= ;;
  *) resolved=$dev ;;
esac
[ -z "$resolved" ] || resolved=$(readlink -f -- "$resolved")`

// SwapActive reports whether the swap area dev, a path or a spec such as
// UUID=..., is in use.
func (c *Client) SwapActive(ctx context.Context, dev string) (bool, error) {
	out, err := c.exec(ctx, script(
		"dev="+q(dev),
		resolveDevice,
		`[ -n "$resolved" ] || exit 0`,
		`tail -n +2 /proc/swaps | while read -r name rest; do`,
		`  [ "$(readlink -f -- "$name")" = "$resolved" ] && echo active`,
		`done`,
		`exit 0`,
	), nil)
	if err != nil {
		return false, wrap(err, "check swap", dev)
	}
	return strings.Contains(string(out), "active"), nil
}

// Mount mounts dev, a path or a spec such as UUID=..., on the mount point
// p, created if needed. Swap areas are enabled instead.
func (c *Client) Mount(ctx context.Context, e FstabEntry) error {
	if e.IsSwap() {
		_, err := c.exec(ctx, "exec swapon -- "+q(e.Device), nil)
		return wrap(err, "enable swap", e.Device)
	}
	options := e.Options
	if options == "" {
		options = "defaults"
	}
	_, err := c.exec(ctx, script(
		"set -e",
		"mkdir -p -- "+q(e.Path),
		"exec mount -t "+q(e.FSType)+" -o "+q(options)+" -- "+q(e.Device)+" "+q(e.Path),
	), nil)
	return wrap(err, "mount "+e.Device+" on", e.Path)
}

// Remount applies new mount options to the mount point p.
func (c *Client) Remount(ctx context.Context, p, options string) error {
	if options == "" {
		options = "defaults"
	}
	_, err := c.exec(ctx, "exec mount -o "+q("remount,"+options)+" -- "+q(p), nil)
	return wrap(err, "remount", p)
}

// Unmount unmounts what e describes, or disables the swap area.
func (c *Client) Unmount(ctx context.Context, e FstabEntry) error {
	if e.IsSwap() {
		_, err := c.exec(ctx, "exec swapoff -- "+q(e.Device), nil)
		return wrap(err, "disable swap", e.Device)
	}
	_, err := c.exec(ctx, "exec umount -- "+q(e.Path), nil)
	return wrap(err, "unmount", e.Path)
}
