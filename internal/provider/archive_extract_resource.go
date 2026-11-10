package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

var (
	_ resource.ResourceWithConfigure      = (*archiveExtractResource)(nil)
	_ resource.ResourceWithValidateConfig = (*archiveExtractResource)(nil)
)

// NewArchiveExtractResource returns the linux_archive_extract resource.
func NewArchiveExtractResource() resource.Resource {
	return &archiveExtractResource{}
}

type archiveExtractResource struct {
	resourceBase
}

type archiveExtractModel struct {
	ID              types.String `tfsdk:"id"`
	Source          types.String `tfsdk:"source"`
	Destination     types.String `tfsdk:"destination"`
	Format          types.String `tfsdk:"format"`
	StripComponents types.Int64  `tfsdk:"strip_components"`
	Triggers        types.Map    `tfsdk:"triggers"`
	Files           types.List   `tfsdk:"files"`
}

func (r *archiveExtractResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_archive_extract"
}

func (r *archiveExtractResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Extracts an archive that is on the host, such as one fetched with `linux_download`, into a directory. " +
			"Files already in the directory are replaced, even running programs. Destroying the resource removes the extracted files, and the directories they leave empty, but not the destination directory. " +
			"Every change extracts the archive again. The archive is not watched: to extract a new version that has the same path, reference its digest in `triggers`. " +
			"Needs `tar` on the host, with `gzip`, `bzip2` or `xz` for compressed archives, or `unzip` for zip archives.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Random identifier.",
			},
			"source": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Absolute path of the archive on the host.",
				Validators:          []validator.String{pathValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"destination": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Absolute path of the directory to extract into. It is created if needed.",
				Validators:          []validator.String{pathValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"format": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Format of the archive: " + strings.Join(quoteAll(system.ArchiveFormats), ", ") +
					". A `gz` file holds a single file, extracted under the name of the archive without `.gz`. Defaults to the format given by the extension of `source`.",
				Validators:    []validator.String{stringvalidator.OneOf(system.ArchiveFormats...)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"strip_components": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(0),
				MarkdownDescription: "Number of leading directories to remove from the paths in the archive, like `tar --strip-components`: with `1`, `node_exporter-1.8.2.linux-amd64/node_exporter` is extracted as `node_exporter`. Defaults to `0`.",
				Validators:          []validator.Int64{int64validator.AtLeast(0)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"triggers": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Arbitrary values that extract the archive again when they change, such as the `sha256` of the `linux_download` resource that fetched it.",
				PlanModifiers:       []planmodifier.Map{mapplanmodifier.RequiresReplace()},
			},
			"files": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Extracted files, relative to `destination`, sorted. Directories are not listed.",
			},
		},
	}
}

func (r *archiveExtractResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m archiveExtractModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !m.Format.IsNull() || m.Source.IsNull() || m.Source.IsUnknown() {
		return
	}
	if _, ok := system.ArchiveFormat(m.Source.ValueString()); !ok {
		resp.Diagnostics.AddAttributeError(path.Root("format"), "Unknown archive format",
			fmt.Sprintf("The format of %s cannot be told from its extension: set format to one of %s.",
				m.Source.ValueString(), strings.Join(system.ArchiveFormats, ", ")))
	}
}

func (r *archiveExtractResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan archiveExtractModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	src := plan.Source.ValueString()
	files, err := r.client.ExtractArchive(ctx, src, knownString(plan.Format), plan.Destination.ValueString(), int(plan.StripComponents.ValueInt64()))
	if system.IsNotExist(err) {
		resp.Diagnostics.AddAttributeError(path.Root("source"), "Archive not found", fmt.Sprintf("%s does not exist on the host.", src))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot extract archive", err.Error())
		return
	}
	list, diags := types.ListValueFrom(ctx, types.StringType, files)
	resp.Diagnostics.Append(diags...)
	plan.ID = types.StringValue(randomID())
	plan.Files = list
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *archiveExtractResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state archiveExtractModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	files := r.files(ctx, state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	dest := state.Destination.ValueString()
	missing, err := r.client.MissingFiles(ctx, dest, files)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read extracted files", err.Error())
		return
	}
	if len(missing) > 0 {
		resp.Diagnostics.AddWarning("Extracted files are missing",
			fmt.Sprintf("%d of the files extracted into %s are missing, such as %s: the archive will be extracted again.", len(missing), dest, missing[0]))
		resp.State.RemoveResource(ctx)
	}
}

// Update is only called when nothing changed: every attribute that can be
// configured replaces the resource.
func (r *archiveExtractResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state archiveExtractModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	plan.Files = state.Files
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *archiveExtractResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state archiveExtractModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	files := r.files(ctx, state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.RemoveFiles(ctx, state.Destination.ValueString(), files); err != nil {
		resp.Diagnostics.AddError("Cannot remove extracted files", err.Error())
	}
}

// files returns the extracted files recorded in the state.
func (r *archiveExtractResource) files(ctx context.Context, m archiveExtractModel, diags *diag.Diagnostics) []string {
	var files []string
	if !m.Files.IsNull() && !m.Files.IsUnknown() {
		diags.Append(m.Files.ElementsAs(ctx, &files, false)...)
	}
	return files
}
