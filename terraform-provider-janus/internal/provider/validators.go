package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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
