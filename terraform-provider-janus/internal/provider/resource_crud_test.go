package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/steveokay/janus-secrets/terraform-provider-janus/internal/client"
)

// fakeJanus wires a *client.Client to a test HTTP handler and returns both the
// client and a shared config-configure request carrying it.
func fakeJanus(t *testing.T, h http.Handler) *client.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := client.New(srv.URL, "janus_svc_test", &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return c
}

func resSchema(t *testing.T, r resource.Resource) resschema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// planFrom builds a tfsdk.Plan from an object of attribute values.
func planFrom(t *testing.T, s resschema.Schema, obj map[string]attr.Value) tfsdk.Plan {
	t.Helper()
	raw := objectToTFValue(t, s, obj)
	return tfsdk.Plan{Schema: s, Raw: raw}
}

func stateFrom(t *testing.T, s resschema.Schema, obj map[string]attr.Value) tfsdk.State {
	t.Helper()
	raw := objectToTFValue(t, s, obj)
	return tfsdk.State{Schema: s, Raw: raw}
}

func objectToTFValue(t *testing.T, s resschema.Schema, obj map[string]attr.Value) tftypes.Value {
	t.Helper()
	objType := s.Type().TerraformType(context.Background()).(tftypes.Object)
	vals := map[string]tftypes.Value{}
	for name := range objType.AttributeTypes {
		v, ok := obj[name]
		if !ok {
			// Unset attributes become null of their type.
			at := objType.AttributeTypes[name]
			vals[name] = tftypes.NewValue(at, nil)
			continue
		}
		tv, err := v.ToTerraformValue(context.Background())
		if err != nil {
			t.Fatalf("ToTerraformValue(%s): %v", name, err)
		}
		vals[name] = tv
	}
	return tftypes.NewValue(objType, vals)
}

func str(s string) attr.Value { return types.StringValue(s) }

func fatalDiags(t *testing.T, d diag.Diagnostics) {
	t.Helper()
	if d.HasError() {
		t.Fatalf("diagnostics: %v", d)
	}
}

// --- janus_project CRUD ---

func TestProjectResourceCreateReadDelete(t *testing.T) {
	var deleted bool
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/projects":
			b, _ := io.ReadAll(r.Body)
			var body map[string]string
			_ = json.Unmarshal(b, &body)
			if body["slug"] != "acme" {
				t.Errorf("slug = %q", body["slug"])
			}
			writeJSON(w, http.StatusCreated, client.Project{ID: "p-1", Slug: "acme", Name: "Acme"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/p-1":
			writeJSON(w, http.StatusOK, client.Project{ID: "p-1", Slug: "acme", Name: "Acme"})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/projects/p-1":
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))

	r := &projectResource{client: c}
	s := resSchema(t, r)

	// Create
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: s}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"slug": str("acme"),
			"name": str("Acme"),
		}),
	}, &createResp)
	fatalDiags(t, createResp.Diagnostics)

	var created projectModel
	fatalDiags(t, createResp.State.Get(context.Background(), &created))
	if created.ID.ValueString() != "p-1" {
		t.Fatalf("created id = %q", created.ID.ValueString())
	}

	// Read
	var readResp resource.ReadResponse
	readResp.State = tfsdk.State{Schema: s}
	r.Read(context.Background(), resource.ReadRequest{
		State: stateFrom(t, s, map[string]attr.Value{
			"id":   str("p-1"),
			"slug": str("acme"),
			"name": str("Acme"),
		}),
	}, &readResp)
	fatalDiags(t, readResp.Diagnostics)

	// Delete
	var delResp resource.DeleteResponse
	delResp.State = tfsdk.State{Schema: s}
	r.Delete(context.Background(), resource.DeleteRequest{
		State: stateFrom(t, s, map[string]attr.Value{
			"id":   str("p-1"),
			"slug": str("acme"),
			"name": str("Acme"),
		}),
	}, &delResp)
	fatalDiags(t, delResp.Diagnostics)
	if !deleted {
		t.Error("delete not called")
	}
}

