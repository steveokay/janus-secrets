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
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/steveokay/janus-secrets/terraform-provider-janus/internal/client"
)

var (
	_ resource.Resource                = (*groupMemberResource)(nil)
	_ resource.ResourceWithConfigure   = (*groupMemberResource)(nil)
	_ resource.ResourceWithImportState = (*groupMemberResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*groupMemberResource)(nil)
)

// NewGroupMemberResource is the janus_group_member resource factory.
func NewGroupMemberResource() resource.Resource { return &groupMemberResource{} }

type groupMemberResource struct {
	client *client.Client
}

type groupMemberModel struct {
	ID      types.String `tfsdk:"id"`
	GroupID types.String `tfsdk:"group_id"`
	UserID  types.String `tfsdk:"user_id"`
}

// oidcMemberRefusal is the single wording for "you cannot hand-add a member to
// an IdP-fed group", shared by the plan-time check and the create pre-flight.
const oidcMemberRefusal = "Membership of an `oidc` group is a snapshot refreshed from the identity provider at each " +
	"sign-in, so Janus cannot accept a hand-added member — the database schema makes one unrepresentable, not just the " +
	"API. That is deliberate: it is what makes an access review run against the IdP complete for every binding this " +
	"group confers.\n\n" +
	"Manage this membership in the identity provider, or use a `local` group (kind = \"local\") if you need Janus to " +
	"own the list. For genuinely temporary access use break-glass, which is TTL-clamped and expires by itself."

func (r *groupMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group_member"
}

func (r *groupMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Membership of a **local** Janus group.\n\n" +
			"Only valid for `kind = \"local\"` groups. An `oidc` group's membership comes from the identity provider and " +
			"is refreshed at each sign-in; an admin can never hand-add a member, and the schema makes such a row " +
			"unrepresentable. The provider refuses at `terraform plan` whenever it can see the group's kind.\n\n" +
			"Needs instance-scoped `group:manage` — the same authority as `janus_group`, and a *different* one from " +
			"`janus_group_binding`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Computed — `\"<group_id>/<user_id>\"`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"group_id": schema.StringAttribute{
				MarkdownDescription: "UUID of the **local** group. Changing it forces replacement.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"user_id": schema.StringAttribute{
				MarkdownDescription: "UUID of the Janus user to add. Changing it forces replacement.",
				Required:            true,
				PlanModifiers:       replace,
			},
		},
	}
}

func (r *groupMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

// ModifyPlan refuses a member of an `oidc` group at PLAN time.
//
// The group's kind is not in this resource's own configuration, so the only way
// to know it during plan is to ask the server — which is possible exactly when
// `group_id` is already concrete (an existing group, an imported one, or a
// literal id). When the group is being created in the same apply its id is still
// unknown, and the Create pre-flight catches it before any write instead.
//
// A failed lookup is deliberately SILENT: plan must not break because the server
// is briefly unreachable, the token cannot read the catalog, or the group is
// created later in the graph. Only a definitive "this group is oidc" stops the
// plan.
func (r *groupMemberResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return // destroy plan: nothing to validate
	}
	if r.client == nil {
		return
	}
	var plan groupMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.GroupID.IsUnknown() || plan.GroupID.ValueString() == "" {
		return
	}
	g, err := r.client.GetGroup(ctx, plan.GroupID.ValueString())
	if err != nil || g == nil {
		return
	}
	if g.Kind != client.GroupKindLocal {
		resp.Diagnostics.AddAttributeError(
			path.Root("group_id"),
			fmt.Sprintf("Group %q is IdP-fed (kind = %q)", g.Name, g.Kind),
			oidcMemberRefusal,
		)
	}
}

// Create adds the member, pre-flighting the group's kind so a first-apply
// mistake (where ModifyPlan could not resolve the id yet) still fails with a
// provider-authored explanation rather than a bare server 409.
func (r *groupMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	gid, uid := plan.GroupID.ValueString(), plan.UserID.ValueString()

	g, err := r.client.GetGroup(ctx, gid)
	if err != nil {
		apiErrorToDiag(&resp.Diagnostics, "Unable to read group before adding a member", err)
		return
	}
	if g.Kind != client.GroupKindLocal {
		resp.Diagnostics.AddAttributeError(
			path.Root("group_id"),
			fmt.Sprintf("Group %q is IdP-fed (kind = %q)", g.Name, g.Kind),
			oidcMemberRefusal+"\n\nNo membership was written.",
		)
		return
	}

	if err := r.client.AddGroupMember(ctx, gid, uid); err != nil {
		apiErrorToDiag(&resp.Diagnostics, "Unable to add group member", err)
		return
	}
	plan.ID = types.StringValue(groupMemberID(gid, uid))
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *groupMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	gid, uid := state.GroupID.ValueString(), state.UserID.ValueString()

	present, err := r.client.HasGroupMember(ctx, gid, uid)
	if err != nil {
		if client.IsNotFound(err) {
			// The group itself is gone; the membership went with it.
			resp.State.RemoveResource(ctx)
			return
		}
		apiErrorToDiag(&resp.Diagnostics, "Unable to read group membership", err)
		return
	}
	if !present {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(groupMemberID(gid, uid))
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *groupMemberResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Both attributes force replacement; Update should never be reached.
	resp.Diagnostics.AddError(
		"Update not supported",
		"janus_group_member has no mutable attributes; a change re-creates the membership.",
	)
}

func (r *groupMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.RemoveGroupMember(ctx, state.GroupID.ValueString(), state.UserID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		apiErrorToDiag(&resp.Diagnostics, "Unable to remove group member", err)
	}
}

// ImportState accepts "<group_id>/<user_id>".
func (r *groupMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	gid, uid, ok := strings.Cut(req.ID, "/")
	if !ok || gid == "" || uid == "" {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected \"<group_id>/<user_id>\", got %q.", req.ID),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), groupMemberID(gid, uid))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("group_id"), gid)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), uid)...)
}

func groupMemberID(groupID, userID string) string { return groupID + "/" + userID }
