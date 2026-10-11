package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

var (
	_ resource.ResourceWithConfigure   = (*groupResource)(nil)
	_ resource.ResourceWithImportState = (*groupResource)(nil)
)

// NewGroupResource returns the linux_group resource.
func NewGroupResource() resource.Resource {
	return &groupResource{}
}

type groupResource struct {
	resourceBase
}

type groupModel struct {
	ID     types.String `tfsdk:"id"`
	Name   types.String `tfsdk:"name"`
	GID    types.Int64  `tfsdk:"gid"`
	System types.Bool   `tfsdk:"system"`
}

func (r *groupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (r *groupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a group with `groupadd`, `groupmod` and `groupdel`. An existing group is adopted. " +
			"Destroying the resource deletes the group only if this resource created it. Add users to it with `linux_user.groups`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Name of the group.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the group.",
				Validators:          []validator.String{accountNameValidator},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"gid": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Group ID. Defaults to the next free one.",
				Validators:          []validator.Int64{int64validator.AtLeast(0)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"system": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Create a system group. Only used on creation. Defaults to `false`.",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
		},
	}
}

func (r *groupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name, gid := plan.Name.ValueString(), knownInt64(plan.GID)
	group, err := r.client.Group(ctx, name)
	switch {
	case system.IsNotExist(err):
		err = r.client.CreateGroup(ctx, name, gid, plan.System.ValueBool())
		if err == nil {
			markCreated(ctx, resp.Private, &resp.Diagnostics)
		}
	case err == nil && gid != "" && gid != group.GID:
		err = r.client.SetGroupID(ctx, name, gid)
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot create group", err.Error())
		return
	}
	plan.ID = plan.Name
	r.refresh(ctx, &plan, resp.Diagnostics.AddError)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *groupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	group, err := r.client.Group(ctx, state.Name.ValueString())
	if system.IsNotExist(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read group", err.Error())
		return
	}
	state.ID = state.Name
	state.GID = int64Value(group.GID)
	if state.System.IsNull() {
		state.System = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *groupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if gid := knownInt64(plan.GID); gid != "" && !plan.GID.Equal(state.GID) {
		if err := r.client.SetGroupID(ctx, plan.Name.ValueString(), gid); err != nil {
			resp.Diagnostics.AddError("Cannot update group", err.Error())
			return
		}
	}
	r.refresh(ctx, &plan, resp.Diagnostics.AddError)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *groupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !wasCreated(ctx, req.Private, &resp.Diagnostics) {
		return
	}
	name := state.Name.ValueString()
	if _, err := r.client.Group(ctx, name); system.IsNotExist(err) {
		return
	}
	if err := r.client.DeleteGroup(ctx, name); err != nil {
		resp.Diagnostics.AddError("Cannot delete group", err.Error())
	}
}

func (r *groupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
}

func (r *groupResource) refresh(ctx context.Context, m *groupModel, addError func(string, string)) {
	group, err := r.client.Group(ctx, m.Name.ValueString())
	if err != nil {
		addError("Cannot read group", err.Error())
		return
	}
	m.GID = int64Value(group.GID)
}
