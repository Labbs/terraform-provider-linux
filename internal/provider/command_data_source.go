package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
	"github.com/labbs/terraform-provider-linux/internal/transport"
)

var _ datasource.DataSourceWithConfigure = (*commandDataSource)(nil)

// NewCommandDataSource returns the linux_command data source.
func NewCommandDataSource() datasource.DataSource {
	return &commandDataSource{}
}

type commandDataSource struct {
	dataSourceBase
}

type commandDataModel struct {
	ID               types.String `tfsdk:"id"`
	Command          types.String `tfsdk:"command"`
	Environment      types.Map    `tfsdk:"environment"`
	WorkingDirectory types.String `tfsdk:"working_directory"`
	AllowFailure     types.Bool   `tfsdk:"allow_failure"`
	Stdout           types.String `tfsdk:"stdout"`
	Stderr           types.String `tfsdk:"stderr"`
	ExitCode         types.Int64  `tfsdk:"exit_code"`
}

func (d *commandDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_command"
}

func (d *commandDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Runs a shell script with `/bin/sh` and exposes its output. The script runs on every plan: it should not change anything.",
		Attributes: map[string]schema.Attribute{
			"id":      schema.StringAttribute{Computed: true, MarkdownDescription: "Digest of the command."},
			"command": schema.StringAttribute{Required: true, MarkdownDescription: "Script to run."},
			"environment": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Environment variables exported to the script.",
				Validators:          []validator.Map{mapvalidator.KeysAre(envNameValidator)},
			},
			"working_directory": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Directory the script runs in.",
				Validators:          []validator.String{pathValidator{}},
			},
			"allow_failure": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Report a non-zero exit status in `exit_code` instead of failing. Defaults to `false`.",
			},
			"stdout":    schema.StringAttribute{Computed: true, MarkdownDescription: "Standard output."},
			"stderr":    schema.StringAttribute{Computed: true, MarkdownDescription: "Standard error."},
			"exit_code": schema.Int64Attribute{Computed: true, MarkdownDescription: "Exit status."},
		},
	}
}

func (d *commandDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m commandDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cmd := system.Command{
		Script: m.Command.ValueString(),
		Env:    stringMap(ctx, m.Environment, &resp.Diagnostics),
		Dir:    m.WorkingDirectory.ValueString(),
	}
	if resp.Diagnostics.HasError() {
		return
	}
	res, err := d.client.RunCommand(ctx, cmd)
	if _, ok := transport.ExitCode(err); ok && m.AllowFailure.ValueBool() {
		err = nil
	}
	if err != nil {
		addCommandError(&resp.Diagnostics, "Command failed", res, err)
		return
	}
	m.ID = types.StringValue(sha256Hex([]byte(cmd.Script))[:16])
	m.Stdout = types.StringValue(string(res.Stdout))
	m.Stderr = types.StringValue(string(res.Stderr))
	m.ExitCode = types.Int64Value(int64(res.ExitCode))
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
