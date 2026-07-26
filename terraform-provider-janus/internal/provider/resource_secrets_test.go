package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// --- fixtures & helpers -----------------------------------------------------

// batchRequest mirrors the wire body of PUT /v1/configs/{cid}/secrets.
type batchRequest struct {
	Message string `json:"message"`
	Changes []struct {
		Key    string `json:"key"`
		Value  string `json:"value"`
		Delete bool   `json:"delete"`
	} `json:"changes"`
}

// batchRecorder is a fake Janus that serves the masked list and records every
// batch write, so tests can assert how MANY writes an apply performed.
type batchRecorder struct {
	mu sync.Mutex
	// masked is the value-free list the fake returns; tests mutate it to
	// simulate out-of-band writes/deletes.
	masked map[string]map[string]any
	writes []batchRequest
	// status overrides the batch-write response status (e.g. 202 for a
	// protected/four-eyes config).
	status int
	// listErr, when non-zero, makes the masked list fail with that status.
	listErr int
}

func newBatchRecorder(masked map[string]map[string]any) *batchRecorder {
	if masked == nil {
		masked = map[string]map[string]any{}
	}
	return &batchRecorder{masked: masked}
}

func maskedEntry(version int, origin string) map[string]any {
	return map[string]any{"value_version": version, "origin": origin, "type": "string"}
}

