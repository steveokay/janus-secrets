// Package provider implements the terraform-provider-janus Terraform provider
// using the terraform-plugin-framework. It manages Janus resources (projects,
// environments, configs, secrets, service tokens) declaratively over the Janus
// /v1 REST API. Secret values and minted tokens are marked Sensitive.
package provider

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/steveokay/janus-secrets/terraform-provider-janus/internal/client"
)

// defaultHTTPTimeout bounds every request the provider makes to Janus.
const defaultHTTPTimeout = 30 * time.Second

// Ensure janusProvider satisfies the provider.Provider interface.
var _ provider.Provider = (*janusProvider)(nil)

type janusProvider struct {
	version string
}

// New returns a provider factory for the given build version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &janusProvider{version: version}
	}
}

// providerModel maps the provider configuration block.
type providerModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	Token    types.String `tfsdk:"token"`
}

func (p *janusProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "janus"
	resp.Version = p.version
}

func (p *janusProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage Janus secrets-manager resources declaratively. " +
			"Configure with a `janus_svc_`/admin service token; both attributes may " +
			"also be supplied via the `JANUS_ADDR` and `JANUS_TOKEN` environment variables.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Janus base URL, e.g. `https://janus.example.com`. " +
					"Falls back to the `JANUS_ADDR` environment variable.",
				Optional: true,
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "Janus service token (`janus_svc_...`) or admin token. " +
					"Falls back to the `JANUS_TOKEN` environment variable.",
				Optional:  true,
				Sensitive: true,
			},
		},
	}
}

func (p *janusProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := os.Getenv("JANUS_ADDR")
	if !cfg.Endpoint.IsNull() && cfg.Endpoint.ValueString() != "" {
		endpoint = cfg.Endpoint.ValueString()
	}
	token := os.Getenv("JANUS_TOKEN")
	if !cfg.Token.IsNull() && cfg.Token.ValueString() != "" {
		token = cfg.Token.ValueString()
	}

	if endpoint == "" {
		resp.Diagnostics.AddAttributeError(
			pathRoot("endpoint"),
			"Missing Janus endpoint",
			"Set the provider `endpoint` attribute or the JANUS_ADDR environment variable.",
		)
	}
	if token == "" {
		resp.Diagnostics.AddAttributeError(
			pathRoot("token"),
			"Missing Janus token",
			"Set the provider `token` attribute or the JANUS_TOKEN environment variable.",
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	httpClient := &http.Client{Timeout: defaultHTTPTimeout}
	c, err := client.New(endpoint, token, httpClient)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Janus client", err.Error())
		return
	}

	resp.DataSourceData = c
	resp.ResourceData = c
}

func (p *janusProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewProjectResource,
		NewEnvironmentResource,
		NewConfigResource,
		NewSecretResource,
		NewServiceTokenResource,
	}
}

func (p *janusProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewSecretDataSource,
		NewConfigDataSource,
	}
}
