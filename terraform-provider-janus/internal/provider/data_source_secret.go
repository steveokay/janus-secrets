package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/steveokay/janus-secrets/terraform-provider-janus/internal/client"
)

var (
	_ datasource.DataSource              = (*secretDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*secretDataSource)(nil)
)

// NewSecretDataSource is the janus_secret data source factory.
func NewSecretDataSource() datasource.DataSource { return &secretDataSource{} }

type secretDataSource struct {
	client *client.Client
}

type secretDataSourceModel struct {
	ID       types.String `tfsdk:"id"`
	ConfigID types.String `tfsdk:"config_id"`
	Key      types.String `tfsdk:"key"`
	Value    types.String `tfsdk:"value"`
}

func (d *secretDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secret"
}

func (d *secretDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a single secret value from a Janus config. The read is audited server-side (`secret.reveal`). " +
			"The returned value is sensitive and lands in Terraform state.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Synthetic ID: `<config_id>/<key>`.",
				Computed:            true,
			},
			"config_id": schema.StringAttribute{
				MarkdownDescription: "Config UUID to read from.",
				Required:            true,
			},
			"key": schema.StringAttribute{
				MarkdownDescription: "Secret key to read.",
				Required:            true,
			},
			"value": schema.StringAttribute{
				MarkdownDescription: "The resolved secret value. Sensitive.",
				Computed:            true,
				Sensitive:           true,
			},
		},
	}
}

func (d *secretDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *secretDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg secretDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	value, err := d.client.GetSecret(ctx, cfg.ConfigID.ValueString(), cfg.Key.ValueString())
	if err != nil {
		apiErrorToDiag(&resp.Diagnostics, "Unable to read secret", err)
		return
	}
	cfg.Value = types.StringValue(value)
	cfg.ID = types.StringValue(secretID(cfg.ConfigID.ValueString(), cfg.Key.ValueString()))
	resp.Diagnostics.Append(resp.State.Set(ctx, cfg)...)
}
