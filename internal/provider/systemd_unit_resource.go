package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.ResourceWithConfigure      = (*systemdUnitResource)(nil)
	_ resource.ResourceWithImportState    = (*systemdUnitResource)(nil)
	_ resource.ResourceWithValidateConfig = (*systemdUnitResource)(nil)
)

// triggerActions are the systemctl commands a change of triggers can run.
var triggerActions = []string{"restart", "reload", "reload-or-restart", "try-restart", "try-reload-or-restart"}

// NewSystemdUnitResource returns the linux_systemd_unit resource.
func NewSystemdUnitResource() resource.Resource {
	return &systemdUnitResource{}
}

type systemdUnitResource struct {
	resourceBase
}

type systemdUnitModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	Enabled         types.Bool   `tfsdk:"enabled"`
	Active          types.Bool   `tfsdk:"active"`
	Masked          types.Bool   `tfsdk:"masked"`
	DaemonReload    types.Bool   `tfsdk:"daemon_reload"`
	Triggers        types.Map    `tfsdk:"triggers"`
	TriggerAction   types.String `tfsdk:"trigger_action"`
	RevertOnDestroy types.Bool   `tfsdk:"revert_on_destroy"`
}

func (r *systemdUnitResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_systemd_unit"
}

func (r *systemdUnitResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the state of a systemd unit: whether it is enabled, running or masked. " +
			"Attributes left unset are not managed. Use `triggers` to restart the unit when its configuration changes.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Name of the unit.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the unit, such as `nginx.service`. Without a suffix, systemd assumes `.service`.",
				Validators:          []validator.String{unitNameValidator},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"enabled": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Whether the unit starts at boot (`systemctl enable`/`disable`).",
			},
			"active": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Whether the unit is running (`systemctl start`/`stop`).",
			},
			"masked": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Whether the unit is masked, which prevents it from starting at all (`systemctl mask`/`unmask`).",
			},
			"daemon_reload": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Run `systemctl daemon-reload` before changing the unit, to pick up changed unit files. Defaults to `false`.",
			},
			"triggers": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Arbitrary values that run `trigger_action` on the unit when they change, unless the unit is stopped. " +
					"For instance `{ config = linux_file.config.sha256 }`. When the resource is created with triggers, the action runs if the unit was already running.",
			},
			"trigger_action": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("restart"),
				MarkdownDescription: "systemctl command run when `triggers` change: `restart`, `reload`, `reload-or-restart`, `try-restart` or `try-reload-or-restart`. Defaults to `restart`.",
				Validators:          []validator.String{stringvalidator.OneOf(triggerActions...)},
			},
			"revert_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "On destroy, stop, disable and unmask the unit as far as this resource started, enabled or masked it. Otherwise destroying leaves the unit as it is. Defaults to `false`.",
			},
		},
	}
}

func (r *systemdUnitResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg systemdUnitModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || !cfg.Masked.ValueBool() {
		return
	}
	if cfg.Enabled.ValueBool() {
		resp.Diagnostics.AddAttributeError(path.Root("enabled"), "Conflicting configuration", "A masked unit cannot be enabled.")
	}
	if cfg.Active.ValueBool() {
		resp.Diagnostics.AddAttributeError(path.Root("active"), "Conflicting configuration", "A masked unit cannot be active.")
	}
}

func (r *systemdUnitResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan systemdUnitModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan, nil, resp.Diagnostics.AddWarning); err != nil {
		resp.Diagnostics.AddError("Cannot configure unit", err.Error())
		return
	}
	plan.ID = plan.Name
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *systemdUnitResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state systemdUnitModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	status, err := r.client.UnitStatus(ctx, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read unit", err.Error())
		return
	}
	if !status.Exists() && !status.Masked() {
		resp.State.RemoveResource(ctx)
		return
	}
	// Only the attributes the configuration manages are refreshed.
	if !state.Enabled.IsNull() && status.CanBeEnabled() {
		state.Enabled = types.BoolValue(status.Enabled())
	}
	if !state.Active.IsNull() {
		state.Active = types.BoolValue(status.Active())
	}
	if !state.Masked.IsNull() {
		state.Masked = types.BoolValue(status.Masked())
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *systemdUnitResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state systemdUnitModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan, &state, resp.Diagnostics.AddWarning); err != nil {
		resp.Diagnostics.AddError("Cannot configure unit", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *systemdUnitResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state systemdUnitModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !state.RevertOnDestroy.ValueBool() {
		return
	}
	name := state.Name.ValueString()
	status, err := r.client.UnitStatus(ctx, name)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read unit", err.Error())
		return
	}

	var commands []string
	if state.Masked.ValueBool() && status.Masked() {
		commands = append(commands, "unmask")
	}
	if state.Active.ValueBool() && status.Active() {
		commands = append(commands, "stop")
	}
	if state.Enabled.ValueBool() && status.Enabled() {
		commands = append(commands, "disable")
	}
	for _, command := range commands {
		if err := r.client.Systemctl(ctx, command, name); err != nil {
			resp.Diagnostics.AddError("Cannot revert unit", err.Error())
			return
		}
	}
}

