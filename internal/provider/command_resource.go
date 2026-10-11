package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
	"github.com/labbs/terraform-provider-linux/internal/transport"
)

var (
	_ resource.ResourceWithConfigure  = (*commandResource)(nil)
	_ resource.ResourceWithModifyPlan = (*commandResource)(nil)
)

// NewCommandResource returns the linux_command resource.
func NewCommandResource() resource.Resource {
	return &commandResource{}
}

type commandResource struct {
	resourceBase
}

type commandModel struct {
	ID               types.String `tfsdk:"id"`
	Create           types.String `tfsdk:"create"`
	Read             types.String `tfsdk:"read"`
	Update           types.String `tfsdk:"update"`
	Delete           types.String `tfsdk:"delete"`
	Environment      types.Map    `tfsdk:"environment"`
	WorkingDirectory types.String `tfsdk:"working_directory"`
	Triggers         types.Map    `tfsdk:"triggers"`
	Output           types.String `tfsdk:"output"`
}

func (m *commandModel) command(ctx context.Context, script types.String, diags *diag.Diagnostics) system.Command {
	return system.Command{
		Script: script.ValueString(),
		Env:    stringMap(ctx, m.Environment, diags),
		Dir:    m.WorkingDirectory.ValueString(),
	}
}

// changed reports whether attributes that run the update script changed.
func (m *commandModel) changed(prior *commandModel) bool {
	return !m.Create.Equal(prior.Create) ||
		!m.Environment.Equal(prior.Environment) ||
		!m.WorkingDirectory.Equal(prior.WorkingDirectory) ||
		!m.Triggers.Equal(prior.Triggers)
}

func (r *commandResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_command"
}

func (r *commandResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	const replaceDescription = "Changing it runs `update` when set, and replaces the resource otherwise."
	resp.Schema = schema.Schema{
		MarkdownDescription: "Runs shell scripts with `/bin/sh` when the resource is created, updated or destroyed, " +
			"for anything the other resources do not cover. Scripts are sent on standard input, never on a command line, " +
			"and get an empty standard input themselves.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Random identifier.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"create": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Script run when the resource is created. " + replaceDescription,
				PlanModifiers:       []planmodifier.String{replaceUnlessUpdateString()},
			},
			"read": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Script run on refresh, whose standard output becomes `output`. " +
					"When it exits with a non-zero status, the resource is considered gone and Terraform plans to create it again.",
			},
			"update": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Script run when `create`, `environment`, `working_directory` or `triggers` change. Without it, such changes replace the resource.",
			},
			"delete": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Script run when the resource is destroyed, with the environment and working directory in the state.",
			},
			"environment": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Environment variables exported to the scripts. " + replaceDescription,
				Validators:          []validator.Map{mapvalidator.KeysAre(envNameValidator)},
				PlanModifiers:       []planmodifier.Map{replaceUnlessUpdateMap()},
			},
			"working_directory": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Directory the scripts run in. " + replaceDescription,
				Validators:          []validator.String{pathValidator{}},
				PlanModifiers:       []planmodifier.String{replaceUnlessUpdateString()},
			},
			"triggers": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Arbitrary values that run `update`, or replace the resource, when they change.",
				PlanModifiers:       []planmodifier.Map{replaceUnlessUpdateMap()},
			},
			"output": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Standard output of `read` when set, of the last `create` or `update` otherwise.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func updateIsNull(ctx context.Context, plan tfsdk.Plan) (bool, diag.Diagnostics) {
	var update types.String
	diags := plan.GetAttribute(ctx, path.Root("update"), &update)
	return update.IsNull(), diags
}

func replaceUnlessUpdateString() planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(
		func(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			var diags diag.Diagnostics
			resp.RequiresReplace, diags = updateIsNull(ctx, req.Plan)
			resp.Diagnostics.Append(diags...)
		},
		"Replaces the resource unless update is set.", "Replaces the resource unless `update` is set.",
	)
}

func replaceUnlessUpdateMap() planmodifier.Map {
	return mapplanmodifier.RequiresReplaceIf(
		func(ctx context.Context, req planmodifier.MapRequest, resp *mapplanmodifier.RequiresReplaceIfFuncResponse) {
			var diags diag.Diagnostics
			resp.RequiresReplace, diags = updateIsNull(ctx, req.Plan)
			resp.Diagnostics.Append(diags...)
		},
		"Replaces the resource unless update is set.", "Replaces the resource unless `update` is set.",
	)
}

func (r *commandResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var plan, state commandModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.Update.IsNull() && plan.changed(&state) {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("output"), types.StringUnknown())...)
	}
}

func (r *commandResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan commandModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	output, ok := r.run(ctx, &plan, plan.Create, "create", &resp.Diagnostics)
	if !ok {
		return
	}
	plan.ID = types.StringValue(randomID())
	plan.Output = types.StringValue(output)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *commandResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state commandModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || state.Read.IsNull() {
		return
	}
	cmd := state.command(ctx, state.Read, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	res, err := r.client.RunCommand(ctx, cmd)
	if _, ok := transport.ExitCode(err); ok {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addCommandError(&resp.Diagnostics, "read script failed", res, err)
		return
	}
	state.Output = types.StringValue(string(res.Stdout))
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *commandResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state commandModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.Update.IsNull() && plan.changed(&state) {
		output, ok := r.run(ctx, &plan, plan.Update, "update", &resp.Diagnostics)
		if !ok {
			return
		}
		plan.Output = types.StringValue(output)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *commandResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state commandModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || state.Delete.IsNull() {
		return
	}
	cmd := state.command(ctx, state.Delete, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if res, err := r.client.RunCommand(ctx, cmd); err != nil {
		addCommandError(&resp.Diagnostics, "delete script failed", res, err)
	}
}

// run runs script, then read if set, and returns the output to store.
func (r *commandResource) run(ctx context.Context, m *commandModel, script types.String, name string, diags *diag.Diagnostics) (string, bool) {
	cmd := m.command(ctx, script, diags)
	if diags.HasError() {
		return "", false
	}
	res, err := r.client.RunCommand(ctx, cmd)
	if err != nil {
		addCommandError(diags, name+" script failed", res, err)
		return "", false
	}
	if m.Read.IsNull() {
		return string(res.Stdout), true
	}
	cmd.Script = m.Read.ValueString()
	res, err = r.client.RunCommand(ctx, cmd)
	if err != nil {
		addCommandError(diags, "read script failed after "+name, res, err)
		return "", false
	}
	return string(res.Stdout), true
}
