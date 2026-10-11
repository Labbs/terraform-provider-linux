package provider

import (
	"context"
	"regexp"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

var (
	_ resource.ResourceWithConfigure   = (*userResource)(nil)
	_ resource.ResourceWithImportState = (*userResource)(nil)
)

var accountNameValidator = stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9._-]{0,30}\$?$`),
	"must be a user or group name")

// NewUserResource returns the linux_user resource.
func NewUserResource() resource.Resource {
	return &userResource{}
}

type userResource struct {
	resourceBase
}

type userModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	UID          types.Int64  `tfsdk:"uid"`
	Group        types.String `tfsdk:"group"`
	Groups       types.Set    `tfsdk:"groups"`
	Comment      types.String `tfsdk:"comment"`
	Home         types.String `tfsdk:"home"`
	Shell        types.String `tfsdk:"shell"`
	System       types.Bool   `tfsdk:"system"`
	CreateHome   types.Bool   `tfsdk:"create_home"`
	PasswordHash types.String `tfsdk:"password_hash"`
	RemoveHome   types.Bool   `tfsdk:"remove_home"`
}

// options returns the settings of m that are known and differ from prior,
// or all known settings when prior is nil.
func (m *userModel) options(ctx context.Context, prior *userModel, diags *diag.Diagnostics) system.UserOptions {
	var p userModel
	isNew := prior == nil
	if !isNew {
		p = *prior
	}
	var o system.UserOptions
	if isNew || !m.UID.Equal(p.UID) {
		o.UID = knownInt64(m.UID)
	}
	if isNew || !m.Group.Equal(p.Group) {
		o.Group = knownString(m.Group)
	}
	if isNew || !m.Comment.Equal(p.Comment) {
		o.Comment = knownString(m.Comment)
	}
	if isNew || !m.Home.Equal(p.Home) {
		o.Home = knownString(m.Home)
	}
	if isNew || !m.Shell.Equal(p.Shell) {
		o.Shell = knownString(m.Shell)
	}
	if !m.Groups.IsNull() && !m.Groups.IsUnknown() && (isNew || !m.Groups.Equal(p.Groups)) {
		o.SetGroups = true
		diags.Append(m.Groups.ElementsAs(ctx, &o.Groups, false)...)
		slices.Sort(o.Groups)
	}
	return o
}

func (r *userResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (r *userResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{
			Optional:            true,
			Computed:            true,
			MarkdownDescription: description,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a user account with `useradd`, `usermod` and `userdel` (on Alpine, install the `shadow` package). " +
			"An existing account is adopted. Destroying the resource deletes the account only if this resource created it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Name of the user.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the user.",
				Validators:          []validator.String{accountNameValidator},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"uid": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "User ID. Defaults to the next free one.",
				Validators:          []validator.Int64{int64validator.AtLeast(0)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"group":   computedString("Primary group, which must exist. Defaults to the system default, usually a group named after the user."),
			"comment": computedString("Comment (GECOS field), usually the full name."),
			"home":    computedString("Home directory. Changing it does not move the existing one."),
			"shell":   computedString("Login shell."),
			"groups": schema.SetAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Supplementary groups, which must exist. When set, the user belongs to these groups only. Leave unset to not manage them.",
				Validators:          []validator.Set{setvalidator.ValueStringsAre(accountNameValidator)},
			},
			"system": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Create a system account. Only used on creation. Defaults to `false`.",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"create_home": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Create the home directory. Only used on creation. Defaults to `true`.",
			},
			"password_hash": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				MarkdownDescription: "Password, hashed for `/etc/shadow`, such as the output of `openssl passwd -6`. It is sent on standard input. " +
					"Changes made outside of Terraform are not detected.",
				Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[^:\n]+$`), "must be a password hash")},
			},
			"remove_home": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Delete the home directory and mail spool with the account. Defaults to `false`.",
			},
		},
	}
}

func (r *userResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := plan.Name.ValueString()
	options := plan.options(ctx, nil, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.User(ctx, name)
	switch {
	case system.IsNotExist(err):
		err = r.client.CreateUser(ctx, name, options, plan.System.ValueBool(), plan.CreateHome.ValueBool())
		if err == nil {
			markCreated(ctx, resp.Private, &resp.Diagnostics)
		}
	case err == nil:
		err = r.client.ModifyUser(ctx, name, options)
	}
	if err == nil && !plan.PasswordHash.IsNull() {
		err = r.client.SetPasswordHash(ctx, name, plan.PasswordHash.ValueString())
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot create user", err.Error())
		return
	}
	plan.ID = plan.Name
	r.mustRefresh(ctx, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *userResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.refresh(ctx, &state, &resp.Diagnostics) {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = state.Name
	for _, d := range []struct {
		value *types.Bool
		def   bool
	}{{&state.System, false}, {&state.CreateHome, true}, {&state.RemoveHome, false}} {
		if d.value.IsNull() {
			*d.value = types.BoolValue(d.def)
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *userResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := plan.Name.ValueString()
	options := plan.options(ctx, &state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.ModifyUser(ctx, name, options)
	if err == nil && !plan.PasswordHash.IsNull() && !plan.PasswordHash.Equal(state.PasswordHash) {
		err = r.client.SetPasswordHash(ctx, name, plan.PasswordHash.ValueString())
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot update user", err.Error())
		return
	}
	r.mustRefresh(ctx, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *userResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !wasCreated(ctx, req.Private, &resp.Diagnostics) {
		return
	}
	name := state.Name.ValueString()
	if _, err := r.client.User(ctx, name); system.IsNotExist(err) {
		return
	}
	if err := r.client.DeleteUser(ctx, name, state.RemoveHome.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Cannot delete user", err.Error())
	}
}

func (r *userResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
}

// refresh stores the actual settings of the account. It returns false when
// the account does not exist.
func (r *userResource) refresh(ctx context.Context, m *userModel, diags *diag.Diagnostics) bool {
	user, err := r.client.User(ctx, m.Name.ValueString())
	if system.IsNotExist(err) {
		return false
	}
	if err != nil {
		diags.AddError("Cannot read user", err.Error())
		return true
	}
	m.UID = int64Value(user.UID)
	m.Group = types.StringValue(user.Group)
	m.Comment = types.StringValue(user.Comment)
	m.Home = types.StringValue(user.Home)
	m.Shell = types.StringValue(user.Shell)
	if !m.Groups.IsNull() {
		groups, d := types.SetValueFrom(ctx, types.StringType, user.Groups)
		diags.Append(d...)
		m.Groups = groups
	}
	return true
}

// mustRefresh is refresh, for an account that must exist.
func (r *userResource) mustRefresh(ctx context.Context, m *userModel, diags *diag.Diagnostics) {
	if !r.refresh(ctx, m, diags) {
		diags.AddError("Cannot read user", "The user "+m.Name.ValueString()+" does not exist.")
	}
}
