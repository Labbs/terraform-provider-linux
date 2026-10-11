package provider

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"golang.org/x/crypto/ssh"

	"github.com/labbs/terraform-provider-linux/internal/system"
	"github.com/labbs/terraform-provider-linux/internal/transport"
)

// testAccProviders serves the provider in-process for acceptance tests. The
// target host comes from the LINUX_* environment variables, e.g.
// LINUX_MODE=ssh LINUX_SSH_HOST=127.0.0.1 LINUX_SSH_PORT=2222.
var testAccProviders = map[string]func() (tfprotov6.ProviderServer, error){
	"linux": providerserver.NewProtocol6WithError(New("test")()),
}

// testAccPreCheckLinux skips tests that need GNU or BusyBox userland when
// the target is the local machine and it does not run Linux.
func testAccPreCheckLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && os.Getenv("LINUX_MODE") != modeSSH && os.Getenv("LINUX_SSH_HOST") == "" {
		t.Skip("requires a Linux target: run on Linux, or set LINUX_MODE=ssh and LINUX_SSH_* to reach one")
	}
}

// testAccPreCheckSystemd skips tests that need systemd on the target.
func testAccPreCheckSystemd(t *testing.T) {
	t.Helper()
	testAccPreCheckLinux(t)
	if os.Getenv("LINUX_ACC_SYSTEMD") == "" {
		t.Skip("requires systemd on the target: set LINUX_ACC_SYSTEMD=1")
	}
}

// testAccPreCheckRoot skips tests that change the target system-wide
// (packages, users, kernel parameters...): run them on throwaway hosts only.
func testAccPreCheckRoot(t *testing.T) {
	t.Helper()
	testAccPreCheckLinux(t)
	if os.Getenv("LINUX_ACC_ROOT") == "" {
		t.Skip("changes the target system-wide: set LINUX_ACC_ROOT=1 on a throwaway host")
	}
}

// testClient returns a client for the target the acceptance tests use.
func testClient(t *testing.T) *system.Client {
	t.Helper()
	runner, diags := newRunner(context.Background(), providerModel{
		Mode:         types.StringNull(),
		Sudo:         types.BoolNull(),
		SudoPassword: types.StringNull(),
		SSH:          types.ObjectNull(map[string]attr.Type{}),
	})
	if diags.HasError() {
		t.Fatalf("configuring test client: %v", diags)
	}
	t.Cleanup(func() { _ = runner.Close() })
	return system.New(runner)
}

// testPath returns a unique path under /tmp on the target.
func testPath(name string) string {
	return "/tmp/tf-acc-" + acctest.RandString(10) + "/" + name
}

func TestProviderSchemas(t *testing.T) {
	server, err := providerserver.NewProtocol6WithError(New("test")())()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range resp.Diagnostics {
		t.Errorf("%s: %s", d.Summary, d.Detail)
	}

	want := map[string][]string{
		"resources": {
			"linux_command", "linux_cron_job", "linux_directory", "linux_file", "linux_file_block", "linux_file_line",
			"linux_group", "linux_hostname", "linux_package", "linux_patch", "linux_sed", "linux_ssh_authorized_key",
			"linux_symlink", "linux_sysctl", "linux_systemd_unit", "linux_user",
		},
		"data sources": {
			"linux_command", "linux_file", "linux_group", "linux_host", "linux_package", "linux_sed", "linux_stat",
			"linux_systemd_unit", "linux_user",
		},
		"actions": {"linux_command", "linux_systemctl"},
	}
	got := map[string]map[string]bool{"resources": {}, "data sources": {}, "actions": {}}
	for name := range resp.ResourceSchemas {
		got["resources"][name] = true
	}
	for name := range resp.DataSourceSchemas {
		got["data sources"][name] = true
	}
	for name := range resp.ActionSchemas {
		got["actions"][name] = true
	}
	for kind, names := range want {
		for _, name := range names {
			if !got[kind][name] {
				t.Errorf("missing %s %s", kind, name)
			}
		}
		if len(got[kind]) != len(names) {
			t.Errorf("%s: got %d, want %d", kind, len(got[kind]), len(names))
		}
	}
}

func clearLinuxEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "LINUX_") {
			t.Setenv(name, "")
			_ = os.Unsetenv(name)
		}
	}
}

