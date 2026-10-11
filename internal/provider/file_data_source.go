package provider

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

var _ datasource.DataSourceWithConfigure = (*fileDataSource)(nil)

// NewFileDataSource returns the linux_file data source.
func NewFileDataSource() datasource.DataSource {
	return &fileDataSource{}
}

type fileDataSource struct {
	dataSourceBase
}

type fileDataModel struct {
	ID            types.String `tfsdk:"id"`
	Path          types.String `tfsdk:"path"`
	Content       types.String `tfsdk:"content"`
	ContentBase64 types.String `tfsdk:"content_base64"`
	SHA256        types.String `tfsdk:"sha256"`
	Mode          types.String `tfsdk:"mode"`
	Owner         types.String `tfsdk:"owner"`
	Group         types.String `tfsdk:"group"`
}

func (d *fileDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_file"
}

func (d *fileDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a file.",
		Attributes: map[string]schema.Attribute{
			"id":             schema.StringAttribute{Computed: true, MarkdownDescription: "Path of the file."},
			"path":           schema.StringAttribute{Required: true, MarkdownDescription: "Absolute path of the file.", Validators: []validator.String{pathValidator{}}},
			"content":        schema.StringAttribute{Computed: true, MarkdownDescription: "Content of the file, as text."},
			"content_base64": schema.StringAttribute{Computed: true, MarkdownDescription: "Content of the file, base64-encoded."},
			"sha256":         schema.StringAttribute{Computed: true, MarkdownDescription: "SHA-256 digest of the content, hex-encoded."},
			"mode":           schema.StringAttribute{Computed: true, MarkdownDescription: "Permissions, as four octal digits."},
			"owner":          schema.StringAttribute{Computed: true, MarkdownDescription: "Owner name, or UID when it has none."},
			"group":          schema.StringAttribute{Computed: true, MarkdownDescription: "Group name, or GID when it has none."},
		},
	}
}

func (d *fileDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m fileDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := m.Path.ValueString()
	info, err := d.client.Stat(ctx, p)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read file", err.Error())
		return
	}
	if info.Type == system.TypeDirectory {
		resp.Diagnostics.AddError("Cannot read file", fmt.Sprintf("%s is a directory.", p))
		return
	}
	data, err := d.client.ReadFile(ctx, p)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read file", err.Error())
		return
	}
	m.ID = m.Path
	m.Content = types.StringValue(string(data))
	m.ContentBase64 = types.StringValue(base64.StdEncoding.EncodeToString(data))
	m.SHA256 = types.StringValue(sha256Hex(data))
	m.Mode = types.StringValue(info.Mode)
	m.Owner = types.StringValue(info.Owner)
	m.Group = types.StringValue(info.Group)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
