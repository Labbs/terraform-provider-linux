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

	"github.com/labbs/terraform-provider-linux/internal/system"
)

var (
	_ resource.ResourceWithConfigure   = (*directoryResource)(nil)
	_ resource.ResourceWithImportState = (*directoryResource)(nil)
)

// NewDirectoryResource returns the linux_directory resource.
func NewDirectoryResource() resource.Resource {
	return &directoryResource{}
}

type directoryResource struct {
	resourceBase
}

type directoryModel struct {
	ID            types.String `tfsdk:"id"`
	Path          types.String `tfsdk:"path"`
	Mode          types.String `tfsdk:"mode"`
	Owner         types.String `tfsdk:"owner"`
	Group         types.String `tfsdk:"group"`
	CreateParents types.Bool   `tfsdk:"create_parents"`
	ForceDestroy  types.Bool   `tfsdk:"force_destroy"`
}

func (m *directoryModel) attributes() system.Attributes {
	return system.Attributes{
		Mode:  knownString(m.Mode),
		Owner: knownString(m.Owner),
		Group: knownString(m.Group),
	}
}

func (r *directoryResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_directory"
}

func (r *directoryResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a directory, its permissions and ownership. An existing directory is adopted as is.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Path of the directory.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Absolute path of the directory.",
				Validators:          []validator.String{pathValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"mode": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("0755"),
				MarkdownDescription: "Permissions, in octal. Defaults to `0755`.",
				Validators:          []validator.String{modeValidator},
			},
			"owner": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Owner, as a user name or UID. Defaults to the user running the commands.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"group": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Group, as a group name or GID. Defaults to the primary group of the user running the commands.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"create_parents": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Create missing parent directories, like `mkdir -p`. They are not removed with the directory. Defaults to `true`.",
			},
			"force_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "On destroy, delete the directory with everything it contains. Otherwise destroying fails when the directory is not empty. Defaults to `false`.",
			},
		},
	}
}

func (r *directoryResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan directoryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Mkdir(ctx, plan.Path.ValueString(), plan.attributes(), plan.CreateParents.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Cannot create directory", err.Error())
		return
	}
	plan.ID = plan.Path
	r.refreshAttributes(ctx, &plan, resp.Diagnostics.AddError)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *directoryResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state directoryModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := state.Path.ValueString()
	info, err := r.client.Stat(ctx, p)
	if system.IsNotExist(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read directory", err.Error())
		return
	}
	if info.Type != system.TypeDirectory {
		resp.Diagnostics.AddWarning("Not a directory", fmt.Sprintf("%s is a %s, not a directory.", p, info.Type))
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = state.Path
	state.Mode = modeValue(state.Mode, info.Mode)
	state.Owner = ownerValue(state.Owner, info.Owner, info.UID)
	state.Group = ownerValue(state.Group, info.Group, info.GID)
	if state.CreateParents.IsNull() {
		state.CreateParents = types.BoolValue(true)
	}
	if state.ForceDestroy.IsNull() {
		state.ForceDestroy = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *directoryResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state directoryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.attributes() != state.attributes() {
		if err := r.client.SetAttributes(ctx, plan.Path.ValueString(), plan.attributes()); err != nil {
			resp.Diagnostics.AddError("Cannot update directory", err.Error())
			return
		}
	}
	r.refreshAttributes(ctx, &plan, resp.Diagnostics.AddError)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *directoryResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state directoryModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.RemoveDir(ctx, state.Path.ValueString(), state.ForceDestroy.ValueBool()); err != nil {
		detail := err.Error()
		if !state.ForceDestroy.ValueBool() {
			detail += "\n\nSet force_destroy = true to delete a directory that is not empty."
		}
		resp.Diagnostics.AddError("Cannot delete directory", detail)
	}
}

func (r *directoryResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if err := system.CheckPath(req.ID); err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("The ID must be the path of the directory: %q %s.", req.ID, err))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("path"), req.ID)...)
}

// refreshAttributes stores the actual mode, owner and group of the directory.
func (r *directoryResource) refreshAttributes(ctx context.Context, m *directoryModel, addError func(string, string)) {
	info, err := r.client.Stat(ctx, m.Path.ValueString())
	if err != nil {
		addError("Cannot read directory", err.Error())
		return
	}
	m.Mode = modeValue(m.Mode, info.Mode)
	m.Owner = ownerValue(m.Owner, info.Owner, info.UID)
	m.Group = ownerValue(m.Group, info.Group, info.GID)
}
