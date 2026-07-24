package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/steveokay/janus-secrets/terraform-provider-janus/internal/client"
)

var (
	_ resource.Resource                = (*environmentResource)(nil)
	_ resource.ResourceWithConfigure   = (*environmentResource)(nil)
	_ resource.ResourceWithImportState = (*environmentResource)(nil)
)

// NewEnvironmentResource is the janus_environment resource factory.
func NewEnvironmentResource() resource.Resource { return &environmentResource{} }

type environmentResource struct {
	client *client.Client
}

type environmentModel struct {
	ID        types.String `tfsdk:"id"`
	ProjectID types.String `tfsdk:"project_id"`
	Slug      types.String `tfsdk:"slug"`
	Name      types.String `tfsdk:"name"`
}

func (r *environmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environment"
}

func (r *environmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An environment within a Janus project (e.g. dev/staging/prod).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Environment UUID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "Parent project UUID. Changing it forces replacement.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Immutable environment slug. Changing it forces replacement.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Human-readable environment name.",
				Optional:            true,
				Computed:            true,
			},
		},
	}
}

func (r *environmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *environmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan environmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.client.CreateEnvironment(ctx, plan.ProjectID.ValueString(), plan.Slug.ValueString(), plan.Name.ValueString())
	if err != nil {
		apiErrorToDiag(&resp.Diagnostics, "Unable to create environment", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, environmentToModel(e))...)
}

func (r *environmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state environmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.client.GetEnvironment(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		apiErrorToDiag(&resp.Diagnostics, "Unable to read environment", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, environmentToModel(e))...)
}

func (r *environmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan environmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.client.UpdateEnvironment(ctx, plan.ProjectID.ValueString(), plan.ID.ValueString(), plan.Name.ValueString())
	if err != nil {
		apiErrorToDiag(&resp.Diagnostics, "Unable to update environment", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, environmentToModel(e))...)
}

func (r *environmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state environmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteEnvironment(ctx, state.ProjectID.ValueString(), state.ID.ValueString(), false); err != nil {
		if client.IsNotFound(err) {
			return
		}
		apiErrorToDiag(&resp.Diagnostics, "Unable to delete environment", err)
	}
}

// ImportState accepts "project_id/environment_id" because environment routes
// are addressed by both ids.
func (r *environmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected \"project_id/environment_id\", got %q.", req.ID),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathRoot("project_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathRoot("id"), parts[1])...)
}

func environmentToModel(e *client.Environment) environmentModel {
	return environmentModel{
		ID:        types.StringValue(e.ID),
		ProjectID: types.StringValue(e.ProjectID),
		Slug:      types.StringValue(e.Slug),
		Name:      types.StringValue(e.Name),
	}
}
