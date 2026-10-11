package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSourceWithConfigure = (*systemdUnitDataSource)(nil)

// NewSystemdUnitDataSource returns the linux_systemd_unit data source.
func NewSystemdUnitDataSource() datasource.DataSource {
	return &systemdUnitDataSource{}
}

type systemdUnitDataSource struct {
	dataSourceBase
}

type systemdUnitDataModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Exists        types.Bool   `tfsdk:"exists"`
	Enabled       types.Bool   `tfsdk:"enabled"`
	Active        types.Bool   `tfsdk:"active"`
	Masked        types.Bool   `tfsdk:"masked"`
	LoadState     types.String `tfsdk:"load_state"`
	ActiveState   types.String `tfsdk:"active_state"`
	SubState      types.String `tfsdk:"sub_state"`
	UnitFileState types.String `tfsdk:"unit_file_state"`
}

func (d *systemdUnitDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_systemd_unit"
}

func (d *systemdUnitDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads the state of a systemd unit.",
		Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{Computed: true, MarkdownDescription: "Name of the unit."},
			"name":            schema.StringAttribute{Required: true, MarkdownDescription: "Name of the unit, such as `nginx.service`.", Validators: []validator.String{unitNameValidator}},
			"exists":          schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether systemd knows the unit."},
			"enabled":         schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the unit starts at boot."},
			"active":          schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the unit is running."},
			"masked":          schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the unit is masked."},
			"load_state":      schema.StringAttribute{Computed: true, MarkdownDescription: "`LoadState` property, such as `loaded` or `not-found`."},
			"active_state":    schema.StringAttribute{Computed: true, MarkdownDescription: "`ActiveState` property, such as `active` or `failed`."},
			"sub_state":       schema.StringAttribute{Computed: true, MarkdownDescription: "`SubState` property, such as `running` or `dead`."},
			"unit_file_state": schema.StringAttribute{Computed: true, MarkdownDescription: "`UnitFileState` property, such as `enabled` or `static`."},
		},
	}
}

func (d *systemdUnitDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m systemdUnitDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	status, err := d.client.UnitStatus(ctx, m.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read unit", err.Error())
		return
	}
	m.ID = m.Name
	m.Exists = types.BoolValue(status.Exists())
	m.Enabled = types.BoolValue(status.Enabled())
	m.Active = types.BoolValue(status.Active())
	m.Masked = types.BoolValue(status.Masked())
	m.LoadState = types.StringValue(status.LoadState)
	m.ActiveState = types.StringValue(status.ActiveState)
	m.SubState = types.StringValue(status.SubState)
	m.UnitFileState = types.StringValue(status.UnitFileState)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
