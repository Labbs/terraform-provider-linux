package provider

import (
	"bytes"
	"context"
	"path"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"golang.org/x/crypto/ssh"

	"github.com/labbs/terraform-provider-linux/internal/system"
	"github.com/labbs/terraform-provider-linux/internal/textedit"
)

var _ resource.ResourceWithConfigure = (*sshAuthorizedKeyResource)(nil)

// NewSSHAuthorizedKeyResource returns the linux_ssh_authorized_key resource.
func NewSSHAuthorizedKeyResource() resource.Resource {
	return &sshAuthorizedKeyResource{}
}

type sshAuthorizedKeyResource struct {
	resourceBase
}

type sshAuthorizedKeyModel struct {
	ID   types.String `tfsdk:"id"`
	User types.String `tfsdk:"user"`
	Key  types.String `tfsdk:"key"`
	Path types.String `tfsdk:"path"`
}

// keyBlob returns the public key of an authorized_keys line, without its
// options and comment, or nil.
func keyBlob(line string) []byte {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return nil
	}
	return key.Marshal()
}

// sameKey returns a predicate matching the lines holding the key of line.
func sameKey(line string) func(string) bool {
	blob := keyBlob(line)
	return func(other string) bool {
		b := keyBlob(other)
		return b != nil && bytes.Equal(b, blob)
	}
}

func (r *sshAuthorizedKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_authorized_key"
}

func (r *sshAuthorizedKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Authorizes a public key to log in as a user, in `~/.ssh/authorized_keys`, like Ansible's `authorized_key`. " +
			"The `.ssh` directory and the file are created with the right owner and permissions when needed. Other keys are left untouched.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "User and fingerprint of the key.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"user": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "User the key logs in as.",
				Validators:          []validator.String{accountNameValidator},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"key": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Public key, as a line of `authorized_keys`: optional options, key type, base64 key and optional comment, " +
					"such as `ssh-ed25519 AAAA... alice@laptop`.",
				Validators:    []validator.String{authorizedKeyValidator{}},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Path of the `authorized_keys` file.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *sshAuthorizedKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan sshAuthorizedKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	user, err := r.client.User(ctx, plan.User.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot find user", err.Error())
		return
	}
	owner := system.Attributes{Owner: user.UID, Group: user.GID}
	dir := path.Join(user.Home, ".ssh")
	dirAttrs, fileAttrs := owner, owner
	dirAttrs.Mode, fileAttrs.Mode = "0700", "0600"
	if err := r.client.Mkdir(ctx, dir, dirAttrs, false); err != nil {
		resp.Diagnostics.AddError("Cannot create .ssh directory", err.Error())
		return
	}

	file := path.Join(dir, "authorized_keys")
	key := strings.TrimSpace(plan.Key.ValueString())
	matches := sameKey(key)
	_, err = r.client.EditFile(ctx, file, &fileAttrs, func(content []byte) ([]byte, error) {
		// Replace the line holding the same key, to update its options.
		text := textedit.RemoveLines(string(content), matches)
		return []byte(textedit.Line{Line: key}.Ensure(text)), nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot add key", err.Error())
		return
	}

	pub, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(key))
	plan.ID = types.StringValue(plan.User.ValueString() + ":" + ssh.FingerprintSHA256(pub))
	plan.Path = types.StringValue(file)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *sshAuthorizedKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state sshAuthorizedKeyModel
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
		resp.Diagnostics.AddError("Cannot read authorized keys", err.Error())
		return
	}
	matches := sameKey(state.Key.ValueString())
	found := ""
	for _, line := range strings.Split(string(content), "\n") {
		if matches(line) {
			found = strings.TrimSpace(line)
			break
		}
	}
	if found == "" {
		resp.State.RemoveResource(ctx)
		return
	}
	if found != strings.TrimSpace(state.Key.ValueString()) {
		state.Key = types.StringValue(found)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *sshAuthorizedKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Every attribute but the computed ones requires a replacement.
	var plan sshAuthorizedKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *sshAuthorizedKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state sshAuthorizedKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	matches := sameKey(state.Key.ValueString())
	_, err := r.client.EditFile(ctx, state.Path.ValueString(), nil, func(content []byte) ([]byte, error) {
		return []byte(textedit.RemoveLines(string(content), matches)), nil
	})
	if err != nil && !system.IsNotExist(err) {
		resp.Diagnostics.AddError("Cannot remove key", err.Error())
	}
}

// authorizedKeyValidator checks a line of authorized_keys.
type authorizedKeyValidator struct{}

func (authorizedKeyValidator) Description(context.Context) string {
	return "must be a public key in authorized_keys format"
}

func (v authorizedKeyValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (authorizedKeyValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	key := strings.TrimSpace(req.ConfigValue.ValueString())
	if strings.Contains(key, "\n") {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid public key", "The key must fit on a single line.")
		return
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key)); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid public key", err.Error())
	}
}
