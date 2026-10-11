package system

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseUser(t *testing.T) {
	user, err := parseUser("app:x:1001:1001:App user,,,:/srv/app:/bin/bash\napp\napp docker adm docker\n")
	if err != nil {
		t.Fatal(err)
	}
	if user.UID != "1001" || user.Home != "/srv/app" || user.Shell != "/bin/bash" || user.Comment != "App user,,," || user.Group != "app" {
		t.Errorf("unexpected %+v", *user)
	}
	if !slices.Equal(user.Groups, []string{"adm", "docker"}) {
		t.Errorf("groups = %q", user.Groups)
	}
	if _, err := parseUser("garbage\n"); err == nil {
		t.Error("expected an error")
	}
}

func TestParseGroup(t *testing.T) {
	group, err := parseGroup("docker:x:999:alice,bob\n")
	if err != nil || group.GID != "999" || !slices.Equal(group.Members, []string{"alice", "bob"}) {
		t.Fatalf("parseGroup = %+v, %v", group, err)
	}
	group, err = parseGroup("empty:x:1000:\n")
	if err != nil || len(group.Members) != 0 {
		t.Fatalf("parseGroup = %+v, %v", group, err)
	}
}

func TestParseFacts(t *testing.T) {
	facts, err := parseFacts("hostname=web1\nkernel_release=6.1.0-18-amd64\narchitecture=x86_64\nos_id=debian\nos_version_id=12\npackage_manager=apt\ncpu_count=4\nmemory_total=8323002368\ninit_system=systemd\n")
	if err != nil {
		t.Fatal(err)
	}
	if facts.Hostname != "web1" || facts.OSID != "debian" || facts.CPUCount != 4 || facts.MemoryTotal != 8323002368 || facts.PackageManager != "apt" {
		t.Errorf("unexpected %+v", *facts)
	}
	if facts, _ := parseFacts("kernel_release=x\npackage_manager=none\n"); facts.PackageManager != "" {
		t.Errorf("package manager = %q", facts.PackageManager)
	}
}

func TestValidators(t *testing.T) {
	for name, ok := range map[string]bool{"nginx": true, "libc6:amd64": true, "g++": true, "python3.12": true, "-rf": false, "a b": false} {
		if ValidPackageName(name) != ok {
			t.Errorf("ValidPackageName(%q) != %v", name, ok)
		}
	}
	for name, ok := range map[string]bool{"deploy": true, "www-data": true, "_apt": true, "machine$": true, "1abc": false, "-x": false, "a:b": false} {
		if ValidAccountName(name) != ok {
			t.Errorf("ValidAccountName(%q) != %v", name, ok)
		}
	}
	for name, ok := range map[string]bool{"web-1": true, "web1.example.com": true, "-web": false, "web_1": false, "": false} {
		if ValidHostname(name) != ok {
			t.Errorf("ValidHostname(%q) != %v", name, ok)
		}
	}
	if got := NormalizeSysctl("32768\t60999\n"); got != "32768 60999" {
		t.Errorf("NormalizeSysctl = %q", got)
	}
}

// The following tests change the host: they need root on a Linux host and
// LINUX_TEST_ROOT=1. Run them in a throwaway container.

func rootClient(t *testing.T) *Client {
	t.Helper()
	c := linuxClient(t)
	if os.Getenv("LINUX_TEST_ROOT") == "" {
		t.Skip("changes the host: set LINUX_TEST_ROOT=1 to run")
	}
	return c
}

func TestEditFileAndSymlink(t *testing.T) {
	c := linuxClient(t)
	ctx := context.Background()
	dir := t.TempDir()
	p := filepath.Join(dir, "conf")

	if _, err := c.EditFile(ctx, p, nil, func(b []byte) ([]byte, error) { return b, nil }); !IsNotExist(err) {
		t.Fatalf("EditFile on a missing file without create: %v", err)
	}
	changed, err := c.EditFile(ctx, p, &Attributes{Mode: "0600"}, func(b []byte) ([]byte, error) { return append(b, "a\n"...), nil })
	if err != nil || !changed {
		t.Fatalf("EditFile = %v, %v", changed, err)
	}
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	changed, err = c.EditFile(ctx, p, nil, func(b []byte) ([]byte, error) { return b, nil })
	if err != nil || changed {
		t.Fatalf("no-op EditFile = %v, %v", changed, err)
	}
	if _, err := c.EditFile(ctx, p, nil, func(b []byte) ([]byte, error) { return append(b, "b\n"...), nil }); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(p); string(data) != "a\nb\n" {
		t.Errorf("content = %q", data)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
		t.Errorf("EditFile changed the mode to %o", fi.Mode().Perm())
	}

	link := filepath.Join(dir, "sub", "link")
	if err := c.Symlink(ctx, p, link, true); err != nil {
		t.Fatal(err)
	}
	if target, err := c.Readlink(ctx, link); err != nil || target != p {
		t.Fatalf("Readlink = %q, %v", target, err)
	}
	if err := c.Symlink(ctx, "/etc/hostname", link, false); err != nil {
		t.Fatal(err)
	}
	if target, _ := c.Readlink(ctx, link); target != "/etc/hostname" {
		t.Errorf("Readlink after update = %q", target)
	}
	if info, err := c.StatFollow(ctx, link); err != nil || info.Type != TypeFile {
		t.Errorf("StatFollow = %+v, %v", info, err)
	}
	if err := c.Symlink(ctx, p, dir, false); err == nil {
		t.Error("expected an error replacing a directory")
	}
}

