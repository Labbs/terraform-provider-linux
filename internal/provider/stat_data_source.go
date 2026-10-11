package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

var _ datasource.DataSourceWithConfigure = (*statDataSource)(nil)

// NewStatDataSource returns the linux_stat data source.
func NewStatDataSource() datasource.DataSource {
	return &statDataSource{}
}

type statDataSource struct {
	dataSourceBase
}

type statDataModel struct {
	ID         types.String `tfsdk:"id"`
	Path       types.String `tfsdk:"path"`
	Follow     types.Bool   `tfsdk:"follow"`
	Exists     types.Bool   `tfsdk:"exists"`
	Type       types.String `tfsdk:"type"`
	Mode       types.String `tfsdk:"mode"`
	Owner      types.String `tfsdk:"owner"`
	Group      types.String `tfsdk:"group"`
	UID        types.Int64  `tfsdk:"uid"`
	GID        types.Int64  `tfsdk:"gid"`
	Size       types.Int64  `tfsdk:"size"`
	LinkTarget types.String `tfsdk:"link_target"`
}

func (d *statDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_stat"
}

func (d *statDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Tells whether a path exists and what it is, like Ansible's `stat`: use it to test that a file or a directory exists. " +
			"A missing path is not an error: `exists` is `false` and the other attributes are null.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, MarkdownDescription: "The path."},
			"path":        schema.StringAttribute{Required: true, MarkdownDescription: "Absolute path.", Validators: []validator.String{pathValidator{}}},
			"follow":      schema.BoolAttribute{Optional: true, MarkdownDescription: "Describe the target of a symbolic link rather than the link. Defaults to `false`."},
			"exists":      schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether something exists at the path."},
			"type":        schema.StringAttribute{Computed: true, MarkdownDescription: "`file`, `directory`, `symlink` or `other`."},
			"mode":        schema.StringAttribute{Computed: true, MarkdownDescription: "Permissions, as four octal digits."},
			"owner":       schema.StringAttribute{Computed: true, MarkdownDescription: "Owner name, or UID when it has none."},
			"group":       schema.StringAttribute{Computed: true, MarkdownDescription: "Group name, or GID when it has none."},
			"uid":         schema.Int64Attribute{Computed: true, MarkdownDescription: "Owner ID."},
			"gid":         schema.Int64Attribute{Computed: true, MarkdownDescription: "Group ID."},
			"size":        schema.Int64Attribute{Computed: true, MarkdownDescription: "Size in bytes."},
			"link_target": schema.StringAttribute{Computed: true, MarkdownDescription: "Target of a symbolic link."},
		},
	}
}

func (d *statDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m statDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := m.Path.ValueString()
	m.ID = m.Path
	m.Type, m.Mode, m.Owner, m.Group, m.LinkTarget = types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull()
	m.UID, m.GID, m.Size = types.Int64Null(), types.Int64Null(), types.Int64Null()

	stat := d.client.Stat
	if m.Follow.ValueBool() {
		stat = d.client.StatFollow
	}
	info, err := stat(ctx, p)
	switch {
	case system.IsNotExist(err):
		m.Exists = types.BoolValue(false)
		resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
		return
	case err != nil:
		resp.Diagnostics.AddError("Cannot stat path", err.Error())
		return
	}

	m.Exists = types.BoolValue(true)
	m.Type = types.StringValue(string(info.Type))
	m.Mode = types.StringValue(info.Mode)
	m.Owner = types.StringValue(info.Owner)
	m.Group = types.StringValue(info.Group)
	m.UID = int64Value(info.UID)
	m.GID = int64Value(info.GID)
	m.Size = types.Int64Value(info.Size)
	if info.Type == system.TypeSymlink {
		target, err := d.client.Readlink(ctx, p)
		if err != nil {
			resp.Diagnostics.AddError("Cannot read link", err.Error())
			return
		}
		m.LinkTarget = types.StringValue(target)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
