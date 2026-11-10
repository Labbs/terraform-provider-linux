package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

var (
	_ resource.ResourceWithConfigure  = (*partitionResource)(nil)
	_ resource.ResourceWithModifyPlan = (*partitionResource)(nil)
)

var guidPattern = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

// NewPartitionResource returns the linux_partition resource.
func NewPartitionResource() resource.Resource {
	return &partitionResource{}
}

type partitionResource struct {
	resourceBase
}

type partitionModel struct {
	ID        types.String `tfsdk:"id"`
	Device    types.String `tfsdk:"device"`
	Number    types.Int64  `tfsdk:"number"`
	Size      types.String `tfsdk:"size"`
	Type      types.String `tfsdk:"type"`
	Name      types.String `tfsdk:"name"`
	Path      types.String `tfsdk:"path"`
	UUID      types.String `tfsdk:"uuid"`
	SizeBytes types.Int64  `tfsdk:"size_bytes"`
}

// wantSize returns the requested size in bytes, 0 for all the free space.
func (m *partitionModel) wantSize() int64 {
	if m.Size.IsNull() || m.Size.IsUnknown() {
		return 0
	}
	size, _ := system.ParseSize(m.Size.ValueString()) // Validated by the schema.
	return size
}

func (r *partitionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_partition"
}

func partitionTypeNames() []string {
	names := make([]string, 0, len(system.PartitionTypes))
	for name := range system.PartitionTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *partitionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Makes sure a partition exists on a disk, with `sfdisk`. Only GPT partition tables are supported.\n\n" +
			"Existing data is never overwritten: a partition that already exists is kept as it is, apart from its type, its name and growing it; " +
			"a disk without partition table only gets one when it holds nothing, as `blkid` sees it. A disk that holds a file system directly is refused. " +
			"Partitions are never shrunk, moved or deleted: destroying the resource leaves the partition on the disk.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Disk and partition number, such as `/dev/sdb:1`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"device": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Disk to partition, such as `/dev/sdb` or a stable path under `/dev/disk/by-id/`. The resource waits up to 2 minutes for it to appear, for volumes being attached.",
				Validators:          []validator.String{pathValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"number": schema.Int64Attribute{
				Required:            true,
				MarkdownDescription: "Partition number, from 1.",
				Validators:          []validator.Int64{int64validator.Between(1, 128)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"size": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Size, such as `512M`, `20G` or `1T` (powers of 1024). Defaults to all the free space after the start of the partition: " +
					"the partition then grows when the disk is enlarged and the space after it is free. A larger size grows the partition; a smaller one is an error.",
				Validators: []validator.String{sizeValidator{}},
			},
			"type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("linux"),
				MarkdownDescription: "Partition type: " + strings.Join(quoteAll(partitionTypeNames()), ", ") + ", or a GPT type GUID. Defaults to `linux`.",
				Validators: []validator.String{stringvalidator.Any(
					stringvalidator.OneOf(partitionTypeNames()...),
					stringvalidator.RegexMatches(guidPattern, "must be a GUID"),
				)},
			},
			"name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "GPT partition name, available as `/dev/disk/by-partlabel/<name>`.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 36)},
			},
			"path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Device path of the partition, such as `/dev/sdb1` or `/dev/nvme1n1p1`.",
			},
			"uuid": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique partition GUID (`PARTUUID`).",
			},
			"size_bytes": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Size of the partition, in bytes. Reference it in the `triggers` of `linux_filesystem` to grow the file system with the partition.",
			},
		},
	}
}

// ModifyPlan keeps the computed attributes of a partition that stays in
// place, and plans growing it.
func (r *partitionResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var plan, state partitionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.Device.Equal(state.Device) || !plan.Number.Equal(state.Number) {
		for _, attr := range []string{"path", "uuid"} {
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(attr), types.StringUnknown())...)
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("size_bytes"), types.Int64Unknown())...)
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("path"), state.Path)...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("uuid"), state.UUID)...)

	sizeBytes := state.SizeBytes
	if plan.Size.IsUnknown() {
		sizeBytes = types.Int64Unknown()
	} else if want := plan.wantSize(); want > 0 {
		switch current := state.SizeBytes.ValueInt64(); {
		case want < current:
			resp.Diagnostics.AddAttributeError(path.Root("size"), "Partitions cannot shrink",
				fmt.Sprintf("Partition %d of %s is %d bytes, more than %s: shrinking partitions is not supported.",
					plan.Number.ValueInt64(), plan.Device.ValueString(), current, plan.Size.ValueString()))
		case want > current:
			sizeBytes = types.Int64Unknown()
		}
	} else if r.client != nil {
		// Grow into free space, for instance after the disk was enlarged.
		// Errors are reported when the resource is applied.
		if disk, err := r.client.DeviceInfoNow(ctx, plan.Device.ValueString()); err == nil && disk.Table != nil {
			if p := disk.Table.Partition(int(plan.Number.ValueInt64())); p != nil && system.PartitionGrowable(disk, p) {
				sizeBytes = types.Int64Unknown()
			}
		}
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("size_bytes"), sizeBytes)...)
}

