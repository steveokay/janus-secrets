package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/steveokay/janus-secrets/terraform-provider-janus/internal/client"
)

var (
	_ datasource.DataSource              = (*configDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*configDataSource)(nil)
)

// NewConfigDataSource is the janus_config data source factory.
func NewConfigDataSource() datasource.DataSource { return &configDataSource{} }

type configDataSource struct {
	client *client.Client
}

type configDataSourceModel struct {
	ID            types.String `tfsdk:"id"`
	EnvironmentID types.String `tfsdk:"environment_id"`
	Name          types.String `tfsdk:"name"`
	InheritsFrom  types.String `tfsdk:"inherits_from"`
}

func (d *configDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_config"
}

func (d *configDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads metadata for a Janus config by ID (no secret values).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Config UUID to read.",
				Required:            true,
			},
			"environment_id": schema.StringAttribute{
				MarkdownDescription: "Parent environment UUID.",
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Config name.",
				Computed:            true,
			},
			"inherits_from": schema.StringAttribute{
				MarkdownDescription: "Base config UUID this config inherits from, if any.",
				Computed:            true,
			},
		},
	}
}

func (d *configDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *configDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg configDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := d.client.GetConfig(ctx, cfg.ID.ValueString())
	if err != nil {
		apiErrorToDiag(&resp.Diagnostics, "Unable to read config", err)
		return
	}
	cfg.EnvironmentID = types.StringValue(got.EnvironmentID)
	cfg.Name = types.StringValue(got.Name)
	if got.InheritsFrom != nil && *got.InheritsFrom != "" {
		cfg.InheritsFrom = types.StringValue(*got.InheritsFrom)
	} else {
		cfg.InheritsFrom = types.StringNull()
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, cfg)...)
}