func TestPackages(t *testing.T) {
	c := rootClient(t)
	ctx := context.Background()
	pm, err := c.PackageManager(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("package manager: %s", pm)

	if _, err := c.Package(ctx, "jq"); !IsNotExist(err) {
		t.Fatalf("jq should not be installed: %v", err)
	}
	if err := c.InstallPackage(ctx, "jq", ""); err != nil {
		t.Fatal(err)
	}
	version, err := c.Package(ctx, "jq")
	if err != nil || version == "" {
		t.Fatalf("Package = %q, %v", version, err)
	}
	t.Logf("jq %s", version)
	// Installing the installed version is a no-op.
	if err := c.InstallPackage(ctx, "jq", version); err != nil {
		t.Fatal(err)
	}
	if err := c.RemovePackage(ctx, "jq"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Package(ctx, "jq"); !IsNotExist(err) {
		t.Fatalf("jq should be removed: %v", err)
	}
}

func TestUsersAndGroups(t *testing.T) {
	c := rootClient(t)
	ctx := context.Background()

	if err := c.CreateGroup(ctx, "tfteam", "4242", false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.DeleteGroup(ctx, "tfteam") })
	if group, err := c.Group(ctx, "tfteam"); err != nil || group.GID != "4242" {
		t.Fatalf("Group = %+v, %v", group, err)
	}
	if err := c.SetGroupID(ctx, "tfteam", "4243"); err != nil {
		t.Fatal(err)
	}

	if _, err := c.User(ctx, "tfuser"); !IsNotExist(err) {
		t.Fatalf("User on a missing user: %v", err)
	}
	err := c.CreateUser(ctx, "tfuser", UserOptions{UID: "4242", Comment: "Terraform test", Shell: "/bin/sh", Groups: []string{"tfteam"}}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.DeleteUser(ctx, "tfuser", true) })
	user, err := c.User(ctx, "tfuser")
	if err != nil {
		t.Fatal(err)
	}
	if user.UID != "4242" || user.Comment != "Terraform test" || user.Shell != "/bin/sh" || !slices.Equal(user.Groups, []string{"tfteam"}) {
		t.Errorf("unexpected %+v", *user)
	}
	if _, err := os.Stat(user.Home); err != nil {
		t.Errorf("home not created: %v", err)
	}

	if err := c.ModifyUser(ctx, "tfuser", UserOptions{Shell: "/bin/bash", SetGroups: true}); err != nil {
		t.Fatal(err)
	}
	if user, _ := c.User(ctx, "tfuser"); user.Shell != "/bin/bash" || len(user.Groups) != 0 {
		t.Errorf("after usermod: %+v", *user)
	}
	// chpasswd -e stores the hash as is, without checking it.
	if err := c.SetPasswordHash(ctx, "tfuser", "$6$salt$IxDD3jeSOb5eB1CX5LBsqZFVkJdido3OUILO5Ifz5iwMuTS4XMS130MTSuDDl3aCI6WouIL9AjRbLCelDCy.g."); err != nil {
		t.Fatal(err)
	}
	if shadow, _ := os.ReadFile("/etc/shadow"); !strings.Contains(string(shadow), "tfuser:$6$salt$") {
		t.Error("password hash not set")
	}

	if err := c.DeleteUser(ctx, "tfuser", true); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteGroup(ctx, "tfteam"); err != nil {
		t.Fatal(err)
	}
}

func TestCrontab(t *testing.T) {
	c := rootClient(t)
	ctx := context.Background()
	t.Cleanup(func() { _, _ = c.Run(ctx, "crontab -r", nil) })

	if crontab, err := c.Crontab(ctx, ""); err != nil || crontab != "" {
		t.Fatalf("Crontab = %q, %v", crontab, err)
	}
	changed, err := c.EditCrontab(ctx, "root", func(s string) (string, error) { return s + "@reboot true\n", nil })
	if err != nil || !changed {
		t.Fatalf("EditCrontab = %v, %v", changed, err)
	}
	if crontab, err := c.Crontab(ctx, "root"); err != nil || !strings.Contains(crontab, "@reboot true\n") {
		t.Fatalf("Crontab = %q, %v", crontab, err)
	}
}

func TestSysctlAndHostname(t *testing.T) {
	c := rootClient(t)
	ctx := context.Background()

	// A per-network-namespace parameter, so that a container does not
	// change its host.
	const param = "net.ipv4.ip_local_port_range"
	before, err := c.Sysctl(ctx, param)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.SetSysctl(ctx, param, before) })
	if err := c.SetSysctl(ctx, param, "20000 40000"); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Sysctl(ctx, param); got != "20000 40000" {
		t.Errorf("Sysctl = %q", got)
	}

	current, _, err := c.Hostname(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.SetHostname(ctx, current) })
	if err := c.SetHostname(ctx, "tf-test-host"); err != nil {
		t.Fatal(err)
	}
	if now, persisted, _ := c.Hostname(ctx); now != "tf-test-host" || persisted != "tf-test-host" {
		t.Errorf("Hostname = %q, %q", now, persisted)
	}

	facts, err := c.Facts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Hostname != "tf-test-host" || facts.OSID == "" || facts.CPUCount == 0 || facts.MemoryTotal == 0 || facts.PackageManager == "" {
		t.Errorf("unexpected facts %+v", *facts)
	}
}
