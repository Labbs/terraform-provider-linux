// Package provider implements the linux Terraform provider.
package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/labbs/terraform-provider-linux/internal/system"
	"github.com/labbs/terraform-provider-linux/internal/transport"
)

const (
	modeLocal = "local"
	modeSSH   = "ssh"

	defaultSSHUser = "root"
)

var (
	_ provider.Provider                   = (*linuxProvider)(nil)
	_ provider.ProviderWithActions        = (*linuxProvider)(nil)
	_ provider.ProviderWithValidateConfig = (*linuxProvider)(nil)
)

type linuxProvider struct {
	version string
}

// New returns a constructor for the provider.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &linuxProvider{version: version}
	}
}

type providerModel struct {
	Mode         types.String `tfsdk:"mode"`
	Sudo         types.Bool   `tfsdk:"sudo"`
	SudoPassword types.String `tfsdk:"sudo_password"`
	SSH          types.Object `tfsdk:"ssh"`
}

// endpointModel holds the attributes shared by the target and the bastion.
type endpointModel struct {
	Host                  types.String `tfsdk:"host"`
	Port                  types.Int64  `tfsdk:"port"`
	User                  types.String `tfsdk:"user"`
	Password              types.String `tfsdk:"password"`
	PrivateKey            types.String `tfsdk:"private_key"`
	PrivateKeyPath        types.String `tfsdk:"private_key_path"`
	PrivateKeyPassphrase  types.String `tfsdk:"private_key_passphrase"`
	Agent                 types.Bool   `tfsdk:"agent"`
	HostKey               types.String `tfsdk:"host_key"`
	InsecureIgnoreHostKey types.Bool   `tfsdk:"insecure_ignore_host_key"`
}

type sshModel struct {
	endpointModel
	KnownHostsFile types.String `tfsdk:"known_hosts_file"`
	Timeout        types.String `tfsdk:"timeout"`
	MaxSessions    types.Int64  `tfsdk:"max_sessions"`
	Bastion        types.Object `tfsdk:"bastion"`
}

func (p *linuxProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "linux"
	resp.Version = p.version
}

func (p *linuxProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	sshAttributes := endpointAttributes("LINUX_SSH_")
	sshAttributes["known_hosts_file"] = schema.StringAttribute{
		Optional:            true,
		MarkdownDescription: "known_hosts file used to verify host keys, for the host and the bastion. Defaults to `~/.ssh/known_hosts`. Environment variable: `LINUX_SSH_KNOWN_HOSTS_FILE`.",
	}
	sshAttributes["timeout"] = schema.StringAttribute{
		Optional:            true,
		MarkdownDescription: "Maximum time to establish the connection, as a Go duration such as `30s` or `1m`. Defaults to `30s`.",
		Validators:          []validator.String{durationValidator{}},
	}
	sshAttributes["max_sessions"] = schema.Int64Attribute{
		Optional:            true,
		MarkdownDescription: fmt.Sprintf("Maximum number of commands run at the same time over the connection. Keep it below the `MaxSessions` setting of the SSH server (10 by default). Defaults to `%d`.", transport.DefaultMaxSessions),
		Validators:          []validator.Int64{int64validator.AtLeast(1)},
	}
	sshAttributes["bastion"] = schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: "Jump host used to reach `host`.",
		Attributes:          endpointAttributes(""),
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages files, packages, users, services and other settings of a Linux host, either the machine running Terraform (`mode = \"local\"`) or a remote host over SSH (`mode = \"ssh\"`).",
		Attributes: map[string]schema.Attribute{
			"mode": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Where commands run: `local` (the machine running Terraform) or `ssh`. Defaults to `ssh` when `ssh` is set, `local` otherwise. Environment variable: `LINUX_MODE`.",
				Validators:          []validator.String{stringvalidator.OneOf(modeLocal, modeSSH)},
			},
			"sudo": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Run every command as root through `sudo`. Environment variable: `LINUX_SUDO`.",
			},
			"sudo_password": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Password `sudo` asks for. Leave unset when `sudo` does not ask for one (`NOPASSWD`). It is sent on the standard input of `sudo`, never on a command line. Environment variable: `LINUX_SUDO_PASSWORD`.",
			},
			"ssh": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Connection to the remote host, used when `mode` is `ssh`.",
				Attributes:          sshAttributes,
			},
		},
	}
}

