package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/steveokay/janus-secrets/terraform-provider-janus/internal/client"
)

var (
	_ resource.Resource                = (*secretsResource)(nil)
	_ resource.ResourceWithConfigure   = (*secretsResource)(nil)
	_ resource.ResourceWithImportState = (*secretsResource)(nil)
)

// defaultBatchMessage labels the config version Janus creates for a batch apply.
const defaultBatchMessage = "terraform apply"

// NewSecretsResource is the janus_secrets (batch) resource factory.
func NewSecretsResource() resource.Resource { return &secretsResource{} }

// secretsResource manages a WHOLE map of key/value secrets in one config and
// commits every add/change/removal through the batch endpoint
// (PUT /v1/configs/{cid}/secrets), so an apply that touches N keys creates
// exactly ONE config version instead of N.
type secretsResource struct {
	client *client.Client
}

type secretsModel struct {
	ID            types.String `tfsdk:"id"`
	ConfigID      types.String `tfsdk:"config_id"`
	Message       types.String `tfsdk:"message"`
	Secrets       types.Map    `tfsdk:"secrets"`
	ValueVersions types.Map    `tfsdk:"value_versions"`
	ConfigVersion types.Int64  `tfsdk:"config_version"`
}

func (r *secretsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secrets"
}

func (r *secretsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A **batch** of secret key/values in one Janus config. Every add, change and removal in a single " +
			"`terraform apply` is sent as ONE request to `PUT /v1/configs/{id}/secrets`, so Janus records exactly **one config " +
			"version** for the whole set (the unit of diff and rollback) instead of one per key.\n\n" +
			"Values are **Sensitive** and are written to Terraform state — use a sensitive/remote state backend.\n\n" +
			"**Drift detection is metadata-only.** The masked list endpoint is value-free, so this resource cannot compare " +
			"stored plaintext against your configuration. It tracks each key's server-side `value_version` instead: if a key was " +
			"written or deleted outside Terraform, the version moves (or the key disappears) and the next plan proposes " +
			"rewriting that key. A value changed *and changed back* out of band, or a value that never matched at import time, " +
			"is still detected as a write — but the provider genuinely cannot tell you what the stored value is.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Synthetic ID — the config UUID this batch manages.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"config_id": schema.StringAttribute{
				MarkdownDescription: "Config UUID that owns these secrets. Changing it forces replacement.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"message": schema.StringAttribute{
				MarkdownDescription: "Commit message recorded on the config version this resource creates. " +
					"Changing it alone does not write anything (there would be no change to commit).",
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(defaultBatchMessage),
			},
			"secrets": schema.MapAttribute{
				MarkdownDescription: "Map of secret key → value. Removing a key from the map **tombstones** it in Janus on the next " +
					"apply (a soft delete, recoverable from a previous config version). Keys that are not in this map are never " +
					"touched. The whole map is Sensitive, so plan output masks values *and* key names; use `value_versions` " +
					"(value-free) to see which keys this resource tracks.",
				Required:    true,
				Sensitive:   true,
				ElementType: types.StringType,
			},
			"value_versions": schema.MapAttribute{
				MarkdownDescription: "Value-free drift ledger: key → the server-side `value_version` observed at the last read or " +
					"write. Janus bumps this counter on every write to a key, so a mismatch means someone wrote to the key " +
					"outside Terraform. Not sensitive (no plaintext).",
				Computed:    true,
				ElementType: types.Int64Type,
			},
			"config_version": schema.Int64Attribute{
				MarkdownDescription: "The config version created by the most recent apply of this resource (`0` when the last apply " +
					"had nothing to commit). Refreshed only on write, not on read.",
				Computed: true,
			},
		},
	}
}

