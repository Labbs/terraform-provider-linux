package provider

import (
	"context"
	"fmt"
	"regexp"

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
	_ resource.ResourceWithConfigure  = (*downloadResource)(nil)
	_ resource.ResourceWithModifyPlan = (*downloadResource)(nil)
)

var (
	urlValidator = stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://.`),
		`must be a URL, such as "https://example.com/file.tar.gz"`)
	sha256Validator = stringvalidator.RegexMatches(regexp.MustCompile(`^[0-9a-f]{64}$`),
		"must be a SHA-256 digest: 64 lowercase hexadecimal digits")
)

// NewDownloadResource returns the linux_download resource.
func NewDownloadResource() resource.Resource {
	return &downloadResource{}
}

type downloadResource struct {
	resourceBase
}

type downloadModel struct {
	ID            types.String `tfsdk:"id"`
	URL           types.String `tfsdk:"url"`
	Path          types.String `tfsdk:"path"`
	SHA256        types.String `tfsdk:"sha256"`
	Mode          types.String `tfsdk:"mode"`
	Owner         types.String `tfsdk:"owner"`
	Group         types.String `tfsdk:"group"`
	CreateParents types.Bool   `tfsdk:"create_parents"`
}

func (m *downloadModel) attributes() system.Attributes {
	return system.Attributes{
		Mode:  knownString(m.Mode),
		Owner: knownString(m.Owner),
		Group: knownString(m.Group),
	}
}

func (r *downloadResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_download"
}

func (r *downloadResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Downloads a file from a URL to the host, with `curl` or `wget` run on the host. The file is downloaded again when `url` or `sha256` changes, or when it was modified outside of Terraform. It is replaced atomically, and only once its checksum is verified.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Path of the file.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"url": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "URL to download, fetched from the host. Any scheme that `curl` (or `wget`) supports, such as `https://` or `file://`. Changing it downloads the file again.",
				Validators:          []validator.String{urlValidator},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Absolute path of the file on the host.",
				Validators:          []validator.String{pathValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"sha256": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Expected SHA-256 digest of the file, hex-encoded. A download with another digest fails and leaves the file as it was. When unset, the digest of the downloaded file. Set it to protect against tampered downloads, and to know the content at plan time.",
				Validators:          []validator.String{sha256Validator},
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
		},
	}
}

// ModifyPlan plans the digest: the configured one, the current one when the
// same file stays in place, or an unknown one when it is downloaded again.
func (r *downloadResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var plan, state, config downloadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	switch {
	case !config.SHA256.IsNull():
		if !config.SHA256.Equal(state.SHA256) {
			resp.RequiresReplace.Append(path.Root("sha256"))
		}
	case plan.URL.Equal(state.URL) && plan.Path.Equal(state.Path):
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("sha256"), state.SHA256)...)
	default:
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("sha256"), types.StringUnknown())...)
	}
}

func (r *downloadResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan downloadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	sum, err := r.client.Download(ctx, plan.URL.ValueString(), plan.Path.ValueString(), knownString(plan.SHA256),
		plan.attributes(), plan.CreateParents.ValueBool())
	if err != nil {
		resp.Diagnostics.AddError("Cannot download file", err.Error())
		return
	}
	plan.ID = plan.Path
	plan.SHA256 = types.StringValue(sum)
	r.refreshAttributes(ctx, &plan, resp.Diagnostics.AddError)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *downloadResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state downloadModel
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
		resp.Diagnostics.AddWarning("Not a regular file", fmt.Sprintf("%s is a %s, it will be replaced by the download.", p, info.Type))
		resp.State.RemoveResource(ctx)
		return
	}
	sum, err := r.client.FileSHA256(ctx, p)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read file", err.Error())
		return
	}
	if sum != state.SHA256.ValueString() {
		resp.Diagnostics.AddWarning("File changed outside of Terraform",
			fmt.Sprintf("The content of %s changed since it was downloaded: it will be downloaded again.", p))
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
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update changes the permissions and ownership: any other change downloads
// the file again, through a replacement.
func (r *downloadResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state downloadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.attributes() != state.attributes() {
		if err := r.client.SetAttributes(ctx, plan.Path.ValueString(), plan.attributes()); err != nil {
			resp.Diagnostics.AddError("Cannot update file", err.Error())
			return
		}
	}
	plan.ID = state.ID
	plan.SHA256 = state.SHA256
	r.refreshAttributes(ctx, &plan, resp.Diagnostics.AddError)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *downloadResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state downloadModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Remove(ctx, state.Path.ValueString()); err != nil {
		resp.Diagnostics.AddError("Cannot delete file", err.Error())
	}
}

// refreshAttributes stores the actual mode, owner and group of the file.
func (r *downloadResource) refreshAttributes(ctx context.Context, m *downloadModel, addError func(string, string)) {
	info, err := r.client.Stat(ctx, m.Path.ValueString())
	if err != nil {
		addError("Cannot read file", err.Error())
		return
	}
	m.Mode = modeValue(m.Mode, info.Mode)
	m.Owner = ownerValue(m.Owner, info.Owner, info.UID)
	m.Group = ownerValue(m.Group, info.Group, info.GID)
}
