package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
	"github.com/labbs/terraform-provider-linux/internal/textedit"
)

var _ resource.ResourceWithConfigure = (*fileLineResource)(nil)

// NewFileLineResource returns the linux_file_line resource.
func NewFileLineResource() resource.Resource {
	return &fileLineResource{}
}

type fileLineResource struct {
	resourceBase
}

type fileLineModel struct {
	ID              types.String `tfsdk:"id"`
	Path            types.String `tfsdk:"path"`
	Line            types.String `tfsdk:"line"`
	Match           types.String `tfsdk:"match"`
	InsertAfter     types.String `tfsdk:"insert_after"`
	InsertBefore    types.String `tfsdk:"insert_before"`
	Create          types.Bool   `tfsdk:"create"`
	RemoveOnDestroy types.Bool   `tfsdk:"remove_on_destroy"`
}

func (m *fileLineModel) spec() textedit.Line {
	return textedit.Line{
		Line:  m.Line.ValueString(),
		Match: optionalRegexp(m.Match),
		Position: textedit.Position{
			After:  optionalRegexp(m.InsertAfter),
			Before: optionalRegexp(m.InsertBefore),
		},
	}
}

// lineValue returns the line of m, or "" when m is nil.
func (m *fileLineModel) lineValue() string {
	if m == nil {
		return ""
	}
	return m.Line.ValueString()
}

// insertPositionAttributes are the attributes telling where to insert new
// lines, shared with linux_file_block.
func insertPositionAttributes(what string) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"insert_after": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Regular expression: insert the " + what + " after the last line matching it. Without a match, or when unset, the " + what + " goes at the end of the file.",
			Validators: []validator.String{
				regexValidator{},
				stringvalidator.ConflictsWith(path.MatchRoot("insert_before")),
			},
		},
		"insert_before": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Regular expression: insert the " + what + " before the first line matching it. Use `.*` for the beginning of the file.",
			Validators:          []validator.String{regexValidator{}},
		},
		"create": schema.BoolAttribute{
			Optional:            true,
			Computed:            true,
			Default:             booldefault.StaticBool(false),
			MarkdownDescription: "Create the file, with mode `0644`, when it does not exist. Otherwise a missing file is an error. Defaults to `false`.",
		},
	}
}

func (r *fileLineResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_file_line"
}

func (r *fileLineResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
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
		"line": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Line that must be in the file.",
			Validators: []validator.String{
				stringvalidator.RegexMatches(regexp.MustCompile(`^[^\n]*$`), "must be a single line"),
			},
		},
		"match": schema.StringAttribute{
			Optional: true,
			MarkdownDescription: "Regular expression selecting the line to replace with `line`: the last line matching it. " +
				"It should match `line` too, such as `^#?PermitRootLogin ` for `PermitRootLogin no`, so that the line is found again on the next run.",
			Validators: []validator.String{regexValidator{}},
		},
		"remove_on_destroy": schema.BoolAttribute{
			Optional:            true,
			Computed:            true,
			Default:             booldefault.StaticBool(true),
			MarkdownDescription: "Remove `line` from the file when the resource is destroyed. A line it replaced is not restored. Defaults to `true`.",
		},
	}
	for name, attribute := range insertPositionAttributes("line") {
		attributes[name] = attribute
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Ensures a line is in a file, like Ansible's `lineinfile`: the last line matching `match` is replaced, " +
			"or the line is inserted. Other lines are left untouched. When the line is no longer there, Terraform plans to add it again.",
		Attributes: attributes,
	}
}

func (r *fileLineResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan fileLineModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.ensure(ctx, &plan, nil, resp.Diagnostics.AddError) {
		return
	}
	plan.ID = types.StringValue(randomID())
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *fileLineResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state fileLineModel
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
	if !state.spec().Present(string(content)) {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *fileLineResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state fileLineModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.ensure(ctx, &plan, &state, resp.Diagnostics.AddError) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *fileLineResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state fileLineModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !state.RemoveOnDestroy.ValueBool() {
		return
	}
	spec := state.spec()
	_, err := r.client.EditFile(ctx, state.Path.ValueString(), nil, func(content []byte) ([]byte, error) {
		return []byte(spec.Remove(string(content))), nil
	})
	if err != nil && !system.IsNotExist(err) {
		resp.Diagnostics.AddError("Cannot remove line", err.Error())
	}
}

// ensure puts the line in the file. When the line changes, the previous
// one, from prior, is removed first.
func (r *fileLineResource) ensure(ctx context.Context, plan, prior *fileLineModel, addError func(string, string)) bool {
	var create *system.Attributes
	if plan.Create.ValueBool() {
		create = &system.Attributes{Mode: "0644"}
	}
	spec := plan.spec()
	_, err := r.client.EditFile(ctx, plan.Path.ValueString(), create, func(content []byte) ([]byte, error) {
		text := string(content)
		// A previous line that match selects is replaced in place by Ensure;
		// any other one is removed.
		if previous := prior.lineValue(); previous != "" && previous != spec.Line &&
			(spec.Match == nil || !spec.Match.MatchString(previous)) {
			text = prior.spec().Remove(text)
		}
		return []byte(spec.Ensure(text)), nil
	})
	if err != nil {
		addError("Cannot edit file", err.Error())
		return false
	}
	return true
}
