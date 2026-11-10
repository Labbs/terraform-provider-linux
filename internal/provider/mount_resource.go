package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

var (
	_ resource.ResourceWithConfigure      = (*mountResource)(nil)
	_ resource.ResourceWithValidateConfig = (*mountResource)(nil)
)

// NewMountResource returns the linux_mount resource.
func NewMountResource() resource.Resource {
	return &mountResource{}
}

type mountResource struct {
	resourceBase
}

type mountModel struct {
	ID      types.String `tfsdk:"id"`
	Path    types.String `tfsdk:"path"`
	Device  types.String `tfsdk:"device"`
	FSType  types.String `tfsdk:"fstype"`
	Options types.String `tfsdk:"options"`
	Dump    types.Int64  `tfsdk:"dump"`
	Pass    types.Int64  `tfsdk:"pass"`
	Mounted types.Bool   `tfsdk:"mounted"`
	Persist types.Bool   `tfsdk:"persist"`
	Fstab   types.String `tfsdk:"fstab"`
}

func (m *mountModel) entry() system.FstabEntry {
	return system.FstabEntry{
		Device:  m.Device.ValueString(),
		Path:    m.Path.ValueString(),
		FSType:  m.FSType.ValueString(),
		Options: m.Options.ValueString(),
		Dump:    int(m.Dump.ValueInt64()),
		Pass:    int(m.Pass.ValueInt64()),
	}
}

func (m *mountModel) id() types.String {
	if m.FSType.ValueString() == "swap" {
		return m.Device
	}
	return m.Path
}

func (r *mountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mount"
}

func (r *mountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Mounts a file system, and records it in `/etc/fstab` so that it is mounted at boot, like Ansible's `mount` module. Also enables swap areas (`fstype = \"swap\"`).\n\n" +
			"An existing fstab entry for the same mount point (or swap device) is replaced. When the file system was already mounted, or listed in fstab, before the resource was created, destroying the resource leaves it as it is; otherwise it is unmounted and removed from fstab. Mount points are created when needed and never removed.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Mount point, or device for swap areas.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"path": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Mount point, such as `/data`. Required, except for swap areas.",
				Validators:          []validator.String{pathValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"device": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "What to mount: `UUID=<uuid>` (recommended, see `linux_filesystem.uuid`), `LABEL=<label>`, a device path, " +
					"`server:/export` for NFS, `tmpfs`...",
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"fstype": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "File system type, such as `ext4`, `xfs`, `nfs`, `tmpfs` or `swap`.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"options": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("defaults"),
				MarkdownDescription: "Mount options, comma-separated, such as `defaults,noatime,nofail`. Changing them remounts the file system. Defaults to `defaults`.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"dump": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(0),
				MarkdownDescription: "Fifth fstab field, used by `dump`. Defaults to `0`.",
				Validators:          []validator.Int64{int64validator.AtLeast(0)},
			},
			"pass": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(0),
				MarkdownDescription: "Sixth fstab field: order of the file system checks at boot, `1` for the root file system, `2` for others, `0` for none. Defaults to `0`.",
				Validators:          []validator.Int64{int64validator.Between(0, 2)},
			},
			"mounted": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Mount the file system now. With `false`, it is unmounted if mounted, and only recorded in fstab. Defaults to `true`.",
			},
			"persist": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Record the mount in `fstab`, so that it is mounted at boot. Defaults to `true`.",
			},
			"fstab": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(system.DefaultFstab),
				MarkdownDescription: "File system table to edit. systemd is reloaded after `/etc/fstab` changes. Defaults to `/etc/fstab`.",
				Validators:          []validator.String{pathValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
		},
	}
}

func (r *mountResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m mountModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.FSType.IsUnknown() {
		return
	}
	swap := m.FSType.ValueString() == "swap"
	switch {
	case swap && !m.Path.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("path"), "Unexpected mount point", "Swap areas have no mount point: remove path.")
	case !swap && m.Path.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("path"), "Missing mount point", "path is required, except for swap areas.")
	}
}

// isMounted reports whether e is mounted, or enabled for a swap area.
func (r *mountResource) isMounted(ctx context.Context, e system.FstabEntry) (bool, error) {
	if e.IsSwap() {
		return r.client.SwapActive(ctx, e.Device)
	}
	m, err := r.client.Mounted(ctx, e.Path)
	return m != nil, err
}

