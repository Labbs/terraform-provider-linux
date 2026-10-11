package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

var _ datasource.DataSourceWithConfigure = (*userDataSource)(nil)

// NewUserDataSource returns the linux_user data source.
func NewUserDataSource() datasource.DataSource {
	return &userDataSource{}
}

type userDataSource struct {
	dataSourceBase
}

type userDataModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	Exists  types.Bool   `tfsdk:"exists"`
	UID     types.Int64  `tfsdk:"uid"`
	GID     types.Int64  `tfsdk:"gid"`
	Group   types.String `tfsdk:"group"`
	Groups  types.Set    `tfsdk:"groups"`
	Comment types.String `tfsdk:"comment"`
	Home    types.String `tfsdk:"home"`
	Shell   types.String `tfsdk:"shell"`
}

func (d *userDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (d *userDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a user account, like `getent passwd`. A missing account is not an error: `exists` is `false` and the other attributes are null.",
		Attributes: map[string]schema.Attribute{
			"id":      schema.StringAttribute{Computed: true, MarkdownDescription: "Name of the user."},
			"name":    schema.StringAttribute{Required: true, MarkdownDescription: "Name of the user.", Validators: []validator.String{accountNameValidator}},
			"exists":  schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the account exists."},
			"uid":     schema.Int64Attribute{Computed: true, MarkdownDescription: "User ID."},
			"gid":     schema.Int64Attribute{Computed: true, MarkdownDescription: "ID of the primary group."},
			"group":   schema.StringAttribute{Computed: true, MarkdownDescription: "Primary group."},
			"groups":  schema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Supplementary groups."},
			"comment": schema.StringAttribute{Computed: true, MarkdownDescription: "Comment (GECOS field)."},
			"home":    schema.StringAttribute{Computed: true, MarkdownDescription: "Home directory."},
			"shell":   schema.StringAttribute{Computed: true, MarkdownDescription: "Login shell."},
		},
	}
}

func (d *userDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m userDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	user, err := d.client.User(ctx, m.Name.ValueString())
	if err != nil && !system.IsNotExist(err) {
		resp.Diagnostics.AddError("Cannot read user", err.Error())
		return
	}
	m.ID = m.Name
	m.Exists = types.BoolValue(err == nil)
	if err != nil {
		m.UID, m.GID = types.Int64Null(), types.Int64Null()
		m.Group, m.Comment, m.Home, m.Shell = types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull()
		m.Groups = types.SetNull(types.StringType)
		resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
		return
	}
	m.UID = int64Value(user.UID)
	m.GID = int64Value(user.GID)
	m.Group = types.StringValue(user.Group)
	m.Comment = types.StringValue(user.Comment)
	m.Home = types.StringValue(user.Home)
	m.Shell = types.StringValue(user.Shell)
	groups, diags := types.SetValueFrom(ctx, types.StringType, user.Groups)
	resp.Diagnostics.Append(diags...)
	m.Groups = groups
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
