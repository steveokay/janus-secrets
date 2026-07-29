package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/steveokay/janus-secrets/terraform-provider-janus/internal/client"
)

var (
	_ resource.Resource                   = (*groupBindingResource)(nil)
	_ resource.ResourceWithConfigure      = (*groupBindingResource)(nil)
	_ resource.ResourceWithImportState    = (*groupBindingResource)(nil)
	_ resource.ResourceWithValidateConfig = (*groupBindingResource)(nil)
)

// NewGroupBindingResource is the janus_group_binding resource factory.
func NewGroupBindingResource() resource.Resource { return &groupBindingResource{} }

type groupBindingResource struct {
	client *client.Client
}

type groupBindingModel struct {
	ID            types.String `tfsdk:"id"`
	GroupID       types.String `tfsdk:"group_id"`
	Role          types.String `tfsdk:"role"`
	ProjectID     types.String `tfsdk:"project_id"`
	EnvironmentID types.String `tfsdk:"environment_id"`
	ScopeLevel    types.String `tfsdk:"scope_level"`
}

func (r *groupBindingResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group_binding"
}

func (r *groupBindingResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Bind a Janus **group** at a scope with a role, so everyone in the group holds that role " +
			"there — including people who join the group later.\n\n" +
			"**This is a different authority from `janus_group`.** Curating the catalog (which groups exist, local " +
			"membership, the claim mapping) is instance-scoped `group:manage`. *Binding* a group needs `member:manage` " +
			"**at the scope you are binding**, and is capped by your own bound role — measured against your durable " +
			"role, never a break-glass elevation. A project admin can therefore grant a group access to their project " +
			"but cannot add themselves to that group; a token that can create groups may be unable to bind them " +
			"anywhere. If you run Terraform with one token it needs both.\n\n" +
			"Scope is chosen by which ids you set:\n\n" +
			"| `project_id` | `environment_id` | Scope |\n" +
			"| --- | --- | --- |\n" +
			"| unset | unset | instance-wide |\n" +
			"| set | unset | the whole project (all its environments and configs) |\n" +
			"| set | set | that one environment |\n\n" +
			"Bindings **union** with direct user bindings exactly as two direct bindings do — no precedence, no deny " +
			"rules. A project-scoped grant therefore covers production too; make the production environment four-eyes " +
			"(`janus env protect prod`) rather than looking for a deny rule.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Computed — `\"instance/<group_id>\"`, `\"project/<project_id>/<group_id>\"` or " +
					"`\"environment/<project_id>/<environment_id>/<group_id>\"`.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"group_id": schema.StringAttribute{
				MarkdownDescription: "UUID of the group to bind. Changing it forces replacement.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"role": schema.StringAttribute{
				MarkdownDescription: "`viewer`, `developer` or `admin`. **Never `owner`** — a group binding cannot be " +
					"owner (a database `CHECK` and the API both refuse it), and the provider rejects it at " +
					"`terraform plan`. Updatable in place.",
				Required:   true,
				Validators: []validator.String{stringGroupRole()},
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "Project UUID for a project- or environment-scoped binding. Omit for instance-wide. " +
					"Required alongside `environment_id` (the environment route is nested under its project). Changing " +
					"it forces replacement.",
				Optional:      true,
				PlanModifiers: replace,
			},
			"environment_id": schema.StringAttribute{
				MarkdownDescription: "Environment UUID for an environment-scoped binding; also set `project_id`. " +
					"Changing it forces replacement.",
				Optional:      true,
				PlanModifiers: replace,
			},
			"scope_level": schema.StringAttribute{
				MarkdownDescription: "Computed — `instance`, `project` or `environment`, derived from the ids above. " +
					"Exposed so plan output states plainly how wide the grant is.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *groupBindingResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

// ValidateConfig rejects an environment binding with no project at PLAN time.
// The environment group-members route is nested under its project, so without
// `project_id` there is no URL to call — better to say that locally than to
// build a request that cannot exist.
func (r *groupBindingResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg groupBindingModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.EnvironmentID.IsUnknown() || cfg.ProjectID.IsUnknown() {
		return
	}
	if cfg.EnvironmentID.ValueString() != "" && cfg.ProjectID.ValueString() == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("project_id"),
			"project_id is required for an environment-scoped binding",
			"Janus addresses an environment's group members under its project "+
				"(`/v1/projects/{project}/environments/{environment}/group-members`), so the project UUID is part of "+
				"the scope, not an optional extra. Set project_id to the environment's project.",
		)
	}
}

