package system

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/labbs/terraform-provider-linux/internal/transport"
)

// Crontab returns the crontab of user, empty when it has none. An empty
// user means the user running the commands.
func (c *Client) Crontab(ctx context.Context, user string) (string, error) {
	res, err := c.Run(ctx, preamble+"exec "+crontabCommand(user, "-l"), nil)
	if _, ok := transport.ExitCode(err); ok && strings.Contains(string(res.Stderr), "no crontab") {
		return "", nil
	}
	if err != nil {
		return "", wrap(err, "read crontab of", userOrCurrent(user))
	}
	return string(res.Stdout), nil
}

// EditCrontab replaces the crontab of user with edit(crontab), and reports
// whether it changed. Edits of a crontab are serialized.
func (c *Client) EditCrontab(ctx context.Context, user string, edit func(string) (string, error)) (bool, error) {
	defer c.lock("crontab:" + user)()
	crontab, err := c.Crontab(ctx, user)
	if err != nil {
		return false, err
	}
	edited, err := edit(crontab)
	if err != nil || edited == crontab {
		return false, err
	}
	_, err = c.exec(ctx, "exec "+crontabCommand(user, "-"), strings.NewReader(edited))
	return true, wrap(err, "write crontab of", userOrCurrent(user))
}

func crontabCommand(user, arg string) string {
	if user == "" {
		return transport.Join("crontab", arg)
	}
	return transport.Join("crontab", "-u", user, arg)
}

func userOrCurrent(user string) string {
	if user == "" {
		return "the current user"
	}
	return user
}

var sysctlName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*$`)

// ValidSysctlName reports whether name can be a kernel parameter.
func ValidSysctlName(name string) bool {
	return sysctlName.MatchString(name)
}

// NormalizeSysctl returns a parameter value with single spaces, the way
// values are compared: sysctl separates the fields of a value with tabs.
func NormalizeSysctl(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// Sysctl returns the current value of a kernel parameter.
func (c *Client) Sysctl(ctx context.Context, name string) (string, error) {
	if !ValidSysctlName(name) {
		return "", fmt.Errorf("invalid kernel parameter %q", name)
	}
	out, err := c.exec(ctx, "exec sysctl -n "+q(name), nil)
	if err != nil {
		return "", wrap(err, "sysctl", name)
	}
	return NormalizeSysctl(string(out)), nil
}

// SetSysctl changes the current value of a kernel parameter.
func (c *Client) SetSysctl(ctx context.Context, name, value string) error {
	if !ValidSysctlName(name) {
		return fmt.Errorf("invalid kernel parameter %q", name)
	}
	_, err := c.exec(ctx, "exec sysctl -q -w "+q(name+"="+value), nil)
	return wrap(err, "sysctl", name)
}

// Hostname returns the current host name and the one in /etc/hostname,
// which is empty when the file does not exist.
func (c *Client) Hostname(ctx context.Context) (current, persisted string, err error) {
	out, err := c.exec(ctx, script(
		"hostname",
		`if [ -r /etc/hostname ]; then head -n 1 /etc/hostname; else echo; fi`,
	), nil)
	if err != nil {
		return "", "", fmt.Errorf("reading host name: %w", err)
	}
	current, persisted, _ = strings.Cut(string(out), "\n")
	return strings.TrimSpace(current), strings.TrimSpace(persisted), nil
}

var hostname = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,62})(\.[A-Za-z0-9]([A-Za-z0-9-]{0,62}))*$`)

// ValidHostname reports whether name is a valid host name.
func ValidHostname(name string) bool {
	return len(name) <= 253 && hostname.MatchString(name)
}

// SetHostname changes the host name, now and after reboots. It uses
// hostnamectl when it works, and /etc/hostname otherwise.
func (c *Client) SetHostname(ctx context.Context, name string) error {
	if !ValidHostname(name) {
		return fmt.Errorf("invalid host name %q", name)
	}
	_, err := c.exec(ctx, script(
		"n="+q(name),
		`if command -v hostnamectl >/dev/null 2>&1 && hostnamectl set-hostname "$n" 2>/dev/null; then exit 0; fi`,
		"set -e",
		`printf '%s\n' "$n" > /etc/hostname`,
		`hostname "$n"`,
	), nil)
	if err != nil {
		return fmt.Errorf("setting host name: %w", err)
	}
	return nil
}

// Facts describe a host, like Ansible facts or Salt grains.
type Facts struct {
	Hostname          string
	KernelRelease     string
	Architecture      string
	OSID              string
	OSIDLike          string
	OSName            string
	OSPrettyName      string
	OSVersionID       string
	OSVersionCodename string
	InitSystem        string
	PackageManager    string
	MachineID         string
	CPUCount          int64
	MemoryTotal       int64
}

const factsScript = `printf 'hostname=%s\n' "$(hostname)"
printf 'kernel_release=%s\n' "$(uname -r)"
printf 'architecture=%s\n' "$(uname -m)"
if [ -r /etc/os-release ]; then
	(
		. /etc/os-release
		printf 'os_id=%s\nos_id_like=%s\nos_name=%s\nos_pretty_name=%s\nos_version_id=%s\nos_version_codename=%s\n' \
			"$ID" "$ID_LIKE" "$NAME" "$PRETTY_NAME" "$VERSION_ID" "$VERSION_CODENAME"
	)
fi
if [ -d /run/systemd/system ]; then
	echo init_system=systemd
else
	printf 'init_system=%s\n' "$(cat /proc/1/comm 2>/dev/null)"
fi
printf 'package_manager=%s\n' "$(` + detectPackageManager + `)"
printf 'machine_id=%s\n' "$(cat /etc/machine-id 2>/dev/null)"
printf 'cpu_count=%s\n' "$(nproc 2>/dev/null || grep -c ^processor /proc/cpuinfo)"
awk '/^MemTotal:/ { printf "memory_total=%.0f\n", $2 * 1024 }' /proc/meminfo
`

// Facts gathers facts about the host.
func (c *Client) Facts(ctx context.Context) (*Facts, error) {
	out, err := c.exec(ctx, factsScript, nil)
	if err != nil {
		return nil, fmt.Errorf("gathering facts: %w", err)
	}
	return parseFacts(string(out))
}

func parseFacts(out string) (*Facts, error) {
	values := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = value
		}
	}
	if values["kernel_release"] == "" {
		return nil, errors.New("gathering facts: unexpected output")
	}
	facts := &Facts{
		Hostname:          values["hostname"],
		KernelRelease:     values["kernel_release"],
		Architecture:      values["architecture"],
		OSID:              values["os_id"],
		OSIDLike:          values["os_id_like"],
		OSName:            values["os_name"],
		OSPrettyName:      values["os_pretty_name"],
		OSVersionID:       values["os_version_id"],
		OSVersionCodename: values["os_version_codename"],
		InitSystem:        values["init_system"],
		PackageManager:    values["package_manager"],
		MachineID:         values["machine_id"],
	}
	if facts.PackageManager == "none" {
		facts.PackageManager = ""
	}
	facts.CPUCount, _ = strconv.ParseInt(values["cpu_count"], 10, 64)
	facts.MemoryTotal, _ = strconv.ParseInt(values["memory_total"], 10, 64)
	return facts, nil
}
