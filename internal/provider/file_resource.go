package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
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
	_ resource.ResourceWithConfigure        = (*fileResource)(nil)
	_ resource.ResourceWithConfigValidators = (*fileResource)(nil)
	_ resource.ResourceWithImportState      = (*fileResource)(nil)
	_ resource.ResourceWithModifyPlan       = (*fileResource)(nil)
)

// NewFileResource returns the linux_file resource.
func NewFileResource() resource.Resource {
	return &fileResource{}
}

type fileResource struct {
	resourceBase
}

type fileModel struct {
	ID            types.String `tfsdk:"id"`
	Path          types.String `tfsdk:"path"`
	Content       types.String `tfsdk:"content"`
	ContentBase64 types.String `tfsdk:"content_base64"`
	Mode          types.String `tfsdk:"mode"`
	Owner         types.String `tfsdk:"owner"`
	Group         types.String `tfsdk:"group"`
	CreateParents types.Bool   `tfsdk:"create_parents"`
	SHA256        types.String `tfsdk:"sha256"`
}

// data returns the configured content, and whether it is known.
func (m *fileModel) data() ([]byte, bool, error) {
	if !m.ContentBase64.IsNull() {
		if m.ContentBase64.IsUnknown() {
			return nil, false, nil
		}
		data, err := base64.StdEncoding.DecodeString(m.ContentBase64.ValueString())
		return data, true, err
	}
	if m.Content.IsUnknown() {
		return nil, false, nil
	}
	return []byte(m.Content.ValueString()), true, nil
}

func (m *fileModel) attributes() system.Attributes {
	return system.Attributes{
		Mode:  knownString(m.Mode),
		Owner: knownString(m.Owner),
		Group: knownString(m.Group),
	}
}

func (r *fileResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_file"
}

func (r *fileResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a file: its content, permissions and ownership. The file is replaced atomically, so readers never see it half written.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Path of the file.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Absolute path of the file.",
				Validators:          []validator.String{pathValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"content": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Content of the file, as UTF-8 text. Conflicts with `content_base64`.",
			},
			"content_base64": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Content of the file, base64-encoded, for binary files. Conflicts with `content`.",
				Validators:          []validator.String{base64Validator{}},
			},
			"mode": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("0644"),
				MarkdownDescription: "Permissions, in octal. Defaults to `0644`.",
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
				MarkdownDescription: "Create missing parent directories. They are not removed with the file. Defaults to `true`.",
			},
			"sha256": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "SHA-256 digest of the content, hex-encoded. Known at plan time: use it to restart a service when the file changes.",
			},
		},
	}
}

func (r *fileResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(path.MatchRoot("content"), path.MatchRoot("content_base64")),
	}
}

func (r *fileResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan fileModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data, known, err := plan.data()
	if err != nil {
		return // Reported by the validator.
	}
	sum := types.StringUnknown()
	if known {
		sum = types.StringValue(sha256Hex(data))
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("sha256"), sum)...)
}

func (r *fileResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan fileModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data, _, err := plan.data()
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("content_base64"), "Invalid base64 content", err.Error())
		return
	}
	p := plan.Path.ValueString()
	if err := r.client.WriteFile(ctx, p, bytes.NewReader(data), plan.attributes(), plan.CreateParents.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Cannot create file", err.Error())
		return
	}
	plan.ID = plan.Path
	plan.SHA256 = types.StringValue(sha256Hex(data))
	r.refreshAttributes(ctx, &plan, resp.Diagnostics.AddError)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *fileResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state fileModel
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
		resp.Diagnostics.AddError("Cannot read file", err.Error())
		return
	}
	if info.Type != system.TypeFile {
		// Recreating the resource replaces whatever is there with a file.
		resp.Diagnostics.AddWarning("Not a regular file", fmt.Sprintf("%s is a %s, it will be replaced by a file.", p, info.Type))
		resp.State.RemoveResource(ctx)
		return
	}

	// Only download the content when it changed.
	sum, err := r.client.FileSHA256(ctx, p)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read file", err.Error())
		return
	}
	if sum != state.SHA256.ValueString() {
		data, err := r.client.ReadFile(ctx, p)
		if err != nil {
			resp.Diagnostics.AddError("Cannot read file", err.Error())
			return
		}
		if !state.ContentBase64.IsNull() || (state.Content.IsNull() && !utf8.Valid(data)) {
			state.ContentBase64 = types.StringValue(base64.StdEncoding.EncodeToString(data))
		} else {
			state.Content = types.StringValue(string(data))
		}
		state.SHA256 = types.StringValue(sha256Hex(data))
	}

	state.ID = state.Path
	state.Mode = modeValue(state.Mode, info.Mode)
	state.Owner = ownerValue(state.Owner, info.Owner, info.UID)
	state.Group = ownerValue(state.Group, info.Group, info.GID)
	if state.CreateParents.IsNull() {
		state.CreateParents = types.BoolValue(true)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *fileResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state fileModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data, _, err := plan.data()
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("content_base64"), "Invalid base64 content", err.Error())
		return
	}
	p := plan.Path.ValueString()
	sum := sha256Hex(data)

	if sum != state.SHA256.ValueString() {
		err = r.client.WriteFile(ctx, p, bytes.NewReader(data), plan.attributes(), plan.CreateParents.ValueBool())
	} else if plan.attributes() != state.attributes() {
		err = r.client.SetAttributes(ctx, p, plan.attributes())
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot update file", err.Error())
		return
	}

	plan.SHA256 = types.StringValue(sum)
	r.refreshAttributes(ctx, &plan, resp.Diagnostics.AddError)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *fileResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state fileModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Remove(ctx, state.Path.ValueString()); err != nil {
		resp.Diagnostics.AddError("Cannot delete file", err.Error())
	}
}

func (r *fileResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if err := system.CheckPath(req.ID); err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("The ID must be the path of the file: %q %s.", req.ID, err))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("path"), req.ID)...)
}

// refreshAttributes stores the actual mode, owner and group of the file.
func (r *fileResource) refreshAttributes(ctx context.Context, m *fileModel, addError func(string, string)) {
	info, err := r.client.Stat(ctx, m.Path.ValueString())
	if err != nil {
		addError("Cannot read file", err.Error())
		return
	}
	m.Mode = modeValue(m.Mode, info.Mode)
	m.Owner = ownerValue(m.Owner, info.Owner, info.UID)
	m.Group = ownerValue(m.Group, info.Group, info.GID)
}
