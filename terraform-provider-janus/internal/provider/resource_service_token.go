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
	_ resource.Resource                = (*serviceTokenResource)(nil)
	_ resource.ResourceWithConfigure   = (*serviceTokenResource)(nil)
	_ resource.ResourceWithImportState = (*serviceTokenResource)(nil)
)

// NewServiceTokenResource is the janus_service_token resource factory.
func NewServiceTokenResource() resource.Resource { return &serviceTokenResource{} }

type serviceTokenResource struct {
	client *client.Client
}

type serviceTokenModel struct {
	ID     types.String `tfsdk:"id"`
	Name   types.String `tfsdk:"name"`
	Scope  types.String `tfsdk:"scope"`
	Access types.String `tfsdk:"access"`
	Token  types.String `tfsdk:"token"`
}

func (r *serviceTokenResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_token"
}

func (r *serviceTokenResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A scoped Janus service token (`janus_svc_...`). The raw token is returned only once at creation and stored " +
			"in Terraform state as a sensitive computed attribute — use a sensitive/remote state backend.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Token ID (metadata handle, not the secret).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Token name. Changing it forces replacement (tokens are immutable and re-minted).",
				Required:            true,
				PlanModifiers:       replace,
			},
			"scope": schema.StringAttribute{
				MarkdownDescription: "Scope target UUID: a config ID or environment ID. Pair with `access`. Changing it forces replacement.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"access": schema.StringAttribute{
				MarkdownDescription: "Access level: `read` or `readwrite`. Changing it forces replacement.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "The raw minted token (`janus_svc_...`). Sensitive; available once at create and then persisted in state.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *serviceTokenResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

// scopeKindFor picks the scope kind. The Janus mint API needs kind ∈
// {config, environment}. We default to "config"; callers scoping to an
// environment set access on an environment id — but the wire needs the kind
// explicitly, so we infer nothing and let the server validate. To keep the
// resource simple and unambiguous we treat `scope` as a config id by default;
// see docs for the environment-scoped example using a separate approach.
func (r *serviceTokenResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceTokenModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	minted, err := r.client.MintToken(ctx, plan.Name.ValueString(), "config", plan.Scope.ValueString(), plan.Access.ValueString())
	if err != nil {
		apiErrorToDiag(&resp.Diagnostics, "Unable to mint service token", err)
		return
	}

	plan.ID = types.StringValue(minted.ID)
	plan.Token = types.StringValue(minted.Token)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *serviceTokenResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceTokenModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	meta, err := r.client.GetTokenMeta(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		apiErrorToDiag(&resp.Diagnostics, "Unable to read service token", err)
		return
	}
	// Refresh metadata; the raw token is never re-fetchable so it is preserved
	// from prior state (do not overwrite state.Token).
	state.Name = types.StringValue(meta.Name)
	state.Scope = types.StringValue(meta.ScopeID)
	state.Access = types.StringValue(meta.Access)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *serviceTokenResource) Update(ctx context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	// All plan-mutable attributes force replacement; Update should not be hit.
	resp.Diagnostics.AddError(
		"Update not supported",
		"janus_service_token attributes are immutable; changes re-mint (force replacement).",
	)
}

func (r *serviceTokenResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serviceTokenModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.RevokeToken(ctx, state.ID.ValueString()); err != nil {
		if client.IsNotFound(err) {
			return
		}
		apiErrorToDiag(&resp.Diagnostics, "Unable to revoke service token", err)
	}
}

// ImportState imports by token ID. The raw token cannot be recovered on import
// (it is shown only once at mint), so `token` will be empty after import.
func (r *serviceTokenResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
