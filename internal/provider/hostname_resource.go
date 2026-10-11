package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.ResourceWithConfigure = (*hostnameResource)(nil)

// NewHostnameResource returns the linux_hostname resource.
func NewHostnameResource() resource.Resource {
	return &hostnameResource{}
}

type hostnameResource struct {
	resourceBase
}

type hostnameModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

func (r *hostnameResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_hostname"
}

func (r *hostnameResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Sets the host name, now and after reboots, with `hostnamectl` when available and `/etc/hostname` otherwise. " +
			"`/etc/hosts` is not changed. Destroying the resource leaves the host name as it is.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Always `hostname`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Host name, such as `web-1` or `web-1.example.com`.",
				Validators: []validator.String{stringvalidator.RegexMatches(
					regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,62})(\.[A-Za-z0-9]([A-Za-z0-9-]{0,62}))*$`), "must be a valid host name")},
			},
		},
	}
}

func (r *hostnameResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan hostnameModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.SetHostname(ctx, plan.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Cannot set host name", err.Error())
		return
	}
	plan.ID = types.StringValue("hostname")
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *hostnameResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state hostnameModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, persisted, err := r.client.Hostname(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read host name", err.Error())
		return
	}
	switch name := state.Name.ValueString(); {
	case current != name:
		state.Name = types.StringValue(current)
	case persisted != "" && persisted != name:
		// The name would change on the next reboot.
		state.Name = types.StringValue(persisted)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *hostnameResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan hostnameModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.SetHostname(ctx, plan.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Cannot set host name", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *hostnameResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
	// A host always has a name: it is left as it is.
}
