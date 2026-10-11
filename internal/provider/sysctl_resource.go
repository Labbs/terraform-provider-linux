package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
	"github.com/labbs/terraform-provider-linux/internal/textedit"
)

var (
	_ resource.ResourceWithConfigure   = (*sysctlResource)(nil)
	_ resource.ResourceWithImportState = (*sysctlResource)(nil)
)

const defaultSysctlFile = "/etc/sysctl.d/99-terraform.conf"

// NewSysctlResource returns the linux_sysctl resource.
func NewSysctlResource() resource.Resource {
	return &sysctlResource{}
}

type sysctlResource struct {
	resourceBase
}

type sysctlModel struct {
	ID    types.String `tfsdk:"id"`
	Name  types.String `tfsdk:"name"`
	Value types.String `tfsdk:"value"`
	File  types.String `tfsdk:"file"`
}

// line returns the line persisting the parameter.
func (m *sysctlModel) line() textedit.Line {
	name := m.Name.ValueString()
	return textedit.Line{
		Line:  name + " = " + system.NormalizeSysctl(m.Value.ValueString()),
		Match: sysctlLine(name),
	}
}

func sysctlLine(name string) *regexp.Regexp {
	return regexp.MustCompile(`^\s*` + regexp.QuoteMeta(name) + `\s*=`)
}

func (r *sysctlResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sysctl"
}

func (r *sysctlResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Sets a kernel parameter now, with `sysctl -w`, and after reboots, in a file of `/etc/sysctl.d`. " +
			"Destroying the resource removes the parameter from the file but leaves its current value.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Name of the parameter.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the parameter, such as `net.ipv4.ip_forward`.",
				Validators: []validator.String{stringvalidator.RegexMatches(
					regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*$`), "must be a kernel parameter name")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"value": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Value. Fields of multi-valued parameters are separated by spaces, such as `32768 60999`.",
				Validators:          []validator.String{singleLine},
			},
			"file": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(defaultSysctlFile),
				MarkdownDescription: "File persisting the parameter. Defaults to `" + defaultSysctlFile + "`.",
				Validators:          []validator.String{pathValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
		},
	}
}

func (r *sysctlResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan sysctlModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !r.set(ctx, &plan, resp.Diagnostics.AddError) {
		return
	}
	plan.ID = plan.Name
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *sysctlResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state sysctlModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.File.IsNull() {
		state.File = types.StringValue(defaultSysctlFile)
	}
	content, err := r.client.ReadFile(ctx, state.File.ValueString())
	if system.IsNotExist(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read sysctl file", err.Error())
		return
	}
	// Without its line, the parameter would be lost on reboot: set it again.
	if !state.line().Present(string(content)) {
		resp.State.RemoveResource(ctx)
		return
	}
	current, err := r.client.Sysctl(ctx, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read kernel parameter", err.Error())
		return
	}
	if current != system.NormalizeSysctl(state.Value.ValueString()) {
		state.Value = types.StringValue(current)
	}
	state.ID = state.Name
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *sysctlResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan sysctlModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !r.set(ctx, &plan, resp.Diagnostics.AddError) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *sysctlResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state sysctlModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	match := sysctlLine(state.Name.ValueString())
	_, err := r.client.EditFile(ctx, state.File.ValueString(), nil, func(content []byte) ([]byte, error) {
		return []byte(textedit.RemoveLines(string(content), match.MatchString)), nil
	})
	if err != nil && !system.IsNotExist(err) {
		resp.Diagnostics.AddError("Cannot remove kernel parameter", err.Error())
	}
}

func (r *sysctlResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	value, err := r.client.Sysctl(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read kernel parameter", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("value"), value)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("file"), defaultSysctlFile)...)
}

// set applies the parameter, then persists it.
func (r *sysctlResource) set(ctx context.Context, m *sysctlModel, addError func(string, string)) bool {
	if err := r.client.SetSysctl(ctx, m.Name.ValueString(), m.Value.ValueString()); err != nil {
		addError("Cannot set kernel parameter", err.Error())
		return false
	}
	line := m.line()
	_, err := r.client.EditFile(ctx, m.File.ValueString(), &system.Attributes{Mode: "0644"}, func(content []byte) ([]byte, error) {
		return []byte(line.Ensure(string(content))), nil
	})
	if err != nil {
		addError("Cannot persist kernel parameter", err.Error())
		return false
	}
	return true
}
