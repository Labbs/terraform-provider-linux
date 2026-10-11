package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

var _ datasource.DataSourceWithConfigure = (*groupDataSource)(nil)

// NewGroupDataSource returns the linux_group data source.
func NewGroupDataSource() datasource.DataSource {
	return &groupDataSource{}
}

type groupDataSource struct {
	dataSourceBase
}

type groupDataModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	Exists  types.Bool   `tfsdk:"exists"`
	GID     types.Int64  `tfsdk:"gid"`
	Members types.List   `tfsdk:"members"`
}

func (d *groupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (d *groupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a group, like `getent group`. A missing group is not an error: `exists` is `false` and the other attributes are null.",
		Attributes: map[string]schema.Attribute{
			"id":      schema.StringAttribute{Computed: true, MarkdownDescription: "Name of the group."},
			"name":    schema.StringAttribute{Required: true, MarkdownDescription: "Name of the group.", Validators: []validator.String{accountNameValidator}},
			"exists":  schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the group exists."},
			"gid":     schema.Int64Attribute{Computed: true, MarkdownDescription: "Group ID."},
			"members": schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Users having the group as a supplementary group."},
		},
	}
}

func (d *groupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m groupDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	group, err := d.client.Group(ctx, m.Name.ValueString())
	if err != nil && !system.IsNotExist(err) {
		resp.Diagnostics.AddError("Cannot read group", err.Error())
		return
	}
	m.ID = m.Name
	m.Exists = types.BoolValue(err == nil)
	m.GID, m.Members = types.Int64Null(), types.ListNull(types.StringType)
	if err == nil {
		m.GID = int64Value(group.GID)
		members, diags := types.ListValueFrom(ctx, types.StringType, group.Members)
		resp.Diagnostics.Append(diags...)
		m.Members = members
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
