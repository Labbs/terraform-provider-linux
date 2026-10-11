package provider

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/labbs/terraform-provider-linux/internal/system"
	"github.com/labbs/terraform-provider-linux/internal/transport"
)

// clientFrom extracts the client the provider hands to its resources, data
// sources and actions. It returns nil when the provider is not configured
// yet, e.g. during validation.
func clientFrom(data any, diags *diag.Diagnostics) *system.Client {
	if data == nil {
		return nil
	}
	client, ok := data.(*system.Client)
	if !ok {
		diags.AddError("Unexpected provider data",
			fmt.Sprintf("Expected *system.Client, got %T. Please report this issue to the provider developers.", data))
		return nil
	}
	return client
}

type resourceBase struct {
	client *system.Client
}

func (b *resourceBase) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	b.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

type dataSourceBase struct {
	client *system.Client
}

func (b *dataSourceBase) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	b.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

type actionBase struct {
	client *system.Client
}

func (b *actionBase) Configure(_ context.Context, req action.ConfigureRequest, resp *action.ConfigureResponse) {
	b.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

// Validators shared by the schemas.
var (
	modeValidator = stringvalidator.RegexMatches(regexp.MustCompile(`^[0-7]{3,4}$`),
		`must be permission bits in octal, such as "644" or "0755"`)
	unitNameValidator = stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z0-9:_.@\\-]+$`),
		`must be a systemd unit name, such as "nginx.service"`)
	envNameValidator = stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`),
		"must be a valid environment variable name")
)

// pathValidator checks that a path is absolute and canonical.
type pathValidator struct{}

func (pathValidator) Description(context.Context) string {
	return "must be an absolute path in canonical form, other than /"
}

func (v pathValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (pathValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if err := system.CheckPath(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid path",
			fmt.Sprintf("%q %s.", req.ConfigValue.ValueString(), err))
	}
}

// durationValidator checks that a string is a Go duration.
type durationValidator struct{}

func (durationValidator) Description(context.Context) string {
	return `must be a duration such as "30s" or "2m"`
}

func (v durationValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (durationValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if d, err := time.ParseDuration(req.ConfigValue.ValueString()); err != nil || d <= 0 {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid duration",
			fmt.Sprintf(`%q is not a positive duration such as "30s" or "2m".`, req.ConfigValue.ValueString()))
	}
}

// base64Validator checks that a string is standard base64.
type base64Validator struct{}

func (base64Validator) Description(context.Context) string {
	return "must be base64-encoded"
}

func (v base64Validator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (base64Validator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := base64.StdEncoding.DecodeString(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid base64", err.Error())
	}
}

// regexValidator checks that a string is a valid regular expression.
type regexValidator struct{}

func (regexValidator) Description(context.Context) string {
	return "must be a valid regular expression (RE2 syntax)"
}

func (v regexValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (regexValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := regexp.Compile(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid regular expression", err.Error())
	}
}

// optionalRegexp compiles v, which was validated, or returns nil when v is
// null.
func optionalRegexp(v types.String) *regexp.Regexp {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	return regexp.MustCompile(v.ValueString())
}

// createdKey marks, in the private state, the resources that created what
// they manage: destroying them deletes it, while destroying a resource that
// adopted something that already existed leaves it in place.
const createdKey = "created"

type privateSetter interface {
	SetKey(ctx context.Context, key string, value []byte) diag.Diagnostics
}

type privateGetter interface {
	GetKey(ctx context.Context, key string) ([]byte, diag.Diagnostics)
}

func markCreated(ctx context.Context, private privateSetter, diags *diag.Diagnostics) {
	diags.Append(private.SetKey(ctx, createdKey, []byte("true"))...)
}

func wasCreated(ctx context.Context, private privateGetter, diags *diag.Diagnostics) bool {
	value, d := private.GetKey(ctx, createdKey)
	diags.Append(d...)
	return string(value) == "true"
}

// int64Value parses a numeric ID returned by the host.
func int64Value(s string) types.Int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return types.Int64Unknown()
	}
	return types.Int64Value(n)
}

// knownInt64 returns v as a string, or "" when it is null or unknown.
func knownInt64(v types.Int64) string {
	if v.IsNull() || v.IsUnknown() {
		return ""
	}
	return strconv.FormatInt(v.ValueInt64(), 10)
}

// modeValue returns the mode to store: prior when it denotes the actual
// mode, so that "644" does not show as a change to "0644".
func modeValue(prior types.String, actual string) types.String {
	if !prior.IsNull() && !prior.IsUnknown() {
		if mode, err := system.ParseMode(prior.ValueString()); err == nil && system.FormatMode(mode) == actual {
			return prior
		}
	}
	return types.StringValue(actual)
}

// ownerValue returns the owner or group to store: prior when it is the name
// or the ID of the actual one, the name otherwise.
func ownerValue(prior types.String, name, id string) types.String {
	if !prior.IsNull() && !prior.IsUnknown() && (prior.ValueString() == name || prior.ValueString() == id) {
		return prior
	}
	return types.StringValue(name)
}

// knownString returns the value of v, or "" when it is null or unknown.
func knownString(v types.String) string {
	if v.IsNull() || v.IsUnknown() {
		return ""
	}
	return v.ValueString()
}

// stringMap converts a map of strings, ignoring null.
func stringMap(ctx context.Context, m types.Map, diags *diag.Diagnostics) map[string]string {
	if m.IsNull() || m.IsUnknown() {
		return nil
	}
	out := make(map[string]string, len(m.Elements()))
	diags.Append(m.ElementsAs(ctx, &out, false)...)
	return out
}

// addCommandError reports a failed script, with its output.
func addCommandError(diags *diag.Diagnostics, summary string, res *transport.Result, err error) {
	detail := err.Error()
	if res != nil {
		if out := strings.TrimSpace(string(res.Stdout)); out != "" {
			detail += "\n\nstdout:\n" + out
		}
	}
	diags.AddError(summary, detail)
}

// sha256Hex returns the hex-encoded SHA-256 digest of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