func (r *groupBindingResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupBindingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	scope := bindingScopeOf(plan)
	role := plan.Role.ValueString()

	// Belt and braces: the plan-time validator already refused owner, but a role
	// that was unknown then is concrete now, and nothing must reach the API that
	// the plan would have rejected.
	if err := client.ValidateGroupRole(role); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("role"), "Invalid role for a group binding",
			err.Error()+" No binding was written.")
		return
	}

	if err := r.client.PutGroupBinding(ctx, scope, plan.GroupID.ValueString(), role); err != nil {
		apiErrorToDiag(&resp.Diagnostics, "Unable to bind group", err)
		return
	}
	setBindingComputed(&plan, scope)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *groupBindingResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupBindingModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	scope := bindingScopeOf(state)
	b, err := r.client.GetGroupBinding(ctx, scope, state.GroupID.ValueString())
	if err != nil {
		// 404 covers both "the scope is gone" and "the group is no longer bound
		// here" — either way the binding this resource manages does not exist.
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		apiErrorToDiag(&resp.Diagnostics, "Unable to read group binding", err)
		return
	}
	state.Role = types.StringValue(b.Role)
	setBindingComputed(&state, scope)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update changes the role in place. The API's PUT upserts the binding at its
// exact scope, so re-issuing it with a new role is the whole operation; every
// scope attribute forces replacement instead.
func (r *groupBindingResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan groupBindingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	scope := bindingScopeOf(plan)
	role := plan.Role.ValueString()
	if err := client.ValidateGroupRole(role); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("role"), "Invalid role for a group binding",
			err.Error()+" The binding was not changed.")
		return
	}
	if err := r.client.PutGroupBinding(ctx, scope, plan.GroupID.ValueString(), role); err != nil {
		apiErrorToDiag(&resp.Diagnostics, "Unable to update group binding", err)
		return
	}
	setBindingComputed(&plan, scope)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *groupBindingResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupBindingModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteGroupBinding(ctx, bindingScopeOf(state), state.GroupID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		apiErrorToDiag(&resp.Diagnostics, "Unable to unbind group", err)
	}
}

// ImportState accepts the same id this resource computes:
//
//	instance/<group_id>
//	project/<project_id>/<group_id>
//	environment/<project_id>/<environment_id>/<group_id>
//
// The role is filled in by the refresh that follows.
func (r *groupBindingResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	var groupID, projectID, envID, level string
	switch {
	case len(parts) == 2 && parts[0] == client.ScopeLevelInstance:
		level, groupID = client.ScopeLevelInstance, parts[1]
	case len(parts) == 3 && parts[0] == client.ScopeLevelProject:
		level, projectID, groupID = client.ScopeLevelProject, parts[1], parts[2]
	case len(parts) == 4 && parts[0] == client.ScopeLevelEnvironment:
		level, projectID, envID, groupID = client.ScopeLevelEnvironment, parts[1], parts[2], parts[3]
	default:
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected \"instance/<group_id>\", \"project/<project_id>/<group_id>\" or "+
				"\"environment/<project_id>/<environment_id>/<group_id>\", got %q.", req.ID),
		)
		return
	}
	if groupID == "" {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Missing group id in %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("group_id"), groupID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("scope_level"), level)...)
	if projectID != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), projectID)...)
	}
	if envID != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment_id"), envID)...)
	}
}

// bindingScopeOf derives the API scope from the model's ids.
func bindingScopeOf(m groupBindingModel) client.BindingScope {
	return client.BindingScope{
		ProjectID:     m.ProjectID.ValueString(),
		EnvironmentID: m.EnvironmentID.ValueString(),
	}
}

// setBindingComputed fills the computed id and scope_level from the scope.
func setBindingComputed(m *groupBindingModel, scope client.BindingScope) {
	level := scope.Level()
	m.ScopeLevel = types.StringValue(level)
	m.ID = types.StringValue(groupBindingID(scope, m.GroupID.ValueString()))
}

// groupBindingID renders the resource id, which doubles as the import syntax.
func groupBindingID(scope client.BindingScope, groupID string) string {
	switch scope.Level() {
	case client.ScopeLevelEnvironment:
		return strings.Join([]string{client.ScopeLevelEnvironment, scope.ProjectID, scope.EnvironmentID, groupID}, "/")
	case client.ScopeLevelProject:
		return strings.Join([]string{client.ScopeLevelProject, scope.ProjectID, groupID}, "/")
	default:
		return client.ScopeLevelInstance + "/" + groupID
	}
}