func TestNewRunner(t *testing.T) {
	ctx := context.Background()
	null := providerModel{
		Mode:         types.StringNull(),
		Sudo:         types.BoolNull(),
		SudoPassword: types.StringNull(),
		SSH:          types.ObjectNull(map[string]attr.Type{}),
	}

	t.Run("local by default", func(t *testing.T) {
		clearLinuxEnv(t)
		runner, diags := newRunner(ctx, null)
		if diags.HasError() {
			t.Fatal(diags)
		}
		if _, ok := runner.(*transport.Local); !ok {
			t.Fatalf("got %T", runner)
		}
	})

	t.Run("ssh from environment with sudo", func(t *testing.T) {
		clearLinuxEnv(t)
		t.Setenv("LINUX_SSH_HOST", "203.0.113.7")
		t.Setenv("LINUX_SSH_PORT", "2222")
		t.Setenv("LINUX_SSH_USER", "deploy")
		t.Setenv("LINUX_SSH_PASSWORD", "pw")
		t.Setenv("LINUX_SUDO", "true")
		runner, diags := newRunner(ctx, null)
		if diags.HasError() {
			t.Fatal(diags)
		}
		if got, want := runner.String(), "ssh://deploy@203.0.113.7:2222 (sudo)"; got != want {
			t.Fatalf("got %s, want %s", got, want)
		}
	})

	t.Run("ssh without host", func(t *testing.T) {
		clearLinuxEnv(t)
		cfg := null
		cfg.Mode = types.StringValue(modeSSH)
		if _, diags := newRunner(ctx, cfg); !diags.HasError() {
			t.Fatal("expected an error")
		}
	})

	t.Run("invalid boolean in environment", func(t *testing.T) {
		clearLinuxEnv(t)
		t.Setenv("LINUX_SUDO", "maybe")
		if _, diags := newRunner(ctx, null); !diags.HasError() {
			t.Fatal("expected an error")
		}
	})
}

func TestSSHModelConfig(t *testing.T) {
	clearLinuxEnv(t)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}

	bastion := endpointModel{
		Host:                  types.StringValue("bastion.example.com"),
		Port:                  types.Int64Null(),
		User:                  types.StringValue("jump"),
		Password:              types.StringNull(),
		PrivateKey:            types.StringNull(),
		PrivateKeyPath:        types.StringValue(keyFile),
		PrivateKeyPassphrase:  types.StringNull(),
		Agent:                 types.BoolValue(false),
		HostKey:               types.StringNull(),
		InsecureIgnoreHostKey: types.BoolNull(),
	}
	bastionObj, diags := types.ObjectValueFrom(context.Background(), endpointAttrTypes(), bastion)
	if diags.HasError() {
		t.Fatal(diags)
	}

	m := sshModel{
		endpointModel: endpointModel{
			Host:                  types.StringValue("10.0.0.5"),
			Port:                  types.Int64Value(2200),
			User:                  types.StringNull(),
			Password:              types.StringValue("pw"),
			PrivateKey:            types.StringNull(),
			PrivateKeyPath:        types.StringNull(),
			PrivateKeyPassphrase:  types.StringNull(),
			Agent:                 types.BoolNull(),
			HostKey:               types.StringNull(),
			InsecureIgnoreHostKey: types.BoolValue(true),
		},
		KnownHostsFile: types.StringValue("~/custom_known_hosts"),
		Timeout:        types.StringValue("5s"),
		MaxSessions:    types.Int64Value(3),
		Bastion:        bastionObj,
	}
	cfg, maxSessions, err := m.config(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	switch {
	case cfg.Host != "10.0.0.5" || cfg.Port != 2200 || cfg.User != defaultSSHUser || cfg.Password != "pw":
		t.Errorf("unexpected endpoint %+v", cfg)
	case !cfg.Agent || !cfg.InsecureIgnoreHostKey || cfg.Timeout.String() != "5s" || maxSessions != 3:
		t.Errorf("unexpected options %+v, max sessions %d", cfg, maxSessions)
	case cfg.KnownHostsFile != filepath.Join(home, "custom_known_hosts"):
		t.Errorf("known hosts file = %s", cfg.KnownHostsFile)
	case cfg.Bastion == nil || cfg.Bastion.Host != "bastion.example.com" || cfg.Bastion.Port != 22 || cfg.Bastion.Agent:
		t.Errorf("unexpected bastion %+v", cfg.Bastion)
	case !strings.Contains(cfg.Bastion.PrivateKey, "OPENSSH PRIVATE KEY") || cfg.Bastion.KnownHostsFile != cfg.KnownHostsFile:
		t.Errorf("bastion key or known hosts not set: %+v", cfg.Bastion)
	}
}

func endpointAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"host": types.StringType, "port": types.Int64Type, "user": types.StringType,
		"password": types.StringType, "private_key": types.StringType, "private_key_path": types.StringType,
		"private_key_passphrase": types.StringType, "agent": types.BoolType, "host_key": types.StringType,
		"insecure_ignore_host_key": types.BoolType,
	}
}

func TestModeAndOwnerValues(t *testing.T) {
	if got := modeValue(types.StringValue("644"), "0644"); got.ValueString() != "644" {
		t.Errorf("modeValue kept %s", got)
	}
	if got := modeValue(types.StringValue("644"), "0600"); got.ValueString() != "0600" {
		t.Errorf("modeValue = %s", got)
	}
	if got := modeValue(types.StringNull(), "0755"); got.ValueString() != "0755" {
		t.Errorf("modeValue = %s", got)
	}
	if got := ownerValue(types.StringValue("0"), "root", "0"); got.ValueString() != "0" {
		t.Errorf("ownerValue = %s", got)
	}
	if got := ownerValue(types.StringValue("www-data"), "root", "0"); got.ValueString() != "root" {
		t.Errorf("ownerValue = %s", got)
	}
}

// checkRemoteFile checks the content and mode of a file on the target.
func checkRemoteFile(t *testing.T, p, content, mode string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		c := testClient(t)
		info, err := c.Stat(context.Background(), p)
		if err != nil {
			return err
		}
		if info.Mode != mode {
			return fmt.Errorf("%s: mode is %s, want %s", p, info.Mode, mode)
		}
		data, err := c.ReadFile(context.Background(), p)
		if err != nil {
			return err
		}
		if string(data) != content {
			return fmt.Errorf("%s: content is %q, want %q", p, data, content)
		}
		return nil
	}
}

// remoteExists reports whether something exists at p on the target. It
// only relies on /bin/sh, so it also works against a local macOS.
func remoteExists(t *testing.T, p string) (bool, error) {
	q := transport.Quote(p)
	_, err := testClient(t).Run(context.Background(), "[ -e "+q+" ] || [ -L "+q+" ]", nil)
	if _, ok := transport.ExitCode(err); ok {
		return false, nil
	}
	return err == nil, err
}

// checkRemoteExists checks that something exists at p on the target.
func checkRemoteExists(t *testing.T, p string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		exists, err := remoteExists(t, p)
		if err == nil && !exists {
			err = fmt.Errorf("%s does not exist", p)
		}
		return err
	}
}

// checkRemoteMissing checks that nothing exists at p on the target.
func checkRemoteMissing(t *testing.T, p string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		exists, err := remoteExists(t, p)
		if err == nil && exists {
			err = fmt.Errorf("%s still exists", p)
		}
		return err
	}
}

// cleanupRemoteDir removes the directory of p on the target after the test.
func cleanupRemoteDir(t *testing.T, p string) {
	t.Cleanup(func() {
		_ = testClient(t).RemoveDir(context.Background(), path.Dir(p), true)
	})
}

const testSSHDConfig = `# managed by hand
Port 22
PermitRootLogin yes
UsePAM yes
`

// writeSSHDConfig writes testSSHDConfig to p on the target, outside of
// Terraform.
func writeSSHDConfig(t *testing.T, p string) {
	t.Helper()
	err := testClient(t).WriteFile(context.Background(), p, strings.NewReader(testSSHDConfig), system.Attributes{Mode: "0640"}, true)
	if err != nil {
		t.Fatal(err)
	}
}

// checkUnit checks the state of a unit on the target.
func checkUnit(t *testing.T, unit string, enabled, active bool) resource.TestCheckFunc {
	return func(*terraform.State) error {
		status, err := testClient(t).UnitStatus(context.Background(), unit)
		if err != nil {
			return err
		}
		if status.Enabled() != enabled || status.Active() != active {
			return fmt.Errorf("%s: enabled=%t active=%t, want enabled=%t active=%t",
				unit, status.Enabled(), status.Active(), enabled, active)
		}
		return nil
	}
}
