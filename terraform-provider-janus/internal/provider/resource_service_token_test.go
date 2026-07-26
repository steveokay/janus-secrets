package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/steveokay/janus-secrets/terraform-provider-janus/internal/client"
)

// An environment-scoped token must mint with kind="environment" — Janus scopes
// service tokens to a config OR an environment, never a project or instance.
func TestServiceTokenMintsEnvironmentScope(t *testing.T) {
	const rawToken = "janus_svc_env_placeholder_xyz"
	var mintBody map[string]any
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/tokens":
			_ = json.NewDecoder(r.Body).Decode(&mintBody)
			writeJSON(w, http.StatusOK, client.MintedToken{
				Token: rawToken, ID: "tok-env", Name: "ci-env",
				Scope: client.TokenScope{Kind: "environment", ID: "env-1"}, Access: "readwrite",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tokens":
			writeJSON(w, http.StatusOK, map[string]any{
				"tokens": []client.TokenMeta{
					{ID: "tok-env", Name: "ci-env", ScopeKind: "environment", ScopeID: "env-1", Access: "readwrite"},
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
			"name":       str("ci-env"),
			"scope_kind": str("environment"),
			"scope":      str("env-1"),
			"access":     str("readwrite"),
		}),
	}, &createResp)
	fatalDiags(t, createResp.Diagnostics)

	scope, _ := mintBody["scope"].(map[string]any)
	if scope["kind"] != "environment" || scope["id"] != "env-1" {
		t.Fatalf("mint scope = %v, want kind=environment id=env-1", scope)
	}
	var created serviceTokenModel
	fatalDiags(t, createResp.State.Get(context.Background(), &created))
	if created.ScopeKind.ValueString() != "environment" {
		t.Errorf("scope_kind = %q", created.ScopeKind.ValueString())
	}
	if created.Token.ValueString() != rawToken {
		t.Errorf("minted token not recorded")
	}

	// Read refreshes scope_kind from the server.
	readResp := resource.ReadResponse{State: tfsdk.State{Schema: s}}
	r.Read(context.Background(), resource.ReadRequest{
		State: stateFrom(t, s, map[string]attr.Value{
			"id": str("tok-env"), "name": str("ci-env"), "scope_kind": str("environment"),
			"scope": str("env-1"), "access": str("readwrite"), "token": str(rawToken),
		}),
	}, &readResp)
	fatalDiags(t, readResp.Diagnostics)
	var read serviceTokenModel
	fatalDiags(t, readResp.State.Get(context.Background(), &read))
	if read.ScopeKind.ValueString() != "environment" {
		t.Errorf("scope_kind after read = %q", read.ScopeKind.ValueString())
	}
}

// Omitting scope_kind keeps the pre-existing behaviour (config scope), so state
// written by an older provider version does not plan a replacement.
func TestServiceTokenDefaultsToConfigScope(t *testing.T) {
	var mintBody map[string]any
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&mintBody)
		writeJSON(w, http.StatusOK, client.MintedToken{Token: "janus_svc_x", ID: "tok-1"})
	}))
	r := &serviceTokenResource{client: c}
	s := resSchema(t, r)

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"name": str("ci"), "scope": str("cfg-1"), "access": str("read"),
		}),
	}, &createResp)
	fatalDiags(t, createResp.Diagnostics)

	scope, _ := mintBody["scope"].(map[string]any)
	if scope["kind"] != "config" {
		t.Fatalf("default scope kind = %v, want config", scope["kind"])
	}
	var created serviceTokenModel
	fatalDiags(t, createResp.State.Get(context.Background(), &created))
	if created.ScopeKind.ValueString() != "config" {
		t.Errorf("scope_kind = %q, want config", created.ScopeKind.ValueString())
	}
}

// An invalid scope kind must never reach the API.
func TestServiceTokenRejectsInvalidScopeKindWithoutCallingAPI(t *testing.T) {
	var calls atomic.Int32
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeJSON(w, http.StatusOK, client.MintedToken{Token: "janus_svc_x", ID: "tok-1"})
	}))
	r := &serviceTokenResource{client: c}
	s := resSchema(t, r)

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"name": str("ci"), "scope_kind": str("project"), "scope": str("p-1"), "access": str("read"),
		}),
	}, &createResp)

	if !createResp.Diagnostics.HasError() {
		t.Fatal("expected an error for scope_kind=project (Janus has no project-scoped tokens)")
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("API called %d times; an invalid scope kind must fail before any request", got)
	}
}

// The same rule is enforced at PLAN time by an attribute validator, so `terraform
// plan` fails locally instead of round-tripping to Janus for a 400.
func TestScopeKindValidatorRunsAtPlanTime(t *testing.T) {
	s := resSchema(t, NewServiceTokenResource())
	attrDef, ok := s.Attributes["scope_kind"].(resschema.StringAttribute)
	if !ok {
		t.Fatal("scope_kind attribute missing")
	}
	if len(attrDef.Validators) == 0 {
		t.Fatal("scope_kind must carry a plan-time validator")
	}

	for _, tc := range []struct {
		name    string
		value   types.String
		wantErr bool
	}{
		{"config", types.StringValue("config"), false},
		{"environment", types.StringValue("environment"), false},
		{"project", types.StringValue("project"), true},
		{"empty", types.StringValue(""), true},
		{"null", types.StringNull(), false},
		{"unknown", types.StringUnknown(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var resp validator.StringResponse
			attrDef.Validators[0].ValidateString(context.Background(), validator.StringRequest{
				Path:        path.Root("scope_kind"),
				ConfigValue: tc.value,
			}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("HasError = %v, want %v (%v)", resp.Diagnostics.HasError(), tc.wantErr, resp.Diagnostics)
			}
			if tc.wantErr {
				detail := resp.Diagnostics.Errors()[0].Detail()
				if !strings.Contains(detail, `"config"`) || !strings.Contains(detail, `"environment"`) {
					t.Errorf("error should name the allowed kinds, got %q", detail)
				}
			}
		})
	}
}
