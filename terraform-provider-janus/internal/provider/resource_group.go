package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/steveokay/janus-secrets/terraform-provider-janus/internal/client"
)

var (
	_ resource.Resource                   = (*groupResource)(nil)
	_ resource.ResourceWithConfigure      = (*groupResource)(nil)
	_ resource.ResourceWithImportState    = (*groupResource)(nil)
	_ resource.ResourceWithValidateConfig = (*groupResource)(nil)
)

// NewGroupResource is the janus_group resource factory.
func NewGroupResource() resource.Resource { return &groupResource{} }

type groupResource struct {
	client *client.Client
}

type groupModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Kind              types.String `tfsdk:"kind"`
	ClaimValue        types.String `tfsdk:"claim_value"`
	Description       types.String `tfsdk:"description"`
	CanCreateProjects types.Bool   `tfsdk:"can_create_projects"`
}

func (r *groupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (r *groupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Janus **group** — a subject a role binding can target instead of a user, so a whole team " +
			"is granted access once.\n\n" +
			"Managing the group catalog needs instance-scoped `group:manage`. *Binding* a group at a scope is a " +
			"**different** authority (`member:manage` at that scope) — see `janus_group_binding`.\n\n" +
			"A group is either IdP-fed (`kind = \"oidc\"`, membership refreshed from the identity provider at each " +
			"sign-in) or admin-curated (`kind = \"local\"`, an explicit member list managed with `janus_group_member`). " +
			"Never both: that split is what makes an access review run against the IdP *complete*.\n\n" +
			"Member counts are deliberately not exposed. An `oidc` group's member list only covers users Janus has " +
			"seen sign in, so any count here would read as the group's membership and would not be it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Group UUID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Group name, unique across BOTH kinds (so an IdP group and a local group can never " +
					"quietly become the same group). Janus has no rename endpoint, so changing it forces replacement — " +
					"which also drops every binding the group held.",
				Required:      true,
				PlanModifiers: replace,
			},
			"kind": schema.StringAttribute{
				MarkdownDescription: "`local` (explicit member list you manage) or `oidc` (membership comes from the " +
					"identity provider's group claim). Requires `claim_value` when `oidc`, and forbids it when `local`. " +
					"Changing it forces replacement.",
				Required:      true,
				PlanModifiers: replace,
				Validators:    []validator.String{stringOneOf(client.GroupKindLocal, client.GroupKindOIDC)},
			},
			"claim_value": schema.StringAttribute{
				MarkdownDescription: "For `kind = \"oidc\"`: the exact value the IdP emits in its groups claim (Okta/Google " +
					"send names or emails, Entra sends object GUIDs unless configured otherwise). Required for `oidc`, " +
					"forbidden for `local`. A claim value with no matching group grants nothing — groups are never " +
					"auto-created. Changing it forces replacement.",
				Optional:      true,
				PlanModifiers: replace,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Free-text description (display material — never secret material). Janus has no update " +
					"endpoint for it, so changing it forces replacement.",
				Optional:      true,
				PlanModifiers: replace,
			},
			"can_create_projects": schema.BoolAttribute{
				MarkdownDescription: "Delegate **project creation** to this group without granting instance admin (which " +
					"would carry `project:read` everywhere and reveal every project). A member then creates a project bound " +
					"to the group at `admin` and to themselves at `owner`. This is the one mutable field — it updates in " +
					"place via `PUT /v1/groups/{id}/capabilities`.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
		},
	}
}

func (r *groupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

// ValidateConfig enforces the two-kinds rule at PLAN time, against the raw
// configuration — so `kind = "oidc"` with no claim, or `kind = "local"` with
// one, fails locally instead of round-tripping to Janus for a 400.
//
// It runs on the config (not the plan), so it fires even when `id` and other
// computed values are still unknown.
func (r *groupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg groupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// An unknown kind or claim (computed from another resource) cannot be judged
	// yet; the Create pre-flight catches it.
	if cfg.Kind.IsUnknown() || cfg.ClaimValue.IsUnknown() {
		return
	}
	kind := cfg.Kind.ValueString()
	claim := cfg.ClaimValue.ValueString()

	if kind == client.GroupKindLocal && claim != "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("claim_value"),
			"claim_value is not valid for a local group",
			"A `local` group's membership is the explicit list you manage with janus_group_member; it has no IdP claim. "+
				"Drop claim_value, or set kind = \"oidc\" if the identity provider should own membership.",
		)
	}
	if kind == client.GroupKindOIDC && claim == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("claim_value"),
			"claim_value is required for an oidc group",
			"An `oidc` group is identified by the value your IdP emits in its groups claim. Without one the group would "+
				"match nothing a token can ever assert, so Janus refuses it. Set claim_value, or set kind = \"local\" to "+
				"manage membership in Janus.",
		)
	}
}

func (r *groupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	in := client.GroupInput{
		Name:              plan.Name.ValueString(),
		Kind:              plan.Kind.ValueString(),
		ClaimValue:        plan.ClaimValue.ValueString(),
		Description:       plan.Description.ValueString(),
		CanCreateProjects: plan.CanCreateProjects.ValueBool(),
	}
	// Belt and braces: the validator already ran at plan time, but a value that
	// was unknown then is concrete now.
	if err := client.ValidateGroupInput(in.Kind, in.ClaimValue); err != nil {
		resp.Diagnostics.AddError("Invalid group configuration", err.Error()+" No group was created.")
		return
	}

	g, err := r.client.CreateGroup(ctx, in)
	if err != nil {
		apiErrorToDiag(&resp.Diagnostics, "Unable to create group", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, groupToModel(g, plan))...)
}

func (r *groupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.GetGroup(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		apiErrorToDiag(&resp.Diagnostics, "Unable to read group", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, groupToModel(g, state))...)
}

// Update handles the only in-place change a group supports: the delegated
// project-creation capability. Every other attribute forces replacement.
func (r *groupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID

	if plan.CanCreateProjects.ValueBool() != state.CanCreateProjects.ValueBool() {
		if err := r.client.SetGroupCapability(ctx, plan.ID.ValueString(), plan.CanCreateProjects.ValueBool()); err != nil {
			apiErrorToDiag(&resp.Diagnostics, "Unable to update group capability", err)
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete removes the group. Membership and every binding it conferred cascade,
// so access granted through it is gone on the next request — there is no
// last-owner guard to worry about, because a group binding can never be owner.
func (r *groupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteGroup(ctx, state.ID.ValueString()); err != nil {
		if client.IsNotFound(err) {
			return
		}
		apiErrorToDiag(&resp.Diagnostics, "Unable to delete group", err)
	}
}

func (r *groupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// groupToModel maps a server group onto the model. `prior` supplies the values
// for attributes the server round-trips as empty-vs-null indistinguishably
// (description), so an optional attribute left unset does not flip to "" and
// produce a permanent diff.
func groupToModel(g *client.Group, prior groupModel) groupModel {
	m := groupModel{
		ID:                types.StringValue(g.ID),
		Name:              types.StringValue(g.Name),
		Kind:              types.StringValue(g.Kind),
		ClaimValue:        types.StringNull(),
		Description:       types.StringNull(),
		CanCreateProjects: types.BoolValue(g.CanCreateProjects),
	}
	if g.ClaimValue != nil && *g.ClaimValue != "" {
		m.ClaimValue = types.StringValue(*g.ClaimValue)
	}
	if g.Description != "" {
		m.Description = types.StringValue(g.Description)
	} else if !prior.Description.IsUnknown() {
		m.Description = prior.Description
	}
	return m
}