func TestProjectResourceReadDrift(t *testing.T) {
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"gone"}}`)
	}))
	r := &projectResource{client: c}
	s := resSchema(t, r)

	readResp := resource.ReadResponse{State: stateFrom(t, s, map[string]attr.Value{
		"id":   str("p-1"),
		"slug": str("acme"),
		"name": str("Acme"),
	})}
	r.Read(context.Background(), resource.ReadRequest{
		State: stateFrom(t, s, map[string]attr.Value{
			"id":   str("p-1"),
			"slug": str("acme"),
			"name": str("Acme"),
		}),
	}, &readResp)
	fatalDiags(t, readResp.Diagnostics)
	if !readResp.State.Raw.IsNull() {
		t.Error("expected state removed (null) after 404 drift")
	}
}

// --- janus_secret CRUD (sensitive value round-trip) ---

func TestSecretResourceCreateReadRoundTrip(t *testing.T) {
	const want = "s3cr3t-placeholder-value"
	var putValue string
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			var body map[string]string
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			putValue = body["value"]
			writeJSON(w, http.StatusOK, map[string]any{"version": 1, "id": "v-1"})
		case http.MethodGet:
			if r.URL.Query().Get("raw") != "true" {
				t.Errorf("expected raw=true, got %s", r.URL.RawQuery)
			}
			writeJSON(w, http.StatusOK, map[string]string{"key": "API_KEY", "value": want})
		default:
			t.Errorf("unexpected %s", r.Method)
		}
	}))
	r := &secretResource{client: c}
	s := resSchema(t, r)

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"config_id": str("cfg-1"),
			"key":       str("API_KEY"),
			"value":     str(want),
		}),
	}, &createResp)
	fatalDiags(t, createResp.Diagnostics)
	if putValue != want {
		t.Errorf("PUT value = %q, want %q", putValue, want)
	}
	var created secretModel
	fatalDiags(t, createResp.State.Get(context.Background(), &created))
	if created.ID.ValueString() != "cfg-1/API_KEY" {
		t.Errorf("id = %q", created.ID.ValueString())
	}

	readResp := resource.ReadResponse{State: tfsdk.State{Schema: s}}
	r.Read(context.Background(), resource.ReadRequest{
		State: stateFrom(t, s, map[string]attr.Value{
			"id":        str("cfg-1/API_KEY"),
			"config_id": str("cfg-1"),
			"key":       str("API_KEY"),
			"value":     str("stale"),
		}),
	}, &readResp)
	fatalDiags(t, readResp.Diagnostics)
	var read secretModel
	fatalDiags(t, readResp.State.Get(context.Background(), &read))
	if read.Value.ValueString() != want {
		t.Errorf("read value = %q, want %q", read.Value.ValueString(), want)
	}
}

// --- janus_service_token: token minted once, sensitive, preserved on read ---

func TestServiceTokenCreatePreservesRawTokenOnRead(t *testing.T) {
	const rawToken = "janus_svc_minted_placeholder_xyz"
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/tokens":
			writeJSON(w, http.StatusOK, client.MintedToken{
				Token:  rawToken,
				ID:     "tok-1",
				Name:   "ci",
				Scope:  client.TokenScope{Kind: "config", ID: "cfg-1"},
				Access: "read",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tokens":
			writeJSON(w, http.StatusOK, map[string]any{
				"tokens": []client.TokenMeta{
					{ID: "tok-1", Name: "ci", ScopeKind: "config", ScopeID: "cfg-1", Access: "read"},
				},
				"next_cursor": nil,
			})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	r := &serviceTokenResource{client: c}
	s := resSchema(t, r)

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"name":   str("ci"),
			"scope":  str("cfg-1"),
			"access": str("read"),
		}),
	}, &createResp)
	fatalDiags(t, createResp.Diagnostics)
	var created serviceTokenModel
	fatalDiags(t, createResp.State.Get(context.Background(), &created))
	if created.Token.ValueString() != rawToken {
		t.Fatalf("minted token = %q, want %q", created.Token.ValueString(), rawToken)
	}

	// Read must refresh metadata but NOT clobber the raw token.
	readResp := resource.ReadResponse{State: tfsdk.State{Schema: s}}
	r.Read(context.Background(), resource.ReadRequest{
		State: stateFrom(t, s, map[string]attr.Value{
			"id":     str("tok-1"),
			"name":   str("ci"),
			"scope":  str("cfg-1"),
			"access": str("read"),
			"token":  str(rawToken),
		}),
	}, &readResp)
	fatalDiags(t, readResp.Diagnostics)
	var read serviceTokenModel
	fatalDiags(t, readResp.State.Get(context.Background(), &read))
	if read.Token.ValueString() != rawToken {
		t.Errorf("raw token not preserved on read: got %q", read.Token.ValueString())
	}
}

// writeJSON is the resource-test JSON writer (no *testing.T dependency inside
// the handler closure beyond the outer scope).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
