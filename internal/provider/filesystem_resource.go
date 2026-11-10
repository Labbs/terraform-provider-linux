package provider

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

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
	_ resource.ResourceWithConfigure      = (*filesystemResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*filesystemResource)(nil)
	_ resource.ResourceWithValidateConfig = (*filesystemResource)(nil)
)

// NewFilesystemResource returns the linux_filesystem resource.
func NewFilesystemResource() resource.Resource {
	return &filesystemResource{}
}

type filesystemResource struct {
	resourceBase
}

type filesystemModel struct {
	ID          types.String `tfsdk:"id"`
	Device      types.String `tfsdk:"device"`
	Type        types.String `tfsdk:"type"`
	Label       types.String `tfsdk:"label"`
	Overwrite   types.Bool   `tfsdk:"overwrite"`
	MkfsOptions types.List   `tfsdk:"mkfs_options"`
	Grow        types.Bool   `tfsdk:"grow"`
	Triggers    types.Map    `tfsdk:"triggers"`
	UUID        types.String `tfsdk:"uuid"`
	DeviceSize  types.Int64  `tfsdk:"device_size"`
}

func (r *filesystemResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_filesystem"
}

func (r *filesystemResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Makes sure a device holds a file system, or a swap area, formatting it only when it is empty.\n\n" +
			"A device that already holds a file system of the requested `type` is used as it is, with its data, like cloud-init does: only its label is changed when needed. " +
			"A device that holds anything else (another file system, a partition table, an LVM or RAID member...) is an error, unless `overwrite` is set. " +
			"Destroying the resource leaves the file system and its data in place.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Path of the device.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"device": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Device to format, such as `linux_partition.data.path`. The resource waits up to 2 minutes for it to appear.",
				Validators:          []validator.String{pathValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"type": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "File system type: " + strings.Join(quoteAll(system.FilesystemTypes), ", ") + ". The `mkfs` tool must be installed (`xfsprogs`, `btrfs-progs`, `dosfstools`).",
				Validators:          []validator.String{stringvalidator.OneOf(system.FilesystemTypes...)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"label": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "File system label, available as `/dev/disk/by-label/<label>` and `LABEL=<label>` in `linux_mount`. Changing it relabels the file system without formatting it. Defaults to the existing label.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 16)},
			},
			"overwrite": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Erase whatever the device holds and format it, even a file system of the requested type. **This destroys its data.** Only used when the resource is created. Defaults to `false`.",
			},
			"mkfs_options": schema.ListAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Extra arguments for `mkfs`, such as `[\"-m\", \"1\"]` for ext4. Only used when formatting.",
			},
			"grow": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Grow the file system to the size of the device when the device grows, for instance after enlarging a cloud volume and its partition. Supported for ext2/3/4, and for xfs and btrfs while they are mounted. Defaults to `false`.",
			},
			"triggers": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Arbitrary values that grow the file system again, when `grow` is set, when they change. Use `{ size = linux_partition.data.size_bytes }` to grow it in the same apply as its partition.",
			},
			"uuid": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "UUID of the file system, to mount it with `UUID=<uuid>`, which does not depend on device names.",
			},
			"device_size": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Size of the device, in bytes, when the file system was created or last grown.",
			},
		},
	}
}

func (r *filesystemResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m filesystemModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.Type.IsUnknown() || m.Grow.IsUnknown() {
		return
	}
	if m.Grow.ValueBool() && !slices.Contains(system.GrowableFilesystemTypes, m.Type.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("grow"), "Cannot grow this file system",
			fmt.Sprintf("%s file systems cannot be grown: grow is supported for %s.", m.Type.ValueString(), strings.Join(system.GrowableFilesystemTypes, ", ")))
	}
}