func (r *systemdUnitResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	status, err := r.client.UnitStatus(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read unit", err.Error())
		return
	}
	if !status.Exists() && !status.Masked() {
		resp.Diagnostics.AddError("Unit not found", fmt.Sprintf("systemd does not know the unit %q.", req.ID))
		return
	}
	state := systemdUnitModel{
		ID:              types.StringValue(req.ID),
		Name:            types.StringValue(req.ID),
		Active:          types.BoolValue(status.Active()),
		Masked:          types.BoolValue(status.Masked()),
		DaemonReload:    types.BoolValue(false),
		Triggers:        types.MapNull(types.StringType),
		TriggerAction:   types.StringValue("restart"),
		RevertOnDestroy: types.BoolValue(false),
		Enabled:         types.BoolNull(),
	}
	if status.CanBeEnabled() {
		state.Enabled = types.BoolValue(status.Enabled())
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// apply brings the unit to the planned state. prior is nil on creation.
func (r *systemdUnitResource) apply(ctx context.Context, plan, prior *systemdUnitModel, addWarning func(string, string)) error {
	name := plan.Name.ValueString()
	if plan.DaemonReload.ValueBool() {
		if err := r.client.Systemctl(ctx, "daemon-reload"); err != nil {
			return err
		}
	}
	status, err := r.client.UnitStatus(ctx, name)
	if err != nil {
		return err
	}

	if !plan.Masked.IsNull() && plan.Masked.ValueBool() != status.Masked() {
		if plan.Masked.ValueBool() {
			if status.Active() {
				if err := r.client.Systemctl(ctx, "stop", name); err != nil {
					return err
				}
			}
			return r.client.Systemctl(ctx, "mask", name)
		}
		if err := r.client.Systemctl(ctx, "unmask", name); err != nil {
			return err
		}
		if status, err = r.client.UnitStatus(ctx, name); err != nil {
			return err
		}
	}
	if plan.Masked.ValueBool() {
		return nil
	}
	if !status.Exists() {
		return fmt.Errorf("systemd does not know the unit %s; if its unit file was just written, set daemon_reload = true", name)
	}

	if !plan.Enabled.IsNull() && plan.Enabled.ValueBool() != status.Enabled() {
		if status.CanBeEnabled() {
			command := "disable"
			if plan.Enabled.ValueBool() {
				command = "enable"
			}
			if err := r.client.Systemctl(ctx, command, name); err != nil {
				return err
			}
		} else {
			addWarning("Unit cannot be enabled or disabled",
				fmt.Sprintf("%s is %s: systemctl enable and disable have no effect on it.", name, status.UnitFileState))
		}
	}

	started := false
	if !plan.Active.IsNull() && plan.Active.ValueBool() != status.Active() {
		command := "stop"
		if plan.Active.ValueBool() {
			command, started = "start", true
		}
		if err := r.client.Systemctl(ctx, command, name); err != nil {
			return err
		}
	}

	// A unit that was just started already runs with the new configuration.
	running := plan.Active.ValueBool() || (plan.Active.IsNull() && status.Active())
	triggered := len(plan.Triggers.Elements()) > 0 && (prior == nil || !plan.Triggers.Equal(prior.Triggers))
	if triggered && running && !started {
		return r.client.Systemctl(ctx, plan.TriggerAction.ValueString(), name)
	}
	return nil
}