// endpointAttributes returns the attributes describing an SSH endpoint. When
// envPrefix is set, the documentation mentions the environment variables
// the attributes default to.
func endpointAttributes(envPrefix string) map[string]schema.Attribute {
	env := func(name string) string {
		if envPrefix == "" {
			return ""
		}
		return fmt.Sprintf(" Environment variable: `%s%s`.", envPrefix, name)
	}
	return map[string]schema.Attribute{
		"host": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Host name or IP address. Required." + env("HOST"),
		},
		"port": schema.Int64Attribute{
			Optional:            true,
			MarkdownDescription: "SSH port. Defaults to `22`." + env("PORT"),
			Validators:          []validator.Int64{int64validator.Between(1, 65535)},
		},
		"user": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "User to log in as. Defaults to `root`." + env("USER"),
		},
		"password": schema.StringAttribute{
			Optional:            true,
			Sensitive:           true,
			MarkdownDescription: "Password, for password and keyboard-interactive authentication." + env("PASSWORD"),
		},
		"private_key": schema.StringAttribute{
			Optional:            true,
			Sensitive:           true,
			MarkdownDescription: "Private key in PEM or OpenSSH format." + env("PRIVATE_KEY"),
			Validators: []validator.String{
				stringvalidator.ConflictsWith(path.MatchRelative().AtParent().AtName("private_key_path")),
			},
		},
		"private_key_path": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Path to the private key, on the machine running Terraform. `~` expands to the home directory." + env("PRIVATE_KEY_PATH"),
		},
		"private_key_passphrase": schema.StringAttribute{
			Optional:            true,
			Sensitive:           true,
			MarkdownDescription: "Passphrase of an encrypted private key." + env("PRIVATE_KEY_PASSPHRASE"),
		},
		"agent": schema.BoolAttribute{
			Optional:            true,
			MarkdownDescription: "Authenticate with the keys of the SSH agent listening on `SSH_AUTH_SOCK`. Defaults to `true`.",
		},
		"host_key": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Expected public key of the host, in `authorized_keys` format (`ssh-ed25519 AAAA...`). Takes precedence over `known_hosts_file`." + env("HOST_KEY"),
			Validators: []validator.String{
				stringvalidator.ConflictsWith(path.MatchRelative().AtParent().AtName("insecure_ignore_host_key")),
			},
		},
		"insecure_ignore_host_key": schema.BoolAttribute{
			Optional:            true,
			MarkdownDescription: "Accept any host key. This makes the connection vulnerable to interception: use it only for tests." + env("INSECURE_IGNORE_HOST_KEY"),
		},
	}
}

func (p *linuxProvider) ValidateConfig(ctx context.Context, req provider.ValidateConfigRequest, resp *provider.ValidateConfigResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.Mode.ValueString() == modeLocal && !cfg.SSH.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("ssh"), "Unexpected SSH configuration",
			`ssh is only used when mode is "ssh". Remove it or change mode.`)
	}
	if !cfg.Sudo.IsNull() && !cfg.Sudo.IsUnknown() && !cfg.Sudo.ValueBool() && !cfg.SudoPassword.IsNull() {
		resp.Diagnostics.AddAttributeWarning(path.Root("sudo_password"), "sudo_password is ignored",
			"sudo_password is only used when sudo is true.")
	}
}

