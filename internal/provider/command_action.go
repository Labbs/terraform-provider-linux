package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

var _ action.ActionWithConfigure = (*commandAction)(nil)

// NewCommandAction returns the linux_command action.
func NewCommandAction() action.Action {
	return &commandAction{}
}

type commandAction struct {
	actionBase
}

type commandActionModel struct {
	Command          types.String `tfsdk:"command"`
	Environment      types.Map    `tfsdk:"environment"`
	WorkingDirectory types.String `tfsdk:"working_directory"`
}

func (a *commandAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_command"
}

func (a *commandAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Runs a shell script with `/bin/sh`, for instance after a resource changes. Requires Terraform 1.14 or later.",
		Attributes: map[string]schema.Attribute{
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
		},
	}
}

func (a *commandAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var m commandActionModel
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
	res, err := a.client.RunCommand(ctx, cmd)
	if err != nil {
		addCommandError(&resp.Diagnostics, "Command failed", res, err)
		return
	}
	if out := strings.TrimSpace(string(res.Stdout)); out != "" {
		resp.SendProgress(action.InvokeProgressEvent{Message: out})
	}
}