func (b *batchRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		if b.listErr != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(b.listErr)
			_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"gone"}}`)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"secrets": b.masked})
	case http.MethodPut:
		var req batchRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		b.writes = append(b.writes, req)
		// Reflect the batch into the masked view so a later list is coherent.
		for _, ch := range req.Changes {
			if ch.Delete {
				delete(b.masked, ch.Key)
				continue
			}
			prev := 0
			if e, ok := b.masked[ch.Key]; ok {
				prev, _ = e["value_version"].(int)
			}
			b.masked[ch.Key] = maskedEntry(prev+1, "own")
		}
		status := b.status
		if status == 0 {
			status = http.StatusOK
		}
		if status == http.StatusAccepted {
			writeJSON(w, status, map[string]any{"edit_request_id": "er-1", "status": "pending"})
			return
		}
		writeJSON(w, status, map[string]any{"version": 7, "id": "cv-7", "created_at": "2026-07-26T00:00:00Z"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{})
	}
}

func (b *batchRecorder) writeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.writes)
}

func (b *batchRecorder) lastWrite(t *testing.T) batchRequest {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.writes) == 0 {
		t.Fatal("no batch write recorded")
	}
	return b.writes[len(b.writes)-1]
}

func strMap(m map[string]string) attr.Value {
	elems := map[string]attr.Value{}
	for k, v := range m {
		elems[k] = types.StringValue(v)
	}
	return types.MapValueMust(types.StringType, elems)
}

func verMap(m map[string]int64) attr.Value {
	elems := map[string]attr.Value{}
	for k, v := range m {
		elems[k] = types.Int64Value(v)
	}
	return types.MapValueMust(types.Int64Type, elems)
}

func secretsOf(t *testing.T, s tfsdk.State) map[string]string {
	t.Helper()
	var m secretsModel
	fatalDiags(t, s.Get(context.Background(), &m))
	out := map[string]string{}
	if m.Secrets.IsNull() {
		return out
	}
	fatalDiags(t, m.Secrets.ElementsAs(context.Background(), &out, false))
	return out
}

// --- create -----------------------------------------------------------------

// The whole point of janus_secrets: N keys, ONE write, ONE config version.
func TestSecretsResourceCreateIssuesSingleBatchedWrite(t *testing.T) {
	rec := newBatchRecorder(nil)
	c := fakeJanus(t, rec)
	r := &secretsResource{client: c}
	s := resSchema(t, r)

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"config_id": str("cfg-1"),
			"message":   str("seed prod"),
			"secrets": strMap(map[string]string{
				"API_KEY":      "placeholder-api-key",
				"DATABASE_URL": "postgres://placeholder/db",
				"STRIPE_KEY":   "placeholder-stripe",
			}),
		}),
	}, &createResp)
	fatalDiags(t, createResp.Diagnostics)

	if got := rec.writeCount(); got != 1 {
		t.Fatalf("batch writes = %d, want exactly 1 (three keys must share one config version)", got)
	}
	w := rec.lastWrite(t)
	if w.Message != "seed prod" {
		t.Errorf("message = %q", w.Message)
	}
	if len(w.Changes) != 3 {
		t.Fatalf("changes = %d, want 3", len(w.Changes))
	}
	// Deterministic (sorted) ordering keeps request bodies reviewable.
	wantOrder := []string{"API_KEY", "DATABASE_URL", "STRIPE_KEY"}
	for i, ch := range w.Changes {
		if ch.Key != wantOrder[i] {
			t.Errorf("changes[%d].key = %q, want %q", i, ch.Key, wantOrder[i])
		}
		if ch.Delete {
			t.Errorf("changes[%d] should not be a delete", i)
		}
	}

	var created secretsModel
	fatalDiags(t, createResp.State.Get(context.Background(), &created))
	if created.ID.ValueString() != "cfg-1" {
		t.Errorf("id = %q, want cfg-1", created.ID.ValueString())
	}
	if created.ConfigVersion.ValueInt64() != 7 {
		t.Errorf("config_version = %d, want 7", created.ConfigVersion.ValueInt64())
	}
	versions := map[string]int64{}
	fatalDiags(t, created.ValueVersions.ElementsAs(context.Background(), &versions, false))
	if len(versions) != 3 || versions["API_KEY"] != 1 {
		t.Errorf("value_versions = %v, want a version per key", versions)
	}
}

// A key that already lives in the config (e.g. owned by a janus_secret
// resource) must fail loudly rather than be silently clobbered.
func TestSecretsResourceCreateRefusesToClobberExistingKeys(t *testing.T) {
	rec := newBatchRecorder(map[string]map[string]any{
		"DATABASE_URL": maskedEntry(4, "own"),
	})
	c := fakeJanus(t, rec)
	r := &secretsResource{client: c}
	s := resSchema(t, r)

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"config_id": str("cfg-1"),
			"message":   str("seed"),
			"secrets": strMap(map[string]string{
				"API_KEY":      "placeholder-api-key",
				"DATABASE_URL": "postgres://placeholder/db",
			}),
		}),
	}, &createResp)

	if !createResp.Diagnostics.HasError() {
		t.Fatal("expected an error when adopting a key that already exists")
	}
	if rec.writeCount() != 0 {
		t.Errorf("nothing must be written when the guard trips, got %d writes", rec.writeCount())
	}
	if got := createResp.Diagnostics.Errors()[0].Detail(); !strings.Contains(got, "DATABASE_URL") {
		t.Errorf("error should name the clashing key, got %q", got)
	}
}

// A key visible only through config inheritance is NOT owned by this config, so
// writing it is a legitimate override, not a clobber.
func TestSecretsResourceCreateAllowsOverridingInheritedKey(t *testing.T) {
	rec := newBatchRecorder(map[string]map[string]any{
		"DATABASE_URL": maskedEntry(2, "inherited"),
	})
	c := fakeJanus(t, rec)
	r := &secretsResource{client: c}
	s := resSchema(t, r)

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"config_id": str("cfg-1"),
			"secrets":   strMap(map[string]string{"DATABASE_URL": "postgres://placeholder/override"}),
			"message":   str("override"),
		}),
	}, &createResp)
	fatalDiags(t, createResp.Diagnostics)
	if rec.writeCount() != 1 {
		t.Errorf("writes = %d, want 1", rec.writeCount())
	}
}

// A protected (four-eyes) config answers 202: the batch is queued, NOT
// committed, so the apply must fail rather than record a phantom write.
func TestSecretsResourceCreateFailsOnApprovalRequired(t *testing.T) {
	rec := newBatchRecorder(nil)
	rec.status = http.StatusAccepted
	c := fakeJanus(t, rec)
	r := &secretsResource{client: c}
	s := resSchema(t, r)

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"config_id": str("cfg-1"),
			"message":   str("seed"),
			"secrets":   strMap(map[string]string{"API_KEY": "placeholder"}),
		}),
	}, &createResp)
	if !createResp.Diagnostics.HasError() {
		t.Fatal("202 (pending approval) must surface as an error, not a silent success")
	}
}

// --- update -----------------------------------------------------------------

// Add + change + remove in one plan must still be exactly one request, with the
// removal expressed as a tombstone in the same batch.
func TestSecretsResourceUpdateBatchesAddChangeAndTombstone(t *testing.T) {
	rec := newBatchRecorder(map[string]map[string]any{
		"API_KEY":      maskedEntry(1, "own"),
		"DATABASE_URL": maskedEntry(1, "own"),
		"OLD_KEY":      maskedEntry(1, "own"),
	})
	c := fakeJanus(t, rec)
	r := &secretsResource{client: c}
	s := resSchema(t, r)

	prior := map[string]attr.Value{
		"id":        str("cfg-1"),
		"config_id": str("cfg-1"),
		"message":   str("terraform apply"),
		"secrets": strMap(map[string]string{
			"API_KEY":      "placeholder-api-key",
			"DATABASE_URL": "postgres://placeholder/db",
			"OLD_KEY":      "placeholder-old",
		}),
		"value_versions": verMap(map[string]int64{"API_KEY": 1, "DATABASE_URL": 1, "OLD_KEY": 1}),
		"config_version": types.Int64Value(6),
	}
	planned := map[string]attr.Value{
		"id":        str("cfg-1"),
		"config_id": str("cfg-1"),
		"message":   str("rotate"),
		"secrets": strMap(map[string]string{
			"API_KEY":      "placeholder-api-key",          // unchanged
			"DATABASE_URL": "postgres://placeholder/db-v2", // changed
			"NEW_KEY":      "placeholder-new",              // added
			// OLD_KEY removed
		}),
	}

	updResp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFrom(t, s, planned),
		State: stateFrom(t, s, prior),
	}, &updResp)
	fatalDiags(t, updResp.Diagnostics)

	if got := rec.writeCount(); got != 1 {
		t.Fatalf("batch writes = %d, want exactly 1", got)
	}
	w := rec.lastWrite(t)
	got := map[string]bool{} // key -> isDelete
	for _, ch := range w.Changes {
		got[ch.Key] = ch.Delete
	}
	if len(got) != 3 {
		t.Fatalf("changes = %+v, want DATABASE_URL, NEW_KEY, OLD_KEY only", w.Changes)
	}
	if _, ok := got["API_KEY"]; ok {
		t.Error("unchanged key must not be rewritten")
	}
	if del, ok := got["OLD_KEY"]; !ok || !del {
		t.Error("removing a key from the map must tombstone it (delete:true) in the same batch")
	}
	if del, ok := got["NEW_KEY"]; !ok || del {
		t.Error("added key must be written")
	}
	if del, ok := got["DATABASE_URL"]; !ok || del {
		t.Error("changed key must be written")
	}

	// State must reflect the new set exactly: unchanged keys survive, the
	// tombstoned one is gone. (Anything else is "inconsistent result after apply".)
	after := secretsOf(t, updResp.State)
	if _, ok := after["OLD_KEY"]; ok {
		t.Error("tombstoned key must leave state")
	}
	if after["NEW_KEY"] != "placeholder-new" {
		t.Errorf("added key missing from state: %v", after)
	}
	if after["API_KEY"] != "placeholder-api-key" {
		t.Errorf("unchanged key must remain in state, got %v", after)
	}
	if after["DATABASE_URL"] != "postgres://placeholder/db-v2" {
		t.Errorf("changed key not updated in state: %v", after)
	}
	if len(after) != 3 {
		t.Errorf("state should hold exactly the configured map, got %v", after)
	}
	var afterModel secretsModel
	fatalDiags(t, updResp.State.Get(context.Background(), &afterModel))
	ledger := map[string]int64{}
	fatalDiags(t, afterModel.ValueVersions.ElementsAs(context.Background(), &ledger, false))
	if len(ledger) != 3 || ledger["API_KEY"] == 0 {
		t.Errorf("drift ledger must cover every managed key, got %v", ledger)
	}
}

// Changing only `message` has nothing to commit; the batch endpoint rejects an
// empty change set, so the provider must not call it at all.
func TestSecretsResourceUpdateWithNoValueChangesWritesNothing(t *testing.T) {
	rec := newBatchRecorder(map[string]map[string]any{"API_KEY": maskedEntry(3, "own")})
	c := fakeJanus(t, rec)
	r := &secretsResource{client: c}
	s := resSchema(t, r)

	same := strMap(map[string]string{"API_KEY": "placeholder-api-key"})
	updResp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"id": str("cfg-1"), "config_id": str("cfg-1"), "message": str("new message"), "secrets": same,
		}),
		State: stateFrom(t, s, map[string]attr.Value{
			"id": str("cfg-1"), "config_id": str("cfg-1"), "message": str("old message"), "secrets": same,
			"value_versions": verMap(map[string]int64{"API_KEY": 3}),
			"config_version": types.Int64Value(5),
		}),
	}, &updResp)
	fatalDiags(t, updResp.Diagnostics)

	if rec.writeCount() != 0 {
		t.Errorf("writes = %d, want 0 for a no-op change set", rec.writeCount())
	}
	var after secretsModel
	fatalDiags(t, updResp.State.Get(context.Background(), &after))
	if after.ConfigVersion.ValueInt64() != 5 {
		t.Errorf("config_version = %d, want the prior 5 (nothing was committed)", after.ConfigVersion.ValueInt64())
	}
	versions := map[string]int64{}
	fatalDiags(t, after.ValueVersions.ElementsAs(context.Background(), &versions, false))
	if versions["API_KEY"] != 3 {
		t.Errorf("value_versions = %v, want the ledger carried forward", versions)
	}
}

// --- read / drift -----------------------------------------------------------

func TestSecretsResourceReadDetectsDriftFromValueVersions(t *testing.T) {
	rec := newBatchRecorder(map[string]map[string]any{
		"STABLE":    maskedEntry(2, "own"),
		"TOUCHED":   maskedEntry(9, "own"), // bumped out of band (state says 4)
		"INHERITED": maskedEntry(1, "inherited"),
		// "REMOVED" is absent entirely.
	})
	c := fakeJanus(t, rec)
	r := &secretsResource{client: c}
	s := resSchema(t, r)

	readResp := resource.ReadResponse{State: tfsdk.State{Schema: s}}
	r.Read(context.Background(), resource.ReadRequest{
		State: stateFrom(t, s, map[string]attr.Value{
			"id":        str("cfg-1"),
			"config_id": str("cfg-1"),
			"message":   str("terraform apply"),
			"secrets": strMap(map[string]string{
				"STABLE":    "placeholder-stable",
				"TOUCHED":   "placeholder-touched",
				"INHERITED": "placeholder-inherited",
				"REMOVED":   "placeholder-removed",
			}),
			"value_versions": verMap(map[string]int64{"STABLE": 2, "TOUCHED": 4, "INHERITED": 1, "REMOVED": 1}),
			"config_version": types.Int64Value(6),
		}),
	}, &readResp)
	fatalDiags(t, readResp.Diagnostics)

	after := secretsOf(t, readResp.State)
	if after["STABLE"] != "placeholder-stable" {
		t.Error("an untouched key must stay in state (no drift)")
	}
	if _, ok := after["TOUCHED"]; ok {
		t.Error("a key whose value_version moved must be dropped so the next plan rewrites it")
	}
	if _, ok := after["REMOVED"]; ok {
		t.Error("a key deleted out of band must be dropped from state")
	}
	if _, ok := after["INHERITED"]; ok {
		t.Error("a key that survives only via inheritance is not stored here; it must be dropped")
	}
}

// End-to-end drift loop: Read drops a key whose version moved, and the follow-up
// Update must REASSERT it — not trip the "already exists" guard, which is only
// meant for keys this resource never managed.
func TestSecretsResourceUpdateReassertsDriftedKey(t *testing.T) {
	rec := newBatchRecorder(map[string]map[string]any{
		"API_KEY": maskedEntry(9, "own"), // someone wrote it outside Terraform
	})
	c := fakeJanus(t, rec)
	r := &secretsResource{client: c}
	s := resSchema(t, r)

	desired := strMap(map[string]string{"API_KEY": "placeholder-api-key"})
	prior := map[string]attr.Value{
		"id": str("cfg-1"), "config_id": str("cfg-1"), "message": str("terraform apply"),
		"secrets":        strMap(map[string]string{"API_KEY": "placeholder-api-key"}),
		"value_versions": verMap(map[string]int64{"API_KEY": 4}),
		"config_version": types.Int64Value(6),
	}

	readResp := resource.ReadResponse{State: stateFrom(t, s, prior)}
	r.Read(context.Background(), resource.ReadRequest{State: stateFrom(t, s, prior)}, &readResp)
	fatalDiags(t, readResp.Diagnostics)
	if _, ok := secretsOf(t, readResp.State)["API_KEY"]; ok {
		t.Fatal("precondition: the drifted key should have been dropped by Read")
	}

	// `terraform plan` then `terraform apply` both refresh, so Read runs twice
	// over the already-dropped value. The ownership ledger must survive that.
	secondRead := resource.ReadResponse{State: readResp.State}
	r.Read(context.Background(), resource.ReadRequest{State: readResp.State}, &secondRead)
	fatalDiags(t, secondRead.Diagnostics)
	var afterRead secretsModel
	fatalDiags(t, secondRead.State.Get(context.Background(), &afterRead))
	ledger := map[string]int64{}
	fatalDiags(t, afterRead.ValueVersions.ElementsAs(context.Background(), &ledger, false))
	if ledger["API_KEY"] != 9 {
		t.Fatalf("ledger = %v, want the key still tracked at version 9 after a repeated refresh", ledger)
	}

	updResp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"id": str("cfg-1"), "config_id": str("cfg-1"), "message": str("terraform apply"), "secrets": desired,
		}),
		State: secondRead.State,
	}, &updResp)
	fatalDiags(t, updResp.Diagnostics)

	if rec.writeCount() != 1 {
		t.Fatalf("writes = %d, want 1 (the drifted key must be rewritten)", rec.writeCount())
	}
	if got := secretsOf(t, updResp.State)["API_KEY"]; got != "placeholder-api-key" {
		t.Errorf("state value = %q, want the configured value reasserted", got)
	}
}

// Removing a key that had drifted (value dropped from state, ledger kept) must
// still tombstone it — ownership comes from the ledger, not just the value map.
func TestSecretsResourceUpdateTombstonesDriftedKeyRemovedFromMap(t *testing.T) {
	rec := newBatchRecorder(map[string]map[string]any{
		"GONE_SOON": maskedEntry(9, "own"),
		"KEEP":      maskedEntry(1, "own"),
	})
	c := fakeJanus(t, rec)
	r := &secretsResource{client: c}
	s := resSchema(t, r)

	updResp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"id": str("cfg-1"), "config_id": str("cfg-1"), "message": str("terraform apply"),
			"secrets": strMap(map[string]string{"KEEP": "placeholder-keep"}),
		}),
		// GONE_SOON is in the ledger only — its value was dropped by a prior Read.
		State: stateFrom(t, s, map[string]attr.Value{
			"id": str("cfg-1"), "config_id": str("cfg-1"), "message": str("terraform apply"),
			"secrets":        strMap(map[string]string{"KEEP": "placeholder-keep"}),
			"value_versions": verMap(map[string]int64{"KEEP": 1, "GONE_SOON": 9}),
			"config_version": types.Int64Value(6),
		}),
	}, &updResp)
	fatalDiags(t, updResp.Diagnostics)

	if rec.writeCount() != 1 {
		t.Fatalf("writes = %d, want 1", rec.writeCount())
	}
	changes := rec.lastWrite(t).Changes
	if len(changes) != 1 || changes[0].Key != "GONE_SOON" || !changes[0].Delete {
		t.Fatalf("changes = %+v, want a single GONE_SOON tombstone", changes)
	}
}

func TestSecretsResourceReadRemovesResourceWhenConfigGone(t *testing.T) {
	rec := newBatchRecorder(nil)
	rec.listErr = http.StatusNotFound
	c := fakeJanus(t, rec)
	r := &secretsResource{client: c}
	s := resSchema(t, r)

	base := map[string]attr.Value{
		"id": str("cfg-1"), "config_id": str("cfg-1"),
		"secrets":        strMap(map[string]string{"API_KEY": "placeholder"}),
		"value_versions": verMap(map[string]int64{"API_KEY": 1}),
	}
	readResp := resource.ReadResponse{State: stateFrom(t, s, base)}
	r.Read(context.Background(), resource.ReadRequest{State: stateFrom(t, s, base)}, &readResp)
	fatalDiags(t, readResp.Diagnostics)
	if !readResp.State.Raw.IsNull() {
		t.Error("expected the resource to leave state when the config 404s")
	}
}

// --- delete -----------------------------------------------------------------

func TestSecretsResourceDeleteTombstonesEveryKeyInOneBatch(t *testing.T) {
	rec := newBatchRecorder(map[string]map[string]any{
		"A": maskedEntry(1, "own"), "B": maskedEntry(1, "own"),
	})
	c := fakeJanus(t, rec)
	r := &secretsResource{client: c}
	s := resSchema(t, r)

	delResp := resource.DeleteResponse{State: tfsdk.State{Schema: s}}
	r.Delete(context.Background(), resource.DeleteRequest{
		State: stateFrom(t, s, map[string]attr.Value{
			"id": str("cfg-1"), "config_id": str("cfg-1"),
			"secrets":        strMap(map[string]string{"A": "placeholder-a", "B": "placeholder-b"}),
			"value_versions": verMap(map[string]int64{"A": 1, "B": 1}),
		}),
	}, &delResp)
	fatalDiags(t, delResp.Diagnostics)

	if rec.writeCount() != 1 {
		t.Fatalf("writes = %d, want 1 (destroy is one config version too)", rec.writeCount())
	}
	for _, ch := range rec.lastWrite(t).Changes {
		if !ch.Delete {
			t.Errorf("change %q should be a tombstone", ch.Key)
		}
		if ch.Value != "" {
			t.Errorf("a tombstone must not carry a value (%q)", ch.Key)
		}
	}
}
