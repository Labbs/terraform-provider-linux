package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

var _ resource.ResourceWithConfigure = (*sedResource)(nil)

// NewSedResource returns the linux_sed resource.
func NewSedResource() resource.Resource {
	return &sedResource{}
}

type sedResource struct {
	resourceBase
}

type sedModel struct {
	ID          types.String `tfsdk:"id"`
	Path        types.String `tfsdk:"path"`
	Expressions types.List   `tfsdk:"expressions"`
	Extended    types.Bool   `tfsdk:"extended"`
}

func (r *sedResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sed"
}

func (r *sedResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Edits an existing file in place with `sed` expressions. " +
			"When running the expressions would change the file again, Terraform plans to apply them again: " +
			"write idempotent expressions, such as `s/^#?Port .*/Port 2222/`, rather than `s/a/aa/`. " +
			"Destroying the resource leaves the file as it is.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Path of the file.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Absolute path of the file to edit.",
				Validators:          []validator.String{pathValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"expressions": schema.ListAttribute{
				Required:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "`sed` expressions, each passed with `-e`, such as `s/^#?PermitRootLogin .*/PermitRootLogin no/`.",
				Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
			},
			"extended": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Use extended regular expressions (`sed -E`). Defaults to `false`.",
			},
		},
	}
}

func (r *sedResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan sedModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, resp.Diagnostics.AddError, resp.Diagnostics.AddWarning)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = plan.Path
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *sedResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state sedModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var expressions []string
	resp.Diagnostics.Append(state.Expressions.ElementsAs(ctx, &expressions, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	pending, err := r.client.SedPending(ctx, state.Path.ValueString(), expressions, state.Extended.ValueBool())
	if system.IsNotExist(err) || pending {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot check file", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *sedResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan sedModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, resp.Diagnostics.AddError, resp.Diagnostics.AddWarning)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *sedResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
	// sed edits cannot be undone: the file is left as it is.
}

func (r *sedResource) apply(ctx context.Context, m *sedModel, addError, addWarning func(string, string)) {
	var expressions []string
	if diags := m.Expressions.ElementsAs(ctx, &expressions, false); diags.HasError() {
		addError("Invalid expressions", fmt.Sprint(diags))
		return
	}
	file := m.Path.ValueString()
	res, err := r.client.Sed(ctx, file, expressions, m.Extended.ValueBool())
	if err != nil {
		addError("Cannot edit file", err.Error())
		return
	}
	if !res.Idempotent {
		addWarning("Expressions are not idempotent",
			fmt.Sprintf("Running the expressions again would change %s again, so Terraform will apply them on every run. Rewrite them so that they leave an edited file unchanged.", file))
	}
}
