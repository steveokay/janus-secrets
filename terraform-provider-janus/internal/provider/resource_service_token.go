package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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

// scopeKindConfig / scopeKindEnvironment are the ONLY scope kinds Janus mints
// service tokens for. Project- and instance-wide tokens do not exist by design;
// the server rejects anything else with `scope kind must be "config" or
// "environment"`.
const (
	scopeKindConfig      = "config"
	scopeKindEnvironment = "environment"
)

type serviceTokenModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	ScopeKind types.String `tfsdk:"scope_kind"`
	Scope     types.String `tfsdk:"scope"`
	Access    types.String `tfsdk:"access"`
	Token     types.String `tfsdk:"token"`
}

func (r *serviceTokenResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_token"
}

func (r *serviceTokenResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A scoped Janus service token (`janus_svc_...`), covering a single **config** or a whole " +
			"**environment** (see `scope_kind`). The raw token is returned only once at creation and stored in Terraform state " +
			"as a sensitive computed attribute — use a sensitive/remote state backend.",
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
			"scope_kind": schema.StringAttribute{
				MarkdownDescription: "What `scope` points at: `config` (default) or `environment`. Janus mints service tokens for these two " +
					"kinds only — there is no project-wide or instance-wide service token. An environment-scoped token covers every " +
					"config in that environment. Changing it forces replacement.",
				Optional:      true,
				Computed:      true,
				Default:       stringdefault.StaticString(scopeKindConfig),
				PlanModifiers: replace,
				Validators:    []validator.String{stringOneOf(scopeKindConfig, scopeKindEnvironment)},
			},
			"scope": schema.StringAttribute{
				MarkdownDescription: "Scope target UUID — a config ID when `scope_kind = \"config\"`, an environment ID when " +
					"`scope_kind = \"environment\"`. Pair with `access`. Changing it forces replacement.",
				Required:      true,
				PlanModifiers: replace,
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

// Create mints the token. The scope kind is taken from `scope_kind` (default
// "config") and re-checked here so an invalid kind can never reach the API,
// even if the attribute validator is bypassed.
func (r *serviceTokenResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceTokenModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	kind := plan.ScopeKind.ValueString()
	if plan.ScopeKind.IsNull() || plan.ScopeKind.IsUnknown() || kind == "" {
		kind = scopeKindConfig
	}
	if !isOneOf(kind, scopeKindConfig, scopeKindEnvironment) {
		resp.Diagnostics.AddAttributeError(
			pathRoot("scope_kind"),
			"Invalid scope kind",
			fmt.Sprintf("scope_kind must be %q or %q, got %q. No token was minted.", scopeKindConfig, scopeKindEnvironment, kind),
		)
		return
	}

	minted, err := r.client.MintToken(ctx, plan.Name.ValueString(), kind, plan.Scope.ValueString(), plan.Access.ValueString())
	if err != nil {
		apiErrorToDiag(&resp.Diagnostics, "Unable to mint service token", err)
		return
	}

	plan.ScopeKind = types.StringValue(kind)
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
	// scope_kind refreshes from the server. This also back-fills state written
	// by a provider version that predates the attribute (which recorded a null),
	// so upgrading does not plan a spurious replacement of existing tokens.
	if meta.ScopeKind != "" {
		state.ScopeKind = types.StringValue(meta.ScopeKind)
	} else if state.ScopeKind.IsNull() || state.ScopeKind.IsUnknown() {
		state.ScopeKind = types.StringValue(scopeKindConfig)
	}
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
