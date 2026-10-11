package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ action.ActionWithConfigure      = (*systemctlAction)(nil)
	_ action.ActionWithValidateConfig = (*systemctlAction)(nil)
)

// systemctlCommands are the commands the linux_systemctl action runs.
var systemctlCommands = []string{
	"start", "stop", "restart", "reload", "reload-or-restart", "try-restart", "try-reload-or-restart",
	"enable", "disable", "mask", "unmask", "daemon-reload",
}

// NewSystemctlAction returns the linux_systemctl action.
func NewSystemctlAction() action.Action {
	return &systemctlAction{}
}

type systemctlAction struct {
	actionBase
}

type systemctlActionModel struct {
	Command types.String `tfsdk:"command"`
	Unit    types.String `tfsdk:"unit"`
}

func (a *systemctlAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_systemctl"
}

func (a *systemctlAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Runs a systemctl command, such as restarting a service after its configuration changed. Requires Terraform 1.14 or later.",
		Attributes: map[string]schema.Attribute{
			"command": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "systemctl command: " + strings.Join(quoteAll(systemctlCommands), ", ") + ".",
				Validators:          []validator.String{stringvalidator.OneOf(systemctlCommands...)},
			},
			"unit": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Unit the command applies to. Required, except for `daemon-reload`.",
				Validators:          []validator.String{unitNameValidator},
			},
		},
	}
}

func (a *systemctlAction) ValidateConfig(ctx context.Context, req action.ValidateConfigRequest, resp *action.ValidateConfigResponse) {
	var m systemctlActionModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.Command.IsUnknown() || m.Unit.IsUnknown() {
		return
	}
	switch daemonReload := m.Command.ValueString() == "daemon-reload"; {
	case daemonReload && !m.Unit.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("unit"), "Unexpected unit", "daemon-reload does not apply to a unit.")
	case !daemonReload && m.Unit.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("unit"), "Missing unit", fmt.Sprintf("%s requires a unit.", m.Command.ValueString()))
	}
}

func (a *systemctlAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var m systemctlActionModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var units []string
	if !m.Unit.IsNull() {
		units = append(units, m.Unit.ValueString())
	}
	if err := a.client.Systemctl(ctx, m.Command.ValueString(), units...); err != nil {
		resp.Diagnostics.AddError("systemctl failed", err.Error())
		return
	}
	resp.SendProgress(action.InvokeProgressEvent{
		Message: strings.TrimSpace("systemctl " + m.Command.ValueString() + " " + m.Unit.ValueString()),
	})
}

func quoteAll(values []string) []string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = "`" + v + "`"
	}
	return quoted
}