func (r *mountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan mountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	e := plan.entry()
	fstab := plan.Fstab.ValueString()
	created := true

	if plan.Persist.ValueBool() {
		content, err := r.client.Fstab(ctx, fstab)
		if err != nil {
			resp.Diagnostics.AddError("Cannot read fstab", err.Error())
			return
		}
		if _, found := system.FindFstabEntry(content, e); found {
			created = false
		}
		if _, err := r.client.EditFstab(ctx, fstab, e, false); err != nil {
			resp.Diagnostics.AddError("Cannot edit fstab", err.Error())
			return
		}
	}

	mounted, err := r.isMounted(ctx, e)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read mounts", err.Error())
		return
	}
	if mounted {
		created = false
	}
	switch want := plan.Mounted.ValueBool(); {
	case want && !mounted:
		err = r.client.Mount(ctx, e)
	case !want && mounted:
		err = r.client.Unmount(ctx, e)
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot mount", err.Error())
		return
	}

	if created {
		markCreated(ctx, resp.Private, &resp.Diagnostics)
	}
	plan.ID = plan.id()
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *mountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state mountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	e := state.entry()
	if state.Persist.ValueBool() {
		content, err := r.client.Fstab(ctx, state.Fstab.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Cannot read fstab", err.Error())
			return
		}
		if found, ok := system.FindFstabEntry(content, e); ok {
			state.Device = types.StringValue(found.Device)
			state.FSType = types.StringValue(found.FSType)
			state.Options = types.StringValue(found.Options)
			state.Dump = types.Int64Value(int64(found.Dump))
			state.Pass = types.Int64Value(int64(found.Pass))
		} else {
			state.Persist = types.BoolValue(false)
		}
	}
	mounted, err := r.isMounted(ctx, e)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read mounts", err.Error())
		return
	}
	state.Mounted = types.BoolValue(mounted)
	state.ID = state.id()
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *mountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state mountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	old, e := state.entry(), plan.entry()
	fstab := plan.Fstab.ValueString()

	// A swap area changing device, or a mount becoming a swap area, is
	// another fstab entry: remove the old one.
	if state.Persist.ValueBool() && (!plan.Persist.ValueBool() || !old.Same(e)) {
		if _, err := r.client.EditFstab(ctx, fstab, old, true); err != nil {
			resp.Diagnostics.AddError("Cannot edit fstab", err.Error())
			return
		}
	}
	if plan.Persist.ValueBool() {
		if _, err := r.client.EditFstab(ctx, fstab, e, false); err != nil {
			resp.Diagnostics.AddError("Cannot edit fstab", err.Error())
			return
		}
	}

	mounted, err := r.isMounted(ctx, old)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read mounts", err.Error())
		return
	}
	want := plan.Mounted.ValueBool()
	moved := old.Device != e.Device || old.FSType != e.FSType
	if mounted && (!want || moved) {
		if err := r.client.Unmount(ctx, old); err != nil {
			resp.Diagnostics.AddError("Cannot unmount", err.Error())
			return
		}
		mounted = false
	}
	switch {
	case want && !mounted:
		err = r.client.Mount(ctx, e)
	case want && !e.IsSwap() && old.Options != e.Options:
		err = r.client.Remount(ctx, e.Path, e.Options)
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot mount", err.Error())
		return
	}
	plan.ID = plan.id()
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *mountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state mountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !wasCreated(ctx, req.Private, &resp.Diagnostics) {
		// It was there before the resource: leave it.
		return
	}
	e := state.entry()
	r.unmount(ctx, e, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.Persist.ValueBool() {
		if _, err := r.client.EditFstab(ctx, state.Fstab.ValueString(), e, true); err != nil {
			resp.Diagnostics.AddError("Cannot edit fstab", err.Error())
		}
	}
}

func (r *mountResource) unmount(ctx context.Context, e system.FstabEntry, diags *diag.Diagnostics) {
	mounted, err := r.isMounted(ctx, e)
	if err != nil {
		diags.AddError("Cannot read mounts", err.Error())
		return
	}
	if mounted {
		if err := r.client.Unmount(ctx, e); err != nil {
			diags.AddError("Cannot unmount", fmt.Sprintf("%s. Stop the processes using it, or set mounted = false first.", err))
		}
	}
}