// ModifyPlan keeps the identity of a file system that stays in place, and
// plans growing it when its device grew.
func (r *filesystemResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var plan, state, config filesystemModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.Device.Equal(state.Device) || !plan.Type.Equal(state.Type) {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("uuid"), types.StringUnknown())...)
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("device_size"), types.Int64Unknown())...)
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("uuid"), state.UUID)...)
	if config.Label.IsNull() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("label"), state.Label)...)
	}

	size := state.DeviceSize
	if plan.Grow.ValueBool() {
		if !plan.Triggers.Equal(state.Triggers) {
			size = types.Int64Unknown()
		} else if r.client != nil {
			// Errors are reported when the resource is applied.
			if info, err := r.client.DeviceInfoNow(ctx, plan.Device.ValueString()); err == nil && info.Size != state.DeviceSize.ValueInt64() {
				size = types.Int64Unknown()
			}
		}
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("device_size"), size)...)
}

func (r *filesystemResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan filesystemModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var options []string
	if !plan.MkfsOptions.IsNull() && !plan.MkfsOptions.IsUnknown() {
		resp.Diagnostics.Append(plan.MkfsOptions.ElementsAs(ctx, &options, false)...)
	}
	dev, fsType := plan.Device.ValueString(), plan.Type.ValueString()
	result, err := r.client.EnsureFilesystem(ctx, dev, fsType, knownString(plan.Label), options, plan.Overwrite.ValueBool())
	if errors.Is(err, system.ErrFormatted) {
		resp.Diagnostics.AddAttributeError(path.Root("device"), "Device already formatted", err.Error())
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot create file system", err.Error())
		return
	}
	if !result.Formatted && plan.Grow.ValueBool() {
		// An existing file system may be smaller than its device. XFS and
		// Btrfs can only grow once mounted: the next apply does it.
		if err := r.client.GrowFilesystem(ctx, dev, fsType); err != nil && fsType != "xfs" && fsType != "btrfs" {
			resp.Diagnostics.AddError("Cannot grow file system", err.Error())
			return
		}
		info, err := r.client.DeviceInfo(ctx, dev)
		if err != nil {
			resp.Diagnostics.AddError("Cannot read file system", err.Error())
			return
		}
		result.Size = info.Size
	}
	plan.ID = plan.Device
	plan.UUID = types.StringValue(result.UUID)
	plan.Label = types.StringValue(result.Label)
	plan.DeviceSize = types.Int64Value(result.Size)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *filesystemResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state filesystemModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	dev := state.Device.ValueString()
	info, err := r.client.DeviceInfoNow(ctx, dev)
	if system.IsNotExist(err) {
		resp.Diagnostics.AddWarning("Device not found", fmt.Sprintf("%s does not exist: the file system will be created when it is back, unless it holds data.", dev))
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read file system", err.Error())
		return
	}
	switch found := info.Probe["TYPE"]; found {
	case state.Type.ValueString():
	case "":
		resp.State.RemoveResource(ctx)
		return
	default:
		// Creating it again fails, unless overwrite is set.
		resp.Diagnostics.AddWarning("File system replaced outside of Terraform", fmt.Sprintf("%s now holds a %s file system.", dev, found))
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = state.Device
	state.UUID = types.StringValue(info.Probe["UUID"])
	state.Label = types.StringValue(info.Probe["LABEL"])
	if state.DeviceSize.IsNull() {
		state.DeviceSize = types.Int64Value(info.Size)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *filesystemResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state filesystemModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	dev, fsType := plan.Device.ValueString(), plan.Type.ValueString()
	if !plan.Label.IsUnknown() && !plan.Label.Equal(state.Label) {
		if err := r.client.SetFilesystemLabel(ctx, dev, fsType, plan.Label.ValueString()); err != nil {
			resp.Diagnostics.AddError("Cannot change label", err.Error())
			return
		}
	}
	if plan.Grow.ValueBool() && plan.DeviceSize.IsUnknown() {
		if err := r.client.GrowFilesystem(ctx, dev, fsType); err != nil {
			resp.Diagnostics.AddError("Cannot grow file system", err.Error())
			return
		}
	}
	info, err := r.client.DeviceInfo(ctx, dev)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read file system", err.Error())
		return
	}
	plan.UUID = types.StringValue(info.Probe["UUID"])
	plan.Label = types.StringValue(info.Probe["LABEL"])
	if plan.DeviceSize.IsUnknown() {
		plan.DeviceSize = types.Int64Value(info.Size)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete leaves the file system in place: wiping it would lose its data.
func (r *filesystemResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}