func (p *linuxProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var client *system.Client
	if req.Config.Raw.IsFullyKnown() {
		var cfg providerModel
		resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
		if resp.Diagnostics.HasError() {
			return
		}
		runner, diags := newRunner(ctx, cfg)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		client = system.New(runner)
	} else {
		// Resources being created do not need the provider during plan:
		// fail only if something tries to use it.
		client = system.New(transport.Unavailable{
			Err: errors.New("the provider configuration depends on values that are not known yet, such as attributes of resources that have not been created"),
		})
	}

	resp.ResourceData = client
	resp.DataSourceData = client
	resp.ActionData = client
}

func newRunner(ctx context.Context, cfg providerModel) (transport.Runner, diag.Diagnostics) {
	var diags diag.Diagnostics
	mode := stringValue(cfg.Mode, "LINUX_MODE", "")
	if mode == "" {
		mode = modeLocal
		if !cfg.SSH.IsNull() || os.Getenv("LINUX_SSH_HOST") != "" {
			mode = modeSSH
		}
	}

	var runner transport.Runner
	switch mode {
	case modeLocal:
		if !cfg.SSH.IsNull() {
			diags.AddAttributeError(path.Root("ssh"), "Unexpected SSH configuration",
				`ssh is only used when mode is "ssh". Remove it or change mode.`)
			return nil, diags
		}
		runner = transport.NewLocal()
	case modeSSH:
		var model sshModel
		if !cfg.SSH.IsNull() {
			diags.Append(cfg.SSH.As(ctx, &model, basetypes.ObjectAsOptions{})...)
			if diags.HasError() {
				return nil, diags
			}
		}
		sshCfg, maxSessions, err := model.config(ctx)
		if err != nil {
			diags.AddAttributeError(path.Root("ssh"), "Invalid SSH configuration", err.Error())
			return nil, diags
		}
		runner = transport.NewSSH(sshCfg, maxSessions)
	default:
		diags.AddAttributeError(path.Root("mode"), "Invalid mode",
			fmt.Sprintf(`mode must be "local" or "ssh", got %q.`, mode))
		return nil, diags
	}

	sudo, err := boolValue(cfg.Sudo, "LINUX_SUDO", false)
	if err != nil {
		diags.AddAttributeError(path.Root("sudo"), "Invalid sudo setting", err.Error())
		return nil, diags
	}
	if sudo {
		runner, err = transport.NewSudo(runner, stringValue(cfg.SudoPassword, "LINUX_SUDO_PASSWORD", ""))
		if err != nil {
			diags.AddAttributeError(path.Root("sudo_password"), "Invalid sudo password", err.Error())
			return nil, diags
		}
	}
	return runner, diags
}

// config converts the ssh attribute, falling back to LINUX_SSH_*
// environment variables, into a transport configuration.
func (m *sshModel) config(ctx context.Context) (transport.SSHConfig, int, error) {
	cfg, err := m.endpoint("LINUX_SSH_")
	if err != nil {
		return cfg, 0, err
	}

	cfg.KnownHostsFile = expandHome(stringValue(m.KnownHostsFile, "LINUX_SSH_KNOWN_HOSTS_FILE", ""))
	if timeout := m.Timeout.ValueString(); timeout != "" {
		// Validated by the schema.
		cfg.Timeout, _ = time.ParseDuration(timeout)
	}
	maxSessions := transport.DefaultMaxSessions
	if !m.MaxSessions.IsNull() {
		maxSessions = int(m.MaxSessions.ValueInt64())
	}

	if !m.Bastion.IsNull() {
		var bastion endpointModel
		if diags := m.Bastion.As(ctx, &bastion, basetypes.ObjectAsOptions{}); diags.HasError() {
			return cfg, 0, fmt.Errorf("reading bastion: %v", diags)
		}
		bastionCfg, err := bastion.endpoint("")
		if err != nil {
			return cfg, 0, fmt.Errorf("bastion: %w", err)
		}
		bastionCfg.KnownHostsFile = cfg.KnownHostsFile
		bastionCfg.Timeout = cfg.Timeout
		cfg.Bastion = &bastionCfg
	}
	return cfg, maxSessions, nil
}

