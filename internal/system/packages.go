package system

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// PackageManagers lists the supported package managers, in the order they
// are looked for.
var PackageManagers = []string{"apt", "dnf", "yum", "zypper", "apk", "pacman"}

var (
	packageName    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+:@-]*$`)
	packageVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+:~-]*$`)
)

// ValidPackageName reports whether name can be a package name.
func ValidPackageName(name string) bool {
	return packageName.MatchString(name)
}

// ValidPackageVersion reports whether version can be a package version.
func ValidPackageVersion(version string) bool {
	return packageVersion.MatchString(version)
}

// detectPackageManager prints the first package manager found.
const detectPackageManager = `for pm in apt-get dnf yum zypper apk pacman; do
	if command -v "$pm" >/dev/null 2>&1; then
		[ "$pm" = apt-get ] && pm=apt
		echo "$pm"
		exit 0
	fi
done
echo none`

// PackageManager returns the package manager of the host, one of
// PackageManagers.
func (c *Client) PackageManager(ctx context.Context) (string, error) {
	c.packageManagerMu.Lock()
	defer c.packageManagerMu.Unlock()
	if c.packageManager != "" {
		return c.packageManager, nil
	}
	out, err := c.exec(ctx, detectPackageManager, nil)
	if err != nil {
		return "", fmt.Errorf("detecting the package manager: %w", err)
	}
	pm := strings.TrimSpace(string(out))
	if pm == "none" {
		return "", fmt.Errorf("no supported package manager found: expected one of %s", strings.Join(PackageManagers, ", "))
	}
	c.packageManager = pm
	return pm, nil
}

// Package returns the installed version of a package, or an error
// satisfying IsNotExist when it is not installed.
func (c *Client) Package(ctx context.Context, name string) (string, error) {
	if !ValidPackageName(name) {
		return "", fmt.Errorf("invalid package name %q", name)
	}
	pm, err := c.PackageManager(ctx)
	if err != nil {
		return "", err
	}
	var query string
	switch pm {
	case "apt":
		query = `out=$(dpkg-query -W -f='${Status}\t${Version}\n' "$n" 2>/dev/null) || exit 44
printf '%s\n' "$out" | awk -F '\t' '$1 ~ / installed$/ { print $2; found = 1; exit } END { if (!found) exit 44 }'`
	case "dnf", "yum", "zypper":
		query = `rpm -q --qf '%{VERSION}-%{RELEASE}\n' "$n" 2>/dev/null || exit 44`
	case "apk":
		// The database format is the same for apk-tools 2 and 3.
		query = `awk -v p="P:$n" '$0 == p { found = 1; next } found && /^V:/ { print substr($0, 3); exit } /^$/ { found = 0 }' /lib/apk/db/installed | grep . || exit 44`
	case "pacman":
		query = `out=$(pacman -Q "$n" 2>/dev/null) || exit 44
printf '%s\n' "${out#* }"`
	}
	out, err := c.exec(ctx, script("n="+q(name), query), nil)
	if err != nil {
		return "", wrap(err, "query package", name)
	}
	version, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return version, nil
}

// InstallPackage installs a package, at version when it is set. Package
// operations are serialized: package managers lock their database.
func (c *Client) InstallPackage(ctx context.Context, name, version string) error {
	if !ValidPackageName(name) {
		return fmt.Errorf("invalid package name %q", name)
	}
	if version != "" && !ValidPackageVersion(version) {
		return fmt.Errorf("invalid package version %q", version)
	}
	pm, err := c.PackageManager(ctx)
	if err != nil {
		return err
	}
	defer c.lock("packages")()

	var cmd string
	switch pm {
	case "apt":
		spec, extra := name, ""
		if version != "" {
			spec, extra = name+"="+version, " --allow-downgrades"
		}
		install := aptGet + extra + " install " + q(spec)
		// A fresh host may not have package lists yet.
		cmd = "export DEBIAN_FRONTEND=noninteractive\n" + install + " || { " + aptGet + " update && " + install + "; }"
	case "dnf", "yum":
		spec := name
		if version != "" {
			spec = name + "-" + version
		}
		cmd = pm + " -y install " + q(spec)
		if version != "" {
			cmd += " || " + pm + " -y downgrade " + q(spec)
		}
	case "zypper":
		if version != "" {
			cmd = "zypper --non-interactive install --oldpackage " + q(name+"="+version)
		} else {
			cmd = "zypper --non-interactive install " + q(name)
		}
	case "apk":
		spec := name
		if version != "" {
			spec = name + "=" + version
		}
		cmd = "apk add " + q(spec)
	case "pacman":
		if version != "" {
			return errors.New("pacman cannot install a given version of a package")
		}
		cmd = "pacman -S --noconfirm --needed " + q(name)
	}
	_, err = c.exec(ctx, cmd, nil)
	return wrap(err, "install package", name)
}

// aptGet runs apt-get without prompts, waiting for the dpkg lock and
// keeping modified configuration files.
const aptGet = "apt-get -q -y -o DPkg::Lock::Timeout=300 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold"

// RemovePackage uninstalls a package.
func (c *Client) RemovePackage(ctx context.Context, name string) error {
	if !ValidPackageName(name) {
		return fmt.Errorf("invalid package name %q", name)
	}
	pm, err := c.PackageManager(ctx)
	if err != nil {
		return err
	}
	defer c.lock("packages")()

	var cmd string
	switch pm {
	case "apt":
		cmd = "export DEBIAN_FRONTEND=noninteractive\n" + aptGet + " remove " + q(name)
	case "dnf", "yum":
		cmd = pm + " -y remove " + q(name)
	case "zypper":
		cmd = "zypper --non-interactive remove " + q(name)
	case "apk":
		cmd = "apk del " + q(name)
	case "pacman":
		cmd = "pacman -R --noconfirm " + q(name)
	}
	_, err = c.exec(ctx, cmd, nil)
	return wrap(err, "remove package", name)
}
