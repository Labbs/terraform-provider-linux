package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

var _ datasource.DataSourceWithConfigure = (*packageDataSource)(nil)

// NewPackageDataSource returns the linux_package data source.
func NewPackageDataSource() datasource.DataSource {
	return &packageDataSource{}
}

type packageDataSource struct {
	dataSourceBase
}

type packageDataModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Installed      types.Bool   `tfsdk:"installed"`
	Version        types.String `tfsdk:"version"`
	PackageManager types.String `tfsdk:"package_manager"`
}

func (d *packageDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_package"
}

func (d *packageDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Tells whether a package is installed, and which version.",
		Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{Computed: true, MarkdownDescription: "Name of the package."},
			"name":            schema.StringAttribute{Required: true, MarkdownDescription: "Name of the package.", Validators: []validator.String{packageNameValidator}},
			"installed":       schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the package is installed."},
			"version":         schema.StringAttribute{Computed: true, MarkdownDescription: "Installed version, null when the package is not installed."},
			"package_manager": schema.StringAttribute{Computed: true, MarkdownDescription: "Package manager of the host."},
		},
	}
}

func (d *packageDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m packageDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	pm, err := d.client.PackageManager(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Cannot query package", err.Error())
		return
	}
	version, err := d.client.Package(ctx, m.Name.ValueString())
	if err != nil && !system.IsNotExist(err) {
		resp.Diagnostics.AddError("Cannot query package", err.Error())
		return
	}
	m.ID = m.Name
	m.PackageManager = types.StringValue(pm)
	m.Installed = types.BoolValue(err == nil)
	m.Version = types.StringNull()
	if err == nil {
		m.Version = types.StringValue(version)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
