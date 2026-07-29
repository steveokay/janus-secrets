package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/steveokay/janus-secrets/terraform-provider-janus/internal/client"
)

// oneOfValidator rejects a string attribute whose value is not in the allowed
// set. It is hand-rolled rather than pulled from
// terraform-plugin-framework-validators so the provider keeps its dependency
// set to terraform-plugin-framework alone.
//
// Attribute validators run during config validation — i.e. at `terraform
// plan`, BEFORE any API call — so a typo'd enum fails fast and locally instead
// of round-tripping to Janus for a 400.
type oneOfValidator struct {
	allowed []string
}

// stringOneOf builds a validator restricting an attribute to the given values.
func stringOneOf(allowed ...string) validator.String {
	return oneOfValidator{allowed: allowed}
}

func (v oneOfValidator) Description(_ context.Context) string {
	return fmt.Sprintf("value must be one of: %s", strings.Join(quoteAll(v.allowed), ", "))
}

func (v oneOfValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v oneOfValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	// Null/unknown are someone else's problem (Required, or resolved later).
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	got := req.ConfigValue.ValueString()
	for _, a := range v.allowed {
		if got == a {
			return
		}
	}
	resp.Diagnostics.AddAttributeError(
		req.Path,
		"Invalid attribute value",
		fmt.Sprintf("Attribute %s %s, got: %q.", req.Path, v.Description(ctx), got),
	)
}

func quoteAll(vals []string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		out = append(out, fmt.Sprintf("%q", v))
	}
	return out
}

// groupRoleValidator restricts a group binding's role to viewer/developer/admin
// and gives `owner` its own explanation.
//
// "value must be one of …" would be true but unhelpful for owner: owner is a
// perfectly valid Janus role, just never one a GROUP may hold, and a
// practitioner who typed it deserves to know why rather than assume a typo. Like
// oneOfValidator this runs at `terraform plan`, before any API call.
type groupRoleValidator struct{}

// stringGroupRole builds the group-binding role validator.
func stringGroupRole() validator.String { return groupRoleValidator{} }

func (groupRoleValidator) Description(_ context.Context) string {
	return `value must be one of: "viewer", "developer", "admin" (never "owner")`
}

func (v groupRoleValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v groupRoleValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	got := req.ConfigValue.ValueString()
	if isOneOf(got, client.GroupRoleViewer, client.GroupRoleDeveloper, client.GroupRoleAdmin) {
		return
	}
	if got == client.GroupRoleOwner {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"A group binding can never be owner",
			"Owner rotates the master key, prunes the audit chain and hard-destroys secret history. Deriving it from "+
				"group membership would hand that tier to whoever administers the identity provider, whose membership "+
				"list Janus cannot authoritatively enumerate — and it would break the never-lock-out guard, which relies "+
				"on every instance owner being a direct binding.\n\n"+
				"Both the API and a database CHECK constraint refuse it. Use \"admin\" here, and bind an owner directly "+
				"to a person.",
		)
		return
	}
	resp.Diagnostics.AddAttributeError(
		req.Path,
		"Invalid attribute value",
		fmt.Sprintf("Attribute %s %s, got: %q.", req.Path, v.Description(ctx), got),
	)
}

// isOneOf is the runtime twin of the validator, used as a belt-and-braces check
// inside Create so an invalid value can never reach the API even if the
// framework validation path is bypassed (e.g. a direct SDK caller).
func isOneOf(got string, allowed ...string) bool {
	for _, a := range allowed {
		if got == a {
			return true
		}
	}
	return false
}
