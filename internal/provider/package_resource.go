package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

var (
	_ resource.ResourceWithConfigure   = (*packageResource)(nil)
	_ resource.ResourceWithImportState = (*packageResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*packageResource)(nil)
)

var (
	packageNameValidator = stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+:@-]*$`),
		"must be a package name")
	packageVersionValidator = stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+:~-]*$`),
		"must be a package version")
)

// NewPackageResource returns the linux_package resource.
func NewPackageResource() resource.Resource {
	return &packageResource{}
}

type packageResource struct {
	resourceBase
}

type packageModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Version          types.String `tfsdk:"version"`
	InstalledVersion types.String `tfsdk:"installed_version"`
}

func (r *packageResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_package"
}

func (r *packageResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Installs a package with the package manager of the host: apt, dnf, yum, zypper, apk or pacman. " +
			"Destroying the resource removes the package only if this resource installed it: a package that was already installed is left in place.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Name of the package.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the package.",
				Validators:          []validator.String{packageNameValidator},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"version": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Version to install, exactly as the package manager shows it (see `installed_version`), such as `1.6-2.1+deb12u1` with apt. " +
					"Without it, any installed version is accepted. Not supported with pacman.",
				Validators: []validator.String{packageVersionValidator},
			},
			"installed_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Installed version.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *packageResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var version types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("version"), &version)...)
	if !version.IsNull() && !version.IsUnknown() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("installed_version"), version)...)
	}
}

func (r *packageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan packageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name, version := plan.Name.ValueString(), plan.Version.ValueString()
	installed, err := r.client.Package(ctx, name)
	switch {
	case system.IsNotExist(err):
		err = r.client.InstallPackage(ctx, name, version)
		if err == nil {
			markCreated(ctx, resp.Private, &resp.Diagnostics)
		}
	case err == nil && version != "" && installed != version:
		err = r.client.InstallPackage(ctx, name, version)
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot install package", err.Error())
		return
	}
	if !r.refresh(ctx, &plan, resp.Diagnostics.AddError) {
		return
	}
	plan.ID = plan.Name
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *packageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state packageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	installed, err := r.client.Package(ctx, state.Name.ValueString())
	if system.IsNotExist(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read package", err.Error())
		return
	}
	state.ID = state.Name
	state.InstalledVersion = types.StringValue(installed)
	if !state.Version.IsNull() {
		state.Version = types.StringValue(installed)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *packageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state packageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if version := plan.Version.ValueString(); version != "" && version != state.InstalledVersion.ValueString() {
		if err := r.client.InstallPackage(ctx, plan.Name.ValueString(), version); err != nil {
			resp.Diagnostics.AddError("Cannot install package", err.Error())
			return
		}
	}
	if !r.refresh(ctx, &plan, resp.Diagnostics.AddError) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *packageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state packageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !wasCreated(ctx, req.Private, &resp.Diagnostics) {
		return
	}
	name := state.Name.ValueString()
	if _, err := r.client.Package(ctx, name); system.IsNotExist(err) {
		return
	}
	if err := r.client.RemovePackage(ctx, name); err != nil {
		resp.Diagnostics.AddError("Cannot remove package", err.Error())
	}
}

func (r *packageResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
}

// refresh stores the installed version, which must be the planned one.
func (r *packageResource) refresh(ctx context.Context, m *packageModel, addError func(string, string)) bool {
	installed, err := r.client.Package(ctx, m.Name.ValueString())
	if err != nil {
		addError("Cannot read package", err.Error())
		return false
	}
	if version := m.Version.ValueString(); version != "" && installed != version {
		addError("Unexpected version", fmt.Sprintf("Version %s of %s is installed instead of %s.", installed, m.Name.ValueString(), version))
		return false
	}
	m.InstalledVersion = types.StringValue(installed)
	return true
}
