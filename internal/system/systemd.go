package system

import (
	"bufio"
	"context"
	"slices"
	"strings"

	"github.com/labbs/terraform-provider-linux/internal/transport"
)

// UnitStatus is the state of a systemd unit, as systemctl show reports it.
type UnitStatus struct {
	LoadState     string // loaded, not-found, masked...
	ActiveState   string // active, inactive, failed, activating...
	SubState      string // running, exited, dead...
	UnitFileState string // enabled, disabled, static, masked...
}

// Exists reports whether systemd knows the unit.
func (s *UnitStatus) Exists() bool {
	return s.LoadState != "" && s.LoadState != "not-found"
}

// Active reports whether the unit is running or about to.
func (s *UnitStatus) Active() bool {
	return slices.Contains([]string{"active", "activating", "reloading", "refreshing"}, s.ActiveState)
}

// Enabled reports whether the unit starts at boot.
func (s *UnitStatus) Enabled() bool {
	return s.UnitFileState == "enabled" || s.UnitFileState == "enabled-runtime"
}

// Masked reports whether the unit is masked.
func (s *UnitStatus) Masked() bool {
	return s.LoadState == "masked" || strings.HasPrefix(s.UnitFileState, "masked")
}

// CanBeEnabled reports whether systemctl enable and disable change the unit.
// They do not for static, generated or transient units, for instance.
func (s *UnitStatus) CanBeEnabled() bool {
	return slices.Contains([]string{"enabled", "enabled-runtime", "disabled", "linked", "linked-runtime"}, s.UnitFileState)
}

// UnitStatus returns the state of unit.
func (c *Client) UnitStatus(ctx context.Context, unit string) (*UnitStatus, error) {
	out, err := c.exec(ctx, "exec "+transport.Join(
		"systemctl", "show", "--no-pager",
		"--property=LoadState,ActiveState,SubState,UnitFileState", "--", unit,
	), nil)
	if err != nil {
		return nil, wrap(err, "systemctl show", unit)
	}
	return parseUnitStatus(string(out)), nil
}

func parseUnitStatus(out string) *UnitStatus {
	status := &UnitStatus{}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		key, value, _ := strings.Cut(scanner.Text(), "=")
		switch key {
		case "LoadState":
			status.LoadState = value
		case "ActiveState":
			status.ActiveState = value
		case "SubState":
			status.SubState = value
		case "UnitFileState":
			status.UnitFileState = value
		}
	}
	return status
}

// Systemctl runs systemctl command on units, e.g. Systemctl(ctx, "restart",
// "nginx.service") or Systemctl(ctx, "daemon-reload").
func (c *Client) Systemctl(ctx context.Context, command string, units ...string) error {
	args := []string{"systemctl", command}
	if len(units) > 0 {
		// Unit names may start with a dash, like -.mount.
		args = append(append(args, "--"), units...)
	}
	_, err := c.exec(ctx, "exec "+transport.Join(args...), nil)
	return wrap(err, "systemctl", command+" "+strings.Join(units, " "))
}
