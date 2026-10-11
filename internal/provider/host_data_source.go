package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSourceWithConfigure = (*hostDataSource)(nil)

// NewHostDataSource returns the linux_host data source.
func NewHostDataSource() datasource.DataSource {
	return &hostDataSource{}
}

type hostDataSource struct {
	dataSourceBase
}

type hostDataModel struct {
	ID                types.String `tfsdk:"id"`
	Hostname          types.String `tfsdk:"hostname"`
	KernelRelease     types.String `tfsdk:"kernel_release"`
	Architecture      types.String `tfsdk:"architecture"`
	OSID              types.String `tfsdk:"os_id"`
	OSIDLike          types.String `tfsdk:"os_id_like"`
	OSName            types.String `tfsdk:"os_name"`
	OSPrettyName      types.String `tfsdk:"os_pretty_name"`
	OSVersionID       types.String `tfsdk:"os_version_id"`
	OSVersionCodename types.String `tfsdk:"os_version_codename"`
	InitSystem        types.String `tfsdk:"init_system"`
	PackageManager    types.String `tfsdk:"package_manager"`
	MachineID         types.String `tfsdk:"machine_id"`
	CPUCount          types.Int64  `tfsdk:"cpu_count"`
	MemoryTotal       types.Int64  `tfsdk:"memory_total"`
}

func (d *hostDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_host"
}

func (d *hostDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	computed := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: description}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Facts about the host, like Ansible facts or Salt grains: operating system, kernel, architecture, resources.",
		Attributes: map[string]schema.Attribute{
			"id":                  computed("Machine ID, or host name when the host has none."),
			"hostname":            computed("Host name."),
			"kernel_release":      computed("Kernel release (`uname -r`)."),
			"architecture":        computed("Machine architecture (`uname -m`), such as `x86_64` or `aarch64`."),
			"os_id":               computed("`ID` from `/etc/os-release`, such as `debian`, `ubuntu`, `rhel` or `alpine`."),
			"os_id_like":          computed("`ID_LIKE` from `/etc/os-release`: related distributions, such as `debian` for Ubuntu."),
			"os_name":             computed("`NAME` from `/etc/os-release`."),
			"os_pretty_name":      computed("`PRETTY_NAME` from `/etc/os-release`."),
			"os_version_id":       computed("`VERSION_ID` from `/etc/os-release`, such as `12` or `24.04`."),
			"os_version_codename": computed("`VERSION_CODENAME` from `/etc/os-release`, such as `bookworm`."),
			"init_system":         computed("`systemd`, or the name of the process 1."),
			"package_manager":     computed("Package manager used by `linux_package`: `apt`, `dnf`, `yum`, `zypper`, `apk`, `pacman`, or empty."),
			"machine_id":          computed("Content of `/etc/machine-id`."),
			"cpu_count":           schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of processors available."},
			"memory_total":        schema.Int64Attribute{Computed: true, MarkdownDescription: "Total memory, in bytes."},
		},
	}
}

func (d *hostDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	facts, err := d.client.Facts(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Cannot gather facts", err.Error())
		return
	}
	id := facts.MachineID
	if id == "" {
		id = facts.Hostname
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, hostDataModel{
		ID:                types.StringValue(id),
		Hostname:          types.StringValue(facts.Hostname),
		KernelRelease:     types.StringValue(facts.KernelRelease),
		Architecture:      types.StringValue(facts.Architecture),
		OSID:              types.StringValue(facts.OSID),
		OSIDLike:          types.StringValue(facts.OSIDLike),
		OSName:            types.StringValue(facts.OSName),
		OSPrettyName:      types.StringValue(facts.OSPrettyName),
		OSVersionID:       types.StringValue(facts.OSVersionID),
		OSVersionCodename: types.StringValue(facts.OSVersionCodename),
		InitSystem:        types.StringValue(facts.InitSystem),
		PackageManager:    types.StringValue(facts.PackageManager),
		MachineID:         types.StringValue(facts.MachineID),
		CPUCount:          types.Int64Value(facts.CPUCount),
		MemoryTotal:       types.Int64Value(facts.MemoryTotal),
	})...)
}