// endpoint converts the endpoint attributes. When envPrefix is set, unset
// attributes default to environment variables.
func (m *endpointModel) endpoint(envPrefix string) (transport.SSHConfig, error) {
	env := func(name string) string {
		if envPrefix == "" {
			return ""
		}
		return envPrefix + name
	}

	cfg := transport.SSHConfig{
		Host:                 stringValue(m.Host, env("HOST"), ""),
		User:                 stringValue(m.User, env("USER"), defaultSSHUser),
		Password:             stringValue(m.Password, env("PASSWORD"), ""),
		PrivateKey:           stringValue(m.PrivateKey, env("PRIVATE_KEY"), ""),
		PrivateKeyPassphrase: stringValue(m.PrivateKeyPassphrase, env("PRIVATE_KEY_PASSPHRASE"), ""),
		HostKey:              stringValue(m.HostKey, env("HOST_KEY"), ""),
	}
	if cfg.Host == "" {
		if envPrefix != "" {
			return cfg, fmt.Errorf("host is required: set it or the %sHOST environment variable", envPrefix)
		}
		return cfg, errors.New("host is required")
	}

	var err error
	if cfg.Port, err = intValue(m.Port, env("PORT"), transport.DefaultSSHPort); err != nil {
		return cfg, fmt.Errorf("port: %w", err)
	}
	if cfg.Agent, err = boolValue(m.Agent, "", true); err != nil {
		return cfg, fmt.Errorf("agent: %w", err)
	}
	if cfg.InsecureIgnoreHostKey, err = boolValue(m.InsecureIgnoreHostKey, env("INSECURE_IGNORE_HOST_KEY"), false); err != nil {
		return cfg, fmt.Errorf("insecure_ignore_host_key: %w", err)
	}

	if keyPath := stringValue(m.PrivateKeyPath, env("PRIVATE_KEY_PATH"), ""); keyPath != "" && cfg.PrivateKey == "" {
		key, err := os.ReadFile(expandHome(keyPath))
		if err != nil {
			return cfg, fmt.Errorf("reading private key: %w", err)
		}
		cfg.PrivateKey = string(key)
	}
	return cfg, nil
}

func (p *linuxProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewCommandResource,
		NewCronJobResource,
		NewDirectoryResource,
		NewFileBlockResource,
		NewFileLineResource,
		NewFileResource,
		NewGroupResource,
		NewHostnameResource,
		NewPackageResource,
		NewPatchResource,
		NewSedResource,
		NewSSHAuthorizedKeyResource,
		NewSymlinkResource,
		NewSysctlResource,
		NewSystemdUnitResource,
		NewUserResource,
	}
}

func (p *linuxProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewCommandDataSource,
		NewFileDataSource,
		NewGroupDataSource,
		NewHostDataSource,
		NewPackageDataSource,
		NewSedDataSource,
		NewStatDataSource,
		NewSystemdUnitDataSource,
		NewUserDataSource,
	}
}

func (p *linuxProvider) Actions(context.Context) []func() action.Action {
	return []func() action.Action{
		NewCommandAction,
		NewSystemctlAction,
	}
}

// stringValue returns v, or the environment variable env when v is null,
// or def.
func stringValue(v types.String, env, def string) string {
	if !v.IsNull() {
		return v.ValueString()
	}
	if env != "" {
		if s, ok := os.LookupEnv(env); ok {
			return s
		}
	}
	return def
}

func boolValue(v types.Bool, env string, def bool) (bool, error) {
	if !v.IsNull() {
		return v.ValueBool(), nil
	}
	if s := os.Getenv(env); env != "" && s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return false, fmt.Errorf("invalid boolean %q in %s", s, env)
		}
		return b, nil
	}
	return def, nil
}

func intValue(v types.Int64, env string, def int) (int, error) {
	if !v.IsNull() {
		return int(v.ValueInt64()), nil
	}
	if s := os.Getenv(env); env != "" && s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("invalid number %q in %s", s, env)
		}
		return n, nil
	}
	return def, nil
}

// expandHome replaces a leading ~ with the home directory.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[1:])
}
