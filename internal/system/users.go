package system

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/labbs/terraform-provider-linux/internal/transport"
)

var accountName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9._-]*\$?$`)

// ValidAccountName reports whether name can be a user or group name.
func ValidAccountName(name string) bool {
	return len(name) <= 32 && accountName.MatchString(name)
}

// User describes a user account.
type User struct {
	Name    string
	UID     string
	GID     string
	Comment string
	Home    string
	Shell   string
	// Group is the primary group.
	Group string
	// Groups are the supplementary groups.
	Groups []string
}

// User returns the account called name, or an error satisfying IsNotExist.
func (c *Client) User(ctx context.Context, name string) (*User, error) {
	if !ValidAccountName(name) {
		return nil, fmt.Errorf("invalid user name %q", name)
	}
	out, err := c.exec(ctx, script(
		"n="+q(name),
		`line=$(getent passwd "$n") || exit 44`,
		`printf '%s\n' "$line"`,
		`id -gn "$n"`,
		`id -Gn "$n"`,
	), nil)
	if err != nil {
		return nil, wrap(err, "look up user", name)
	}
	user, err := parseUser(string(out))
	return user, wrap(err, "look up user", name)
}

func parseUser(out string) (*User, error) {
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		return nil, fmt.Errorf("unexpected output %q", out)
	}
	fields := strings.Split(lines[0], ":")
	if len(fields) != 7 {
		return nil, fmt.Errorf("unexpected passwd entry %q", lines[0])
	}
	user := &User{
		Name:    fields[0],
		UID:     fields[2],
		GID:     fields[3],
		Comment: fields[4],
		Home:    fields[5],
		Shell:   fields[6],
		Group:   strings.TrimSpace(lines[1]),
		Groups:  []string{},
	}
	for _, group := range strings.Fields(lines[2]) {
		if group != user.Group && !slices.Contains(user.Groups, group) {
			user.Groups = append(user.Groups, group)
		}
	}
	slices.Sort(user.Groups)
	return user, nil
}

// UserOptions are the settings of a user account. Empty fields are left as
// they are, or to the system defaults on creation.
type UserOptions struct {
	UID     string
	Group   string
	Comment string
	Home    string
	Shell   string
	// Groups replaces the supplementary groups when SetGroups is true.
	Groups    []string
	SetGroups bool
}

func (o UserOptions) args() []string {
	var args []string
	for _, opt := range []struct{ flag, value string }{
		{"-u", o.UID}, {"-g", o.Group}, {"-c", o.Comment}, {"-d", o.Home}, {"-s", o.Shell},
	} {
		if opt.value != "" {
			args = append(args, opt.flag, opt.value)
		}
	}
	if o.SetGroups {
		args = append(args, "-G", strings.Join(o.Groups, ","))
	}
	return args
}

// CreateUser creates a user account with useradd.
func (c *Client) CreateUser(ctx context.Context, name string, o UserOptions, system, createHome bool) error {
	// A new user has no supplementary groups unless some are given.
	o.SetGroups = len(o.Groups) > 0
	args := append([]string{"useradd"}, o.args()...)
	if system {
		args = append(args, "-r")
	}
	if createHome {
		args = append(args, "-m")
	} else {
		args = append(args, "-M")
	}
	_, err := c.exec(ctx, "exec "+transport.Join(append(args, name)...), nil)
	return wrap(err, "create user", name)
}

// ModifyUser changes a user account with usermod.
func (c *Client) ModifyUser(ctx context.Context, name string, o UserOptions) error {
	args := o.args()
	if len(args) == 0 {
		return nil
	}
	_, err := c.exec(ctx, "exec "+transport.Join(append(append([]string{"usermod"}, args...), name)...), nil)
	return wrap(err, "modify user", name)
}

// SetPasswordHash sets the password of a user from its crypt(3) hash. The
// hash is sent on standard input, never on a command line.
func (c *Client) SetPasswordHash(ctx context.Context, name, hash string) error {
	if strings.ContainsAny(hash, ":\n") {
		return fmt.Errorf("set password of %s: invalid hash", name)
	}
	_, err := c.exec(ctx, "exec chpasswd -e", strings.NewReader(name+":"+hash+"\n"))
	return wrap(err, "set password of", name)
}

// DeleteUser deletes a user account, and its home directory when
// removeHome is true.
func (c *Client) DeleteUser(ctx context.Context, name string, removeHome bool) error {
	args := []string{"userdel"}
	if removeHome {
		args = append(args, "-r")
	}
	_, err := c.exec(ctx, "exec "+transport.Join(append(args, name)...), nil)
	return wrap(err, "delete user", name)
}

// Group describes a group.
type Group struct {
	Name    string
	GID     string
	Members []string
}

// Group returns the group called name, or an error satisfying IsNotExist.
func (c *Client) Group(ctx context.Context, name string) (*Group, error) {
	if !ValidAccountName(name) {
		return nil, fmt.Errorf("invalid group name %q", name)
	}
	out, err := c.exec(ctx, script("n="+q(name), `getent group "$n" || exit 44`), nil)
	if err != nil {
		return nil, wrap(err, "look up group", name)
	}
	group, err := parseGroup(string(out))
	return group, wrap(err, "look up group", name)
}

func parseGroup(out string) (*Group, error) {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	fields := strings.Split(line, ":")
	if len(fields) != 4 {
		return nil, fmt.Errorf("unexpected group entry %q", line)
	}
	group := &Group{Name: fields[0], GID: fields[2], Members: []string{}}
	if fields[3] != "" {
		group.Members = strings.Split(fields[3], ",")
	}
	return group, nil
}

// CreateGroup creates a group with groupadd.
func (c *Client) CreateGroup(ctx context.Context, name, gid string, system bool) error {
	args := []string{"groupadd"}
	if gid != "" {
		args = append(args, "-g", gid)
	}
	if system {
		args = append(args, "-r")
	}
	_, err := c.exec(ctx, "exec "+transport.Join(append(args, name)...), nil)
	return wrap(err, "create group", name)
}

// SetGroupID changes the GID of a group.
func (c *Client) SetGroupID(ctx context.Context, name, gid string) error {
	_, err := c.exec(ctx, "exec "+transport.Join("groupmod", "-g", gid, name), nil)
	return wrap(err, "modify group", name)
}

// DeleteGroup deletes a group.
func (c *Client) DeleteGroup(ctx context.Context, name string) error {
	_, err := c.exec(ctx, "exec "+transport.Join("groupdel", name), nil)
	return wrap(err, "delete group", name)
}
