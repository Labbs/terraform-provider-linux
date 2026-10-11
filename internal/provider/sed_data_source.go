package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSourceWithConfigure = (*sedDataSource)(nil)

// NewSedDataSource returns the linux_sed data source.
func NewSedDataSource() datasource.DataSource {
	return &sedDataSource{}
}

type sedDataSource struct {
	dataSourceBase
}

type sedDataModel struct {
	ID          types.String `tfsdk:"id"`
	Path        types.String `tfsdk:"path"`
	Expressions types.List   `tfsdk:"expressions"`
	Extended    types.Bool   `tfsdk:"extended"`
	Pending     types.Bool   `tfsdk:"pending"`
	Result      types.String `tfsdk:"result"`
}

func (d *sedDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sed"
}

func (d *sedDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Runs `sed` expressions on a file without changing it: tells whether they would change the file, and what it would become. " +
			"To test whether a file contains a line, `regexall` on the content of the `linux_file` data source also works.",
		Attributes: map[string]schema.Attribute{
			"id":   schema.StringAttribute{Computed: true, MarkdownDescription: "Path of the file."},
			"path": schema.StringAttribute{Required: true, MarkdownDescription: "Absolute path of the file.", Validators: []validator.String{pathValidator{}}},
			"expressions": schema.ListAttribute{
				Required:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "`sed` expressions, each passed with `-e`.",
				Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
			},
			"extended": schema.BoolAttribute{Optional: true, MarkdownDescription: "Use extended regular expressions (`sed -E`). Defaults to `false`."},
			"pending":  schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether applying the expressions would change the file."},
			"result":   schema.StringAttribute{Computed: true, MarkdownDescription: "Content of the file after applying the expressions."},
		},
	}
}

func (d *sedDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m sedDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var expressions []string
	resp.Diagnostics.Append(m.Expressions.ElementsAs(ctx, &expressions, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := m.Path.ValueString()
	content, err := d.client.ReadFile(ctx, p)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read file", err.Error())
		return
	}
	result, err := d.client.SedPreview(ctx, p, expressions, m.Extended.ValueBool())
	if err != nil {
		resp.Diagnostics.AddError("sed failed", err.Error())
		return
	}
	m.ID = m.Path
	m.Pending = types.BoolValue(string(result) != string(content))
	m.Result = types.StringValue(string(result))
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