func (r *partitionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan partitionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	dev := plan.Device.ValueString()
	number := int(plan.Number.ValueInt64())
	disk, created, err := r.client.EnsurePartition(ctx, dev, system.PartitionSpec{
		Number: number,
		Size:   plan.wantSize(),
		Type:   plan.Type.ValueString(),
		Name:   plan.Name.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create partition", err.Error())
		return
	}
	if !created {
		// The partition already existed: adopt it, without touching its
		// content.
		p := disk.Table.Partition(number)
		size := disk.SectorSize * p.Size
		if want := plan.wantSize(); want > 0 && want < size {
			resp.Diagnostics.AddWarning("Existing partition kept",
				fmt.Sprintf("Partition %d of %s already exists and is larger than %s: it is kept at its size.", number, dev, plan.Size.ValueString()))
		}
		r.reconcile(ctx, &plan, disk, p, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	plan.ID = types.StringValue(fmt.Sprintf("%s:%d", dev, number))
	r.refresh(ctx, &plan, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

// reconcile brings partition p, which exists, in line with m: its type,
// its name and its size, which can only grow.
func (r *partitionResource) reconcile(ctx context.Context, m *partitionModel, disk *system.DeviceInfo, p *system.Partition, diags diagAdder) {
	dev := m.Device.ValueString()
	number := int(m.Number.ValueInt64())
	if !strings.EqualFold(p.Type, system.PartitionTypeGUID(m.Type.ValueString())) {
		if err := r.client.SetPartitionType(ctx, dev, number, m.Type.ValueString()); err != nil {
			diags.AddError("Cannot change partition type", err.Error())
			return
		}
	}
	if !m.Name.IsNull() && p.Name != m.Name.ValueString() {
		if err := r.client.SetPartitionName(ctx, dev, number, m.Name.ValueString()); err != nil {
			diags.AddError("Cannot change partition name", err.Error())
			return
		}
	}
	want := m.wantSize()
	if (want == 0 && system.PartitionGrowable(disk, p)) || want > disk.SectorSize*p.Size {
		if err := r.client.GrowPartition(ctx, dev, number, want); err != nil {
			diags.AddError("Cannot grow partition", err.Error())
		}
	}
}

// diagAdder is the part of diag.Diagnostics that reconcile needs.
type diagAdder interface {
	AddError(summary, detail string)
	HasError() bool
}

// refresh stores the actual state of the partition, which must exist.
func (r *partitionResource) refresh(ctx context.Context, m *partitionModel, diags diagAdder) {
	disk, err := r.client.DeviceInfo(ctx, m.Device.ValueString())
	if err != nil {
		diags.AddError("Cannot read partition", err.Error())
		return
	}
	var p *system.Partition
	if disk.Table != nil {
		p = disk.Table.Partition(int(m.Number.ValueInt64()))
	}
	if p == nil {
		diags.AddError("Cannot read partition", fmt.Sprintf("partition %d of %s does not exist", m.Number.ValueInt64(), m.Device.ValueString()))
		return
	}
	setPartitionState(m, disk, p)
}

func setPartitionState(m *partitionModel, disk *system.DeviceInfo, p *system.Partition) {
	m.Path = types.StringValue(p.Node)
	m.UUID = types.StringValue(strings.ToLower(p.UUID))
	m.SizeBytes = types.Int64Value(p.Size * disk.SectorSize)
	if !strings.EqualFold(system.PartitionTypeGUID(m.Type.ValueString()), p.Type) {
		m.Type = types.StringValue(strings.ToUpper(p.Type))
	}
	if !m.Name.IsNull() {
		m.Name = types.StringValue(p.Name)
	}
}

func (r *partitionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state partitionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	disk, err := r.client.DeviceInfoNow(ctx, state.Device.ValueString())
	if system.IsNotExist(err) {
		resp.Diagnostics.AddWarning("Disk not found",
			fmt.Sprintf("%s does not exist: the partition will be created when it is back.", state.Device.ValueString()))
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read partition", err.Error())
		return
	}
	var p *system.Partition
	if disk.Table != nil {
		p = disk.Table.Partition(int(state.Number.ValueInt64()))
	}
	if p == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	setPartitionState(&state, disk, p)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *partitionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan partitionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	disk, err := r.client.DeviceInfo(ctx, plan.Device.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read partition", err.Error())
		return
	}
	var p *system.Partition
	if disk.Table != nil {
		p = disk.Table.Partition(int(plan.Number.ValueInt64()))
	}
	if p == nil {
		resp.Diagnostics.AddError("Cannot update partition", fmt.Sprintf("partition %d of %s does not exist", plan.Number.ValueInt64(), plan.Device.ValueString()))
		return
	}
	r.reconcile(ctx, &plan, disk, p, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	r.refresh(ctx, &plan, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

// Delete leaves the partition on the disk: deleting it would lose its data.
func (r *partitionResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

// sizeValidator checks sizes such as 20G.
type sizeValidator struct{}

func (sizeValidator) Description(context.Context) string {
	return "must be a size such as 512M, 20G or 1T"
}

func (v sizeValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (sizeValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := system.ParseSize(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid size", err.Error()+".")
	}
}
