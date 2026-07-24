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
	_ resource.Resource                = (*secretResource)(nil)
	_ resource.ResourceWithConfigure   = (*secretResource)(nil)
	_ resource.ResourceWithImportState = (*secretResource)(nil)
)

// NewSecretResource is the janus_secret resource factory.
func NewSecretResource() resource.Resource { return &secretResource{} }

type secretResource struct {
	client *client.Client
}

type secretModel struct {
	ID       types.String `tfsdk:"id"`
	ConfigID types.String `tfsdk:"config_id"`
	Key      types.String `tfsdk:"key"`
	Value    types.String `tfsdk:"value"`
}

func (r *secretResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secret"
}

func (r *secretResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A single secret key/value in a Janus config. Writing a value creates a new immutable config version. " +
			"The value is stored in Terraform state — use a sensitive/remote backend.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Synthetic ID: `<config_id>/<key>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"config_id": schema.StringAttribute{
				MarkdownDescription: "Config UUID that owns this secret. Changing it forces replacement.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"key": schema.StringAttribute{
				MarkdownDescription: "Secret key name. Changing it forces replacement.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"value": schema.StringAttribute{
				MarkdownDescription: "Secret value. Marked sensitive; stored (encrypted at rest server-side) but also written to Terraform state.",
				Required:            true,
				Sensitive:           true,
			},
		},
	}
}

func (r *secretResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *secretResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan secretModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.SetSecret(ctx, plan.ConfigID.ValueString(), plan.Key.ValueString(), plan.Value.ValueString()); err != nil {
		apiErrorToDiag(&resp.Diagnostics, "Unable to write secret", err)
		return
	}
	plan.ID = types.StringValue(secretID(plan.ConfigID.ValueString(), plan.Key.ValueString()))
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *secretResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	value, err := r.client.GetSecret(ctx, state.ConfigID.ValueString(), state.Key.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		apiErrorToDiag(&resp.Diagnostics, "Unable to read secret", err)
		return
	}
	state.Value = types.StringValue(value)
	state.ID = types.StringValue(secretID(state.ConfigID.ValueString(), state.Key.ValueString()))
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *secretResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan secretModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Only value is mutable in place (config_id/key force replacement).
	if err := r.client.SetSecret(ctx, plan.ConfigID.ValueString(), plan.Key.ValueString(), plan.Value.ValueString()); err != nil {
		apiErrorToDiag(&resp.Diagnostics, "Unable to update secret", err)
		return
	}
	plan.ID = types.StringValue(secretID(plan.ConfigID.ValueString(), plan.Key.ValueString()))
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *secretResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteSecret(ctx, state.ConfigID.ValueString(), state.Key.ValueString()); err != nil {
		if client.IsNotFound(err) {
			return
		}
		apiErrorToDiag(&resp.Diagnostics, "Unable to delete secret", err)
	}
}

// ImportState accepts "config_id/key".
func (r *secretResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected \"config_id/key\", got %q.", req.ID),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathRoot("config_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathRoot("key"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathRoot("id"), req.ID)...)
}

func secretID(configID, key string) string { return configID + "/" + key }
