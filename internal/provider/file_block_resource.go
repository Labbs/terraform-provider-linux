package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

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
	_ resource.ResourceWithConfigure      = (*fileBlockResource)(nil)
	_ resource.ResourceWithValidateConfig = (*fileBlockResource)(nil)
)

const defaultBlockMarker = "# {mark} TERRAFORM MANAGED BLOCK"

var regexpMustContainMark = regexp.MustCompile(`^[^\n]*\{mark\}[^\n]*$`)

// NewFileBlockResource returns the linux_file_block resource.
func NewFileBlockResource() resource.Resource {
	return &fileBlockResource{}
}

type fileBlockResource struct {
	resourceBase
}

type fileBlockModel struct {
	ID           types.String `tfsdk:"id"`
	Path         types.String `tfsdk:"path"`
	Content      types.String `tfsdk:"content"`
	Marker       types.String `tfsdk:"marker"`
	InsertAfter  types.String `tfsdk:"insert_after"`
	InsertBefore types.String `tfsdk:"insert_before"`
	Create       types.Bool   `tfsdk:"create"`
}

func (m *fileBlockModel) spec() textedit.Block {
	marker := m.Marker.ValueString()
	return textedit.Block{
		Begin:   strings.ReplaceAll(marker, "{mark}", "BEGIN"),
		End:     strings.ReplaceAll(marker, "{mark}", "END"),
		Content: m.Content.ValueString(),
		Position: textedit.Position{
			After:  optionalRegexp(m.InsertAfter),
			Before: optionalRegexp(m.InsertBefore),
		},
	}
}

func (r *fileBlockResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_file_block"
}

func (r *fileBlockResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attributes := map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Random identifier.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"path": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Absolute path of the file.",
			Validators:          []validator.String{pathValidator{}},
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"content": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Lines kept between the markers.",
		},
		"marker": schema.StringAttribute{
			Optional: true,
			Computed: true,
			Default:  stringdefault.StaticString(defaultBlockMarker),
			MarkdownDescription: "Marker line, where `{mark}` becomes `BEGIN` and `END`. Give each block of a file its own marker, such as `# {mark} nginx upstreams`. " +
				"Defaults to `" + defaultBlockMarker + "`.",
			Validators: []validator.String{
				stringvalidator.RegexMatches(regexpMustContainMark, "must contain {mark} and fit on a single line"),
			},
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
	}
	for name, attribute := range insertPositionAttributes("block") {
		attributes[name] = attribute
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a block of lines between two marker lines in a file, like Ansible's `blockinfile`. " +
			"The rest of the file is left untouched. Changes made to the block outside of Terraform show up in the plan. " +
			"Destroying the resource removes the block and its markers.",
		Attributes: attributes,
	}
}

func (r *fileBlockResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg fileBlockModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.Content.IsUnknown() || cfg.Marker.IsUnknown() {
		return
	}
	if cfg.Marker.IsNull() {
		cfg.Marker = types.StringValue(defaultBlockMarker)
	}
	spec := cfg.spec()
	for _, line := range strings.Split(cfg.Content.ValueString(), "\n") {
		if line == spec.Begin || line == spec.End {
			resp.Diagnostics.AddAttributeError(path.Root("content"), "Invalid content",
				fmt.Sprintf("The content must not contain the marker line %q.", line))
			return
		}
	}
}

func (r *fileBlockResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan fileBlockModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !r.ensure(ctx, &plan, resp.Diagnostics.AddError) {
		return
	}
	plan.ID = types.StringValue(randomID())
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *fileBlockResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state fileBlockModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	content, err := r.client.ReadFile(ctx, state.Path.ValueString())
	if system.IsNotExist(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read file", err.Error())
		return
	}
	inner, ok := state.spec().Find(string(content))
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	if !textedit.SameText(inner, state.Content.ValueString()) {
		state.Content = types.StringValue(inner)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *fileBlockResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan fileBlockModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !r.ensure(ctx, &plan, resp.Diagnostics.AddError) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *fileBlockResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state fileBlockModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	spec := state.spec()
	_, err := r.client.EditFile(ctx, state.Path.ValueString(), nil, func(content []byte) ([]byte, error) {
		return []byte(spec.Remove(string(content))), nil
	})
	if err != nil && !system.IsNotExist(err) {
		resp.Diagnostics.AddError("Cannot remove block", err.Error())
	}
}

func (r *fileBlockResource) ensure(ctx context.Context, plan *fileBlockModel, addError func(string, string)) bool {
	var create *system.Attributes
	if plan.Create.ValueBool() {
		create = &system.Attributes{Mode: "0644"}
	}
	spec := plan.spec()
	_, err := r.client.EditFile(ctx, plan.Path.ValueString(), create, func(content []byte) ([]byte, error) {
		return []byte(spec.Ensure(string(content))), nil
	})
	if err != nil {
		addError("Cannot edit file", err.Error())
		return false
	}
	return true
}
