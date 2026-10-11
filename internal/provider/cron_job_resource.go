package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/textedit"
)

var _ resource.ResourceWithConfigure = (*cronJobResource)(nil)

var singleLine = stringvalidator.RegexMatches(regexp.MustCompile(`^[^\n]+$`), "must be a single, non-empty line")

// NewCronJobResource returns the linux_cron_job resource.
func NewCronJobResource() resource.Resource {
	return &cronJobResource{}
}

type cronJobResource struct {
	resourceBase
}

type cronJobModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	User     types.String `tfsdk:"user"`
	Schedule types.String `tfsdk:"schedule"`
	Command  types.String `tfsdk:"command"`
	Disabled types.Bool   `tfsdk:"disabled"`
}

func (m *cronJobModel) job() textedit.CronJob {
	schedule, _ := textedit.ParseSchedule(m.Schedule.ValueString())
	return textedit.CronJob{
		Name:     m.Name.ValueString(),
		Schedule: schedule,
		Command:  m.Command.ValueString(),
		Disabled: m.Disabled.ValueBool(),
	}
}

func (r *cronJobResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cron_job"
}

func (r *cronJobResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a job in the crontab of a user, like Ansible's `cron`. The job is tagged with a `# Terraform: <name>` comment, " +
			"and the other entries of the crontab are left untouched. Requires `crontab` on the host.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "User and name of the job.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name identifying the job in the crontab.",
				Validators:          []validator.String{singleLine},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"user": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "User whose crontab holds the job. Defaults to the user running the commands.",
				Validators:          []validator.String{accountNameValidator},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"schedule": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "When the job runs: five fields, such as `*/15 * * * *`, or a nickname such as `@daily` or `@reboot`.",
				Validators:          []validator.String{scheduleValidator{}},
			},
			"command": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Command to run. In a crontab, `%` means a new line unless escaped as `\\%`.",
				Validators:          []validator.String{singleLine},
			},
			"disabled": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Keep the job commented out. Defaults to `false`.",
			},
		},
	}
}

func (r *cronJobResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan cronJobModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !r.set(ctx, &plan, resp.Diagnostics.AddError) {
		return
	}
	plan.ID = types.StringValue(plan.User.ValueString() + ":" + plan.Name.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *cronJobResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state cronJobModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	crontab, err := r.client.Crontab(ctx, state.User.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read crontab", err.Error())
		return
	}
	job, ok := textedit.FindCronJob(crontab, state.Name.ValueString())
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	if job.Schedule != state.job().Schedule {
		state.Schedule = types.StringValue(job.Schedule)
	}
	state.Command = types.StringValue(job.Command)
	state.Disabled = types.BoolValue(job.Disabled)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *cronJobResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan cronJobModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !r.set(ctx, &plan, resp.Diagnostics.AddError) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *cronJobResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state cronJobModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := state.Name.ValueString()
	_, err := r.client.EditCrontab(ctx, state.User.ValueString(), func(crontab string) (string, error) {
		return textedit.RemoveCronJob(crontab, name), nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot remove cron job", err.Error())
	}
}

func (r *cronJobResource) set(ctx context.Context, m *cronJobModel, addError func(string, string)) bool {
	job := m.job()
	_, err := r.client.EditCrontab(ctx, m.User.ValueString(), func(crontab string) (string, error) {
		return textedit.SetCronJob(crontab, job), nil
	})
	if err != nil {
		addError("Cannot write cron job", err.Error())
		return false
	}
	return true
}

// scheduleValidator checks a crontab schedule.
type scheduleValidator struct{}

func (scheduleValidator) Description(context.Context) string {
	return `must be five crontab fields, such as "*/15 * * * *", or a nickname such as "@daily"`
}

func (v scheduleValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (scheduleValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := textedit.ParseSchedule(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid schedule", fmt.Sprintf("%s.", err))
	}
}
