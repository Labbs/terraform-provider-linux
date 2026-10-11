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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

var (
	_ resource.ResourceWithConfigure   = (*symlinkResource)(nil)
	_ resource.ResourceWithImportState = (*symlinkResource)(nil)
)

// NewSymlinkResource returns the linux_symlink resource.
func NewSymlinkResource() resource.Resource {
	return &symlinkResource{}
}

type symlinkResource struct {
	resourceBase
}

type symlinkModel struct {
	ID            types.String `tfsdk:"id"`
	Path          types.String `tfsdk:"path"`
	Target        types.String `tfsdk:"target"`
	CreateParents types.Bool   `tfsdk:"create_parents"`
}

func (r *symlinkResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_symlink"
}

func (r *symlinkResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a symbolic link. A file or link already at `path` is replaced, a directory is not.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Path of the link.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Absolute path of the link.",
				Validators:          []validator.String{pathValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"target": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Path the link points to, absolute or relative to the directory of the link. It does not need to exist.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"create_parents": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Create missing parent directories. Defaults to `true`.",
			},
		},
	}
}

func (r *symlinkResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan symlinkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Symlink(ctx, plan.Target.ValueString(), plan.Path.ValueString(), plan.CreateParents.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Cannot create link", err.Error())
		return
	}
	plan.ID = plan.Path
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *symlinkResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state symlinkModel
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
		resp.Diagnostics.AddError("Cannot read link", err.Error())
		return
	}
	if info.Type != system.TypeSymlink {
		resp.Diagnostics.AddWarning("Not a symbolic link", fmt.Sprintf("%s is a %s, it will be replaced by a link.", p, info.Type))
		resp.State.RemoveResource(ctx)
		return
	}
	target, err := r.client.Readlink(ctx, p)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read link", err.Error())
		return
	}
	state.ID = state.Path
	state.Target = types.StringValue(target)
	if state.CreateParents.IsNull() {
		state.CreateParents = types.BoolValue(true)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *symlinkResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan symlinkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Symlink(ctx, plan.Target.ValueString(), plan.Path.ValueString(), plan.CreateParents.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Cannot update link", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *symlinkResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state symlinkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := state.Path.ValueString()
	// Only delete what is still a link.
	info, err := r.client.Stat(ctx, p)
	if system.IsNotExist(err) || (err == nil && info.Type != system.TypeSymlink) {
		return
	}
	if err == nil {
		err = r.client.Remove(ctx, p)
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot delete link", err.Error())
	}
}

func (r *symlinkResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if err := system.CheckPath(req.ID); err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("The ID must be the path of the link: %q %s.", req.ID, err))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("path"), req.ID)...)
}
