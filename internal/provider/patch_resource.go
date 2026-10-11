package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/patch"
	"github.com/labbs/terraform-provider-linux/internal/system"
)

var (
	_ resource.ResourceWithConfigure      = (*patchResource)(nil)
	_ resource.ResourceWithValidateConfig = (*patchResource)(nil)
)

// NewPatchResource returns the linux_patch resource.
func NewPatchResource() resource.Resource {
	return &patchResource{}
}

type patchResource struct {
	resourceBase
}

type patchModel struct {
	ID              types.String `tfsdk:"id"`
	Path            types.String `tfsdk:"path"`
	Patch           types.String `tfsdk:"patch"`
	RevertOnDestroy types.Bool   `tfsdk:"revert_on_destroy"`
}

func (r *patchResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_patch"
}

func (r *patchResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Applies a unified diff to an existing file, and reverts it on destroy. " +
			"The diff is applied by the provider, so the host does not need `patch`. Hunks must match exactly, " +
			"but may have moved, as with `patch`. When the file no longer contains the change, Terraform plans to apply it again.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Path of the file and digest of the diff.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Absolute path of the file to patch. File names in the diff headers are ignored.",
				Validators:          []validator.String{pathValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"patch": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Unified diff (`diff -u`, `git diff`) touching a single file.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"revert_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Revert the patch when the resource is destroyed. Defaults to `true`.",
			},
		},
	}
}

func (r *patchResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var diff types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("patch"), &diff)...)
	if diff.IsNull() || diff.IsUnknown() {
		return
	}
	if _, err := patch.Parse(diff.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("patch"), "Invalid patch", err.Error())
	}
}

func (r *patchResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan patchModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := patch.Parse(plan.Patch.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("patch"), "Invalid patch", err.Error())
		return
	}
	file := plan.Path.ValueString()
	_, err = r.client.EditFile(ctx, file, nil, func(content []byte) ([]byte, error) {
		switch patch.Check(string(content), p) {
		case patch.Applied:
			return content, nil
		case patch.Applicable:
			patched, err := patch.Apply(string(content), p, false)
			return []byte(patched), err
		default:
			_, err := patch.Apply(string(content), p, false)
			return nil, fmt.Errorf("the patch can neither be applied nor is it already applied: %w", err)
		}
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot apply patch", err.Error())
		return
	}

	plan.ID = types.StringValue(file + ":" + sha256Hex([]byte(plan.Patch.ValueString()))[:12])
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *patchResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state patchModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := patch.Parse(state.Patch.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid patch in state", err.Error())
		return
	}
	content, err := r.client.ReadFile(ctx, state.Path.ValueString())
	if system.IsNotExist(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read patched file", err.Error())
		return
	}
	if patch.Check(string(content), p) != patch.Applied {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *patchResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Only revert_on_destroy can change without replacing the resource.
	var plan patchModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *patchResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state patchModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !state.RevertOnDestroy.ValueBool() {
		return
	}
	p, err := patch.Parse(state.Patch.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid patch in state", err.Error())
		return
	}
	file := state.Path.ValueString()
	reverted := false
	_, err = r.client.EditFile(ctx, file, nil, func(content []byte) ([]byte, error) {
		if patch.Check(string(content), p) != patch.Applied {
			return content, nil
		}
		reverted = true
		original, err := patch.Apply(string(content), p, true)
		return []byte(original), err
	})
	switch {
	case system.IsNotExist(err):
	case err != nil:
		resp.Diagnostics.AddError("Cannot revert patch", err.Error())
	case !reverted:
		resp.Diagnostics.AddWarning("Patch not reverted",
			fmt.Sprintf("%s no longer contains the patch, it was left untouched.", file))
	}
}
