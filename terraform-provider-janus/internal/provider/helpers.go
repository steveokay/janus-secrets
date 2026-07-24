package provider

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"

	"github.com/steveokay/janus-secrets/terraform-provider-janus/internal/client"
)

// pathRoot is a tiny alias so provider.go can build attribute paths without
// importing the path package directly.
func pathRoot(name string) path.Path { return path.Root(name) }

// clientFromProviderData extracts the configured *client.Client that the
// provider stashed in ResourceData/DataSourceData. It tolerates a nil
// providerData (which occurs during early framework lifecycle phases).
func clientFromProviderData(providerData any, diags *diag.Diagnostics) *client.Client {
	if providerData == nil {
		return nil
	}
	c, ok := providerData.(*client.Client)
	if !ok {
		diags.AddError(
			"Unexpected provider data type",
			fmt.Sprintf("Expected *client.Client, got %T. This is a provider bug.", providerData),
		)
		return nil
	}
	return c
}

// apiErrorToDiag turns an error from the API client into a Terraform
// diagnostic. Janus error-envelope details (code + message) surface in the
// detail; the envelope is value-free by design so no secret leaks here.
func apiErrorToDiag(diags *diag.Diagnostics, summary string, err error) {
	var ae *client.APIError
	if ok := asAPIError(err, &ae); ok {
		diags.AddError(summary, fmt.Sprintf("%s (HTTP %d, code %q)", ae.Message, ae.Status, ae.Code))
		return
	}
	diags.AddError(summary, err.Error())
}

// asAPIError is a small errors.As shim kept here so callers don't import
// errors just for the assertion.
func asAPIError(err error, target **client.APIError) bool {
	for err != nil {
		if ae, ok := err.(*client.APIError); ok {
			*target = ae
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
