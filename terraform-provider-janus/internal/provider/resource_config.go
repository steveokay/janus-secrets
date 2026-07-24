package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/steveokay/janus-secrets/terraform-provider-janus/internal/client"
)

var (
	_ resource.Resource                = (*configResource)(nil)
	_ resource.ResourceWithConfigure   = (*configResource)(nil)
	_ resource.ResourceWithImportState = (*configResource)(nil)
)

// NewConfigResource is the janus_config resource factory.
func NewConfigResource() resource.Resource { return &configResource{} }

type configResource struct {
	client *client.Client
}

type configModel struct {
	ID            types.String `tfsdk:"id"`
	ProjectID     types.String `tfsdk:"project_id"`
	EnvironmentID types.String `tfsdk:"environment_id"`
	Name          types.String `tfsdk:"name"`
	InheritsFrom  types.String `tfsdk:"inherits_from"`
}

func (r *configResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_config"
}

func (r *configResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A config within an environment. A config holds a set of secrets and can inherit from a base config in the same environment.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Config UUID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "Parent project UUID (used to address the create route). Changing it forces replacement.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"environment_id": schema.StringAttribute{
				MarkdownDescription: "Parent environment UUID. Changing it forces replacement.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Config name (e.g. `prod`). Changing it forces replacement.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"inherits_from": schema.StringAttribute{
				MarkdownDescription: "Optional base config UUID (same environment) to inherit from. Changing it forces replacement.",
				Optional:            true,
				PlanModifiers:       replace,
			},
		},
	}
}

func (r *configResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *configResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan configModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var inherits *string
	if !plan.InheritsFrom.IsNull() && plan.InheritsFrom.ValueString() != "" {
		v := plan.InheritsFrom.ValueString()
		inherits = &v
	}
	cfg, err := r.client.CreateConfig(ctx, plan.ProjectID.ValueString(), plan.EnvironmentID.ValueString(), plan.Name.ValueString(), inherits)
	if err != nil {
		apiErrorToDiag(&resp.Diagnostics, "Unable to create config", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, configToModel(cfg, plan.ProjectID.ValueString()))...)
}

func (r *configResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state configModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cfg, err := r.client.GetConfig(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		apiErrorToDiag(&resp.Diagnostics, "Unable to read config", err)
		return
	}
	// project_id is not returned by the config route; preserve it from state.
	resp.Diagnostics.Append(resp.State.Set(ctx, configToModel(cfg, state.ProjectID.ValueString()))...)
}

func (r *configResource) Update(ctx context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	// All mutable-by-plan attributes force replacement, so Update is a no-op.
	resp.Diagnostics.AddError(
		"Update not supported",
		"janus_config attributes are immutable; changes force replacement. This is a provider bug if reached.",
	)
}

func (r *configResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state configModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteConfig(ctx, state.ID.ValueString(), false); err != nil {
		if client.IsNotFound(err) {
			return
		}
		apiErrorToDiag(&resp.Diagnostics, "Unable to delete config", err)
	}
}

func (r *configResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Config routes are addressed by cid alone; project_id is refreshed lazily
	// on the next plan (it is only used for the create route).
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func configToModel(c *client.Config, projectID string) configModel {
	m := configModel{
		ID:            types.StringValue(c.ID),
		EnvironmentID: types.StringValue(c.EnvironmentID),
		Name:          types.StringValue(c.Name),
		InheritsFrom:  types.StringNull(),
	}
	if projectID != "" {
		m.ProjectID = types.StringValue(projectID)
	} else {
		m.ProjectID = types.StringNull()
	}
	if c.InheritsFrom != nil && *c.InheritsFrom != "" {
		m.InheritsFrom = types.StringValue(*c.InheritsFrom)
	}
	return m
}