func (r *secretsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *secretsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan secretsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	desired := stringMap(ctx, plan.Secrets, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	cid := plan.ConfigID.ValueString()

	// Adopt-don't-clobber: refuse to create over keys that already exist in this
	// config. Those may be managed by a `janus_secret` resource, by the UI, or by
	// a human — silently overwriting them would make two owners fight over the
	// same key on every apply.
	if len(desired) > 0 {
		existing, err := r.client.ListSecretsMasked(ctx, cid)
		if err != nil {
			apiErrorToDiag(&resp.Diagnostics, "Unable to inspect config before writing secrets", err)
			return
		}
		if r.rejectCollisions(&resp.Diagnostics, existing, keysOf(desired)) {
			return
		}
	}

	changes := make([]client.SecretChange, 0, len(desired))
	for _, k := range keysOf(desired) {
		changes = append(changes, client.SecretChange{Key: k, Value: desired[k]})
	}
	r.applyBatch(ctx, cid, plan.Message.ValueString(), changes, desired, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = types.StringValue(cid)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Read refreshes the value-free drift ledger. It CANNOT compare plaintext (the
// masked list has none), so it uses value_version movement as the signal: a key
// whose version moved, or that vanished, is dropped from the state map, which
// makes the next plan propose (re)writing exactly that key.
func (r *secretsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state secretsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cid := state.ConfigID.ValueString()

	metas, err := r.client.ListSecretsMasked(ctx, cid)
	if err != nil {
		if client.IsNotFound(err) {
			// The config itself is gone — drop the whole resource from state.
			resp.State.RemoveResource(ctx)
			return
		}
		apiErrorToDiag(&resp.Diagnostics, "Unable to list secrets", err)
		return
	}

	managed := stringMap(ctx, state.Secrets, &resp.Diagnostics)
	recorded := int64Map(ctx, state.ValueVersions, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	values := make(map[string]string, len(managed))
	versions := make(map[string]int64, len(managed))
	// Walk every key this resource OWNS — that is the union of the remembered
	// values and the drift ledger. They diverge after a drifted key has its value
	// dropped: the ledger keeps the ownership record so a repeated refresh does
	// not forget that the key is ours (which would make the next apply look like
	// a first-time adoption and trip the collision guard).
	for _, key := range keysOf(union(managed, recorded)) {
		meta, ok := metas[key]
		// Missing, or visible only by inheritance from a base config: this config
		// no longer stores the key, so treat it as deleted out of band and forget
		// it entirely — the next plan re-adds it.
		if !ok || !meta.Owned() {
			continue
		}
		versions[key] = int64(meta.ValueVersion)
		val, remembered := managed[key]
		if !remembered {
			continue
		}
		if prev, tracked := recorded[key]; tracked && prev != int64(meta.ValueVersion) {
			// Written outside Terraform. We cannot see the new value, so we drop
			// the remembered one rather than assert a value we cannot verify.
			continue
		}
		values[key] = val
	}

	state.ID = types.StringValue(cid)
	setSecretMaps(ctx, &state, values, versions, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *secretsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state secretsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	desired := stringMap(ctx, plan.Secrets, &resp.Diagnostics)
	prior := stringMap(ctx, state.Secrets, &resp.Diagnostics)
	priorVersions := int64Map(ctx, state.ValueVersions, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	cid := plan.ConfigID.ValueString()

	var changes []client.SecretChange
	var adopted []string
	for _, k := range keysOf(desired) {
		old, existed := prior[k]
		// A key can be absent from prior.secrets yet still be ours: Read drops the
		// remembered value when a key's value_version moved out of band, keeping
		// the ledger entry. Such a key is a REASSERT (the drift the plan is fixing),
		// not a first-time adoption, so it must not trip the collision guard.
		if _, tracked := priorVersions[k]; !existed && !tracked {
			adopted = append(adopted, k)
		}
		if !existed || old != desired[k] {
			changes = append(changes, client.SecretChange{Key: k, Value: desired[k]})
		}
	}
	// A key removed from the map is tombstoned — in the SAME batch as the writes,
	// so an apply that adds two keys and drops one is still one config version.
	// Ownership comes from the union of remembered values and the drift ledger, so
	// a key that drifted (value dropped, ledger kept) is still deleted on removal.
	for _, k := range keysOf(union(prior, priorVersions)) {
		if _, still := desired[k]; !still {
			changes = append(changes, client.SecretChange{Key: k, Delete: true})
		}
	}

	if len(changes) == 0 {
		// Nothing to commit (e.g. only `message` changed). Do NOT call the API:
		// the batch endpoint rejects an empty change set, and an empty write
		// would create a pointless config version.
		plan.ID = types.StringValue(cid)
		setSecretMaps(ctx, &plan, desired, priorVersions, &resp.Diagnostics)
		plan.ConfigVersion = state.ConfigVersion
		if plan.ConfigVersion.IsNull() || plan.ConfigVersion.IsUnknown() {
			plan.ConfigVersion = types.Int64Value(0)
		}
		if resp.Diagnostics.HasError() {
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
		return
	}

	// Same adopt-don't-clobber guard as Create, but only for keys this resource
	// is taking over for the first time.
	if len(adopted) > 0 {
		existing, err := r.client.ListSecretsMasked(ctx, cid)
		if err != nil {
			apiErrorToDiag(&resp.Diagnostics, "Unable to inspect config before writing secrets", err)
			return
		}
		if r.rejectCollisions(&resp.Diagnostics, existing, adopted) {
			return
		}
	}

	r.applyBatch(ctx, cid, plan.Message.ValueString(), changes, desired, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = types.StringValue(cid)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete tombstones every managed key in one batch (one config version). Keys
// this resource never managed are left alone.
func (r *secretsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state secretsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	managed := stringMap(ctx, state.Secrets, &resp.Diagnostics)
	tracked := int64Map(ctx, state.ValueVersions, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	// Ledger ∪ values: a key whose value was dropped as drift is still ours.
	owned := keysOf(union(managed, tracked))
	if len(owned) == 0 {
		return
	}
	changes := make([]client.SecretChange, 0, len(owned))
	for _, k := range owned {
		changes = append(changes, client.SecretChange{Key: k, Delete: true})
	}
	if _, err := r.client.BatchWriteSecrets(ctx, state.ConfigID.ValueString(), "terraform destroy", changes); err != nil {
		if client.IsNotFound(err) {
			// Config already gone; the keys went with it.
			return
		}
		apiErrorToDiag(&resp.Diagnostics, "Unable to delete secrets", err)
	}
}

// ImportState adopts an existing config by UUID. Values cannot be recovered
// value-free, so `secrets` starts empty and the first apply rewrites every key
// in your configuration (as one config version).
func (r *secretsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if strings.TrimSpace(req.ID) == "" {
		resp.Diagnostics.AddError("Invalid import ID", "Expected a config UUID.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("config_id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("message"), defaultBatchMessage)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("secrets"), map[string]string{})...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("value_versions"), map[string]int64{})...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("config_version"), int64(0))...)
}

// applyBatch performs the single batched write and refreshes the drift ledger.
// `final` is the complete desired key set for the config (NOT just the changed
// keys) — it is what lands in state, so unchanged keys survive an update.
func (r *secretsResource) applyBatch(ctx context.Context, cid, message string, changes []client.SecretChange,
	final map[string]string, model *secretsModel, diags *diag.Diagnostics) {
	if len(changes) == 0 {
		setSecretMaps(ctx, model, final, map[string]int64{}, diags)
		model.ConfigVersion = types.Int64Value(0)
		return
	}
	if message == "" {
		message = defaultBatchMessage
	}
	cv, err := r.client.BatchWriteSecrets(ctx, cid, message, changes)
	if err != nil {
		if errors.Is(err, client.ErrApprovalRequired) {
			diags.AddError(
				"Config requires approval — secrets were NOT written",
				"This config is protected (four-eyes). Janus filed the batch as a pending edit request instead of committing it, "+
					"so Terraform cannot record it as applied. Approve the request in Janus, or stop managing this config with Terraform.",
			)
			return
		}
		apiErrorToDiag(diags, "Unable to write secrets", err)
		return
	}

	versions := r.refreshVersions(ctx, cid, final, diags)
	setSecretMaps(ctx, model, final, versions, diags)
	model.ConfigVersion = types.Int64Value(int64(cv.Version))
}

// refreshVersions re-reads the value-free masked list to record each written
// key's new value_version. A failure here is not fatal — the write already
// landed — but it does degrade drift detection until the next successful read.
func (r *secretsResource) refreshVersions(ctx context.Context, cid string, keys map[string]string, diags *diag.Diagnostics) map[string]int64 {
	versions := map[string]int64{}
	metas, err := r.client.ListSecretsMasked(ctx, cid)
	if err != nil {
		diags.AddWarning(
			"Secrets written, but the drift ledger could not be refreshed",
			fmt.Sprintf("The batch was committed. Reading back key metadata failed (%v), so `value_versions` is incomplete "+
				"until the next successful refresh; out-of-band changes to these keys may go unnoticed in the meantime.", err),
		)
		return versions
	}
	for k := range keys {
		if m, ok := metas[k]; ok && m.Owned() {
			versions[k] = int64(m.ValueVersion)
		}
	}
	return versions
}

// rejectCollisions fails loudly when a key this resource is about to adopt is
// already stored in the config. Reports true when it added an error.
// Key NAMES are not secret (the audit log records them), so naming them here is
// safe and is the only way to make the error actionable.
func (r *secretsResource) rejectCollisions(diags *diag.Diagnostics, existing map[string]client.SecretMeta, keys []string) bool {
	var clashes []string
	for _, k := range keys {
		if m, ok := existing[k]; ok && m.Owned() {
			clashes = append(clashes, k)
		}
	}
	if len(clashes) == 0 {
		return false
	}
	sort.Strings(clashes)
	diags.AddError(
		"Secret keys already exist in this config",
		fmt.Sprintf("janus_secrets refuses to overwrite keys it does not already manage: %s.\n\n"+
			"They may be managed by a `janus_secret` resource, by the web UI, or by hand. Remove the other manager (or drop the "+
			"keys from this map) — two owners for one key would fight on every apply. Nothing was written.",
			strings.Join(clashes, ", ")),
	)
	return true
}

// --- small helpers ---

// union returns the key set of two maps (values are irrelevant), used as this
// resource's "keys I own" record.
func union[A any, B any](a map[string]A, b map[string]B) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out) // deterministic request bodies and error messages
	return out
}

func stringMap(ctx context.Context, m types.Map, diags *diag.Diagnostics) map[string]string {
	out := map[string]string{}
	if m.IsNull() || m.IsUnknown() {
		return out
	}
	d := m.ElementsAs(ctx, &out, false)
	if d.HasError() {
		for _, e := range d.Errors() {
			diags.AddError(e.Summary(), e.Detail())
		}
	}
	return out
}

func int64Map(ctx context.Context, m types.Map, diags *diag.Diagnostics) map[string]int64 {
	out := map[string]int64{}
	if m.IsNull() || m.IsUnknown() {
		return out
	}
	d := m.ElementsAs(ctx, &out, false)
	if d.HasError() {
		for _, e := range d.Errors() {
			diags.AddError(e.Summary(), e.Detail())
		}
	}
	return out
}

func setSecretMaps(ctx context.Context, model *secretsModel, values map[string]string, versions map[string]int64, diags *diag.Diagnostics) {
	sv, d := types.MapValueFrom(ctx, types.StringType, values)
	if d.HasError() {
		for _, e := range d.Errors() {
			diags.AddError(e.Summary(), e.Detail())
		}
		return
	}
	vv, d := types.MapValueFrom(ctx, types.Int64Type, versions)
	if d.HasError() {
		for _, e := range d.Errors() {
			diags.AddError(e.Summary(), e.Detail())
		}
		return
	}
	model.Secrets = sv
	model.ValueVersions = vv
}
