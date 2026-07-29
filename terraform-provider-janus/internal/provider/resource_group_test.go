package provider

import (
	"context"
	"encoding/json"
	"io"
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

// obviously-fake fixtures
const (
	testGroupID = "grp-00000000-0000-0000-0000-000000000001"
	testUserID  = "usr-00000000-0000-0000-0000-000000000002"
	testProjID  = "prj-00000000-0000-0000-0000-000000000003"
	testEnvID   = "env-00000000-0000-0000-0000-000000000004"
)

func configFrom(t *testing.T, s resschema.Schema, obj map[string]attr.Value) tfsdk.Config {
	t.Helper()
	return tfsdk.Config{Schema: s, Raw: objectToTFValue(t, s, obj)}
}

func boolean(b bool) attr.Value { return types.BoolValue(b) }

// groupBody is the {"group":…} envelope GET /v1/groups/{gid} answers with.
func groupBody(kind string, claim *string, canCreate bool) map[string]any {
	return map[string]any{
		"group": client.Group{
			ID: testGroupID, Name: "Team Payments", Kind: kind,
			ClaimValue: claim, CanCreateProjects: canCreate,
		},
	}
}

// --- janus_group ---

func TestGroupResourceCreateReadDelete(t *testing.T) {
	var createBody map[string]any
	var deleted bool
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/groups":
			_ = json.NewDecoder(r.Body).Decode(&createBody)
			writeJSON(w, http.StatusCreated, client.Group{
				ID: testGroupID, Name: "Team Payments", Kind: "local",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/groups/"+testGroupID:
			writeJSON(w, http.StatusOK, groupBody("local", nil, false))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/groups/"+testGroupID:
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	r := &groupResource{client: c}
	s := resSchema(t, r)

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"name": str("Team Payments"), "kind": str("local"), "can_create_projects": boolean(false),
		}),
	}, &createResp)
	fatalDiags(t, createResp.Diagnostics)

	if createBody["kind"] != "local" || createBody["name"] != "Team Payments" {
		t.Fatalf("create body = %v", createBody)
	}
	// A local group must not ship a claim_value at all.
	if _, ok := createBody["claim_value"]; ok {
		t.Errorf("local group create sent claim_value: %v", createBody)
	}
	var created groupModel
	fatalDiags(t, createResp.State.Get(context.Background(), &created))
	if created.ID.ValueString() != testGroupID {
		t.Fatalf("id = %q", created.ID.ValueString())
	}
	if !created.ClaimValue.IsNull() {
		t.Errorf("claim_value should stay null for a local group, got %v", created.ClaimValue)
	}

	readResp := resource.ReadResponse{State: tfsdk.State{Schema: s}}
	r.Read(context.Background(), resource.ReadRequest{
		State: stateFrom(t, s, map[string]attr.Value{
			"id": str(testGroupID), "name": str("Team Payments"), "kind": str("local"),
			"can_create_projects": boolean(false),
		}),
	}, &readResp)
	fatalDiags(t, readResp.Diagnostics)

	delResp := resource.DeleteResponse{State: tfsdk.State{Schema: s}}
	r.Delete(context.Background(), resource.DeleteRequest{
		State: stateFrom(t, s, map[string]attr.Value{
			"id": str(testGroupID), "name": str("Team Payments"), "kind": str("local"),
			"can_create_projects": boolean(false),
		}),
	}, &delResp)
	fatalDiags(t, delResp.Diagnostics)
	if !deleted {
		t.Error("delete not called")
	}
}

// The two-kinds rule is enforced at PLAN time, against the raw config, so a
// mismatched kind/claim pair never reaches Janus for a 400.
func TestGroupValidateConfigEnforcesTwoKinds(t *testing.T) {
	r := &groupResource{}
	s := resSchema(t, r)

	for _, tc := range []struct {
		name    string
		cfg     map[string]attr.Value
		wantErr string // substring of the summary, "" = must be accepted
	}{
		{"local without claim", map[string]attr.Value{"name": str("t"), "kind": str("local")}, ""},
		{"oidc with claim", map[string]attr.Value{"name": str("t"), "kind": str("oidc"), "claim_value": str("g-1")}, ""},
		{
			"local with claim",
			map[string]attr.Value{"name": str("t"), "kind": str("local"), "claim_value": str("g-1")},
			"not valid for a local group",
		},
		{
			"oidc without claim",
			map[string]attr.Value{"name": str("t"), "kind": str("oidc")},
			"required for an oidc group",
		},
		{
			// Unknown (computed elsewhere) cannot be judged yet: stay silent and
			// let the Create pre-flight decide.
			"oidc with unknown claim",
			map[string]attr.Value{"name": str("t"), "kind": str("oidc"), "claim_value": types.StringUnknown()},
			"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := resource.ValidateConfigResponse{}
			r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{
				Config: configFrom(t, s, tc.cfg),
			}, &resp)
			if tc.wantErr == "" {
				fatalDiags(t, resp.Diagnostics)
				return
			}
			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected an error containing %q", tc.wantErr)
			}
			if got := resp.Diagnostics.Errors()[0].Summary(); !strings.Contains(got, tc.wantErr) {
				t.Errorf("summary = %q, want it to contain %q", got, tc.wantErr)
			}
		})
	}
}

// kind is additionally guarded by the shared plan-time enum validator.
func TestGroupKindValidatorRunsAtPlanTime(t *testing.T) {
	s := resSchema(t, NewGroupResource())
	attrDef, ok := s.Attributes["kind"].(resschema.StringAttribute)
	if !ok || len(attrDef.Validators) == 0 {
		t.Fatal("kind must carry a plan-time validator")
	}
	var resp validator.StringResponse
	attrDef.Validators[0].ValidateString(context.Background(), validator.StringRequest{
		Path: path.Root("kind"), ConfigValue: types.StringValue("ldap"),
	}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("kind=ldap should fail at plan time")
	}
}

// A mismatched kind/claim that only became concrete at apply time must still be
// refused BEFORE the API call.
func TestGroupCreateRejectsClaimMismatchWithoutCallingAPI(t *testing.T) {
	var calls atomic.Int32
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeJSON(w, http.StatusCreated, client.Group{ID: testGroupID})
	}))
	r := &groupResource{client: c}
	s := resSchema(t, r)

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"name": str("t"), "kind": str("oidc"), "can_create_projects": boolean(false),
		}),
	}, &createResp)

	if !createResp.Diagnostics.HasError() {
		t.Fatal("expected an error for an oidc group with no claim_value")
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("API called %d times; the mismatch must fail before any request", n)
	}
}

// can_create_projects is the ONLY in-place update, and it goes to the
// capabilities route rather than re-creating the group.
func TestGroupUpdateTogglesCapability(t *testing.T) {
	var capBody map[string]any
	var hits int
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/v1/groups/"+testGroupID+"/capabilities" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			return
		}
		hits++
		_ = json.NewDecoder(r.Body).Decode(&capBody)
		writeJSON(w, http.StatusOK, map[string]any{"can_create_projects": true})
	}))
	r := &groupResource{client: c}
	s := resSchema(t, r)

	attrs := func(can bool) map[string]attr.Value {
		return map[string]attr.Value{
			"id": str(testGroupID), "name": str("Team Payments"), "kind": str("local"),
			"can_create_projects": boolean(can),
		}
	}
	updResp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFrom(t, s, attrs(true)),
		State: stateFrom(t, s, attrs(false)),
	}, &updResp)
	fatalDiags(t, updResp.Diagnostics)

	if hits != 1 {
		t.Fatalf("capabilities route hit %d times, want 1", hits)
	}
	if capBody["can_create_projects"] != true {
		t.Errorf("capability body = %v", capBody)
	}
}

func TestGroupReadDrift(t *testing.T) {
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"gone"}}`)
	}))
	r := &groupResource{client: c}
	s := resSchema(t, r)
	state := stateFrom(t, s, map[string]attr.Value{
		"id": str(testGroupID), "name": str("t"), "kind": str("local"), "can_create_projects": boolean(false),
	})
	readResp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &readResp)
	fatalDiags(t, readResp.Diagnostics)
	if !readResp.State.Raw.IsNull() {
		t.Error("expected state removed after 404 drift")
	}
}

// --- janus_group_member ---

// The headline rule: a member of an IdP-fed group is refused at PLAN time,
// whenever the group id is concrete enough to look up.
func TestGroupMemberModifyPlanRefusesOIDCGroup(t *testing.T) {
	claim := "8f14e45f"
	var writes atomic.Int32
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		writeJSON(w, http.StatusOK, groupBody("oidc", &claim, false))
	}))
	r := &groupMemberResource{client: c}
	s := resSchema(t, r)

	resp := resource.ModifyPlanResponse{Plan: tfsdk.Plan{Schema: s}}
	r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"group_id": str(testGroupID), "user_id": str(testUserID),
		}),
	}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected a plan-time error for a member of an oidc group")
	}
	detail := resp.Diagnostics.Errors()[0].Detail()
	if !strings.Contains(detail, "identity provider") {
		t.Errorf("error should explain the IdP snapshot, got %q", detail)
	}
	if n := writes.Load(); n != 0 {
		t.Errorf("plan performed %d writes; it must only read", n)
	}
}

// A local group plans cleanly.
func TestGroupMemberModifyPlanAllowsLocalGroup(t *testing.T) {
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, groupBody("local", nil, false))
	}))
	r := &groupMemberResource{client: c}
	s := resSchema(t, r)

	resp := resource.ModifyPlanResponse{Plan: tfsdk.Plan{Schema: s}}
	r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"group_id": str(testGroupID), "user_id": str(testUserID),
		}),
	}, &resp)
	fatalDiags(t, resp.Diagnostics)
}

// A lookup that fails must not break `terraform plan` — the group may simply be
// created later in the same graph, or the token may not read the catalog.
func TestGroupMemberModifyPlanSilentOnLookupFailure(t *testing.T) {
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"code":"forbidden","message":"nope"}}`)
	}))
	r := &groupMemberResource{client: c}
	s := resSchema(t, r)

	resp := resource.ModifyPlanResponse{Plan: tfsdk.Plan{Schema: s}}
	r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"group_id": str(testGroupID), "user_id": str(testUserID),
		}),
	}, &resp)
	fatalDiags(t, resp.Diagnostics)
}

// A destroy plan carries a null plan; ModifyPlan must not touch it.
func TestGroupMemberModifyPlanIgnoresDestroy(t *testing.T) {
	c := fakeJanus(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("destroy plan must not call the API (%s %s)", r.Method, r.URL.Path)
	}))
	r := &groupMemberResource{client: c}
	s := resSchema(t, r)

	resp := resource.ModifyPlanResponse{}
	r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{
		Plan: tfsdk.Plan{Schema: s},
	}, &resp)
	fatalDiags(t, resp.Diagnostics)
}

// When the group id was unknown at plan time, Create still refuses an oidc group
// BEFORE writing anything.
func TestGroupMemberCreateRefusesOIDCBeforeWriting(t *testing.T) {
	claim := "8f14e45f"
	var puts atomic.Int32
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, groupBody("oidc", &claim, false))
	}))
	r := &groupMemberResource{client: c}
	s := resSchema(t, r)

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"group_id": str(testGroupID), "user_id": str(testUserID),
		}),
	}, &createResp)

	if !createResp.Diagnostics.HasError() {
		t.Fatal("expected an error adding a member to an oidc group")
	}
	if n := puts.Load(); n != 0 {
		t.Errorf("membership PUT issued %d times; it must not be attempted", n)
	}
}

func TestGroupMemberCreateReadDelete(t *testing.T) {
	memberPath := "/v1/groups/" + testGroupID + "/members/" + testUserID
	var added, removed bool
	present := true
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/groups/"+testGroupID:
			writeJSON(w, http.StatusOK, groupBody("local", nil, false))
		case r.Method == http.MethodPut && r.URL.Path == memberPath:
			added = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == memberPath:
			removed = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/groups/"+testGroupID+"/members":
			members := []map[string]any{}
			if present {
				members = append(members, map[string]any{"user_id": testUserID})
			}
			writeJSON(w, http.StatusOK, map[string]any{"members": members, "next_cursor": nil})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	r := &groupMemberResource{client: c}
	s := resSchema(t, r)
	attrs := map[string]attr.Value{
		"id": str(testGroupID + "/" + testUserID), "group_id": str(testGroupID), "user_id": str(testUserID),
	}

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"group_id": str(testGroupID), "user_id": str(testUserID),
		}),
	}, &createResp)
	fatalDiags(t, createResp.Diagnostics)
	if !added {
		t.Fatal("membership PUT not issued")
	}
	var created groupMemberModel
	fatalDiags(t, createResp.State.Get(context.Background(), &created))
	if created.ID.ValueString() != testGroupID+"/"+testUserID {
		t.Errorf("id = %q", created.ID.ValueString())
	}

	readResp := resource.ReadResponse{State: tfsdk.State{Schema: s}}
	r.Read(context.Background(), resource.ReadRequest{State: stateFrom(t, s, attrs)}, &readResp)
	fatalDiags(t, readResp.Diagnostics)
	if readResp.State.Raw.IsNull() {
		t.Fatal("state removed while the member is still listed")
	}

	// Removed out of band → dropped from state so the next plan re-adds it.
	present = false
	driftResp := resource.ReadResponse{State: stateFrom(t, s, attrs)}
	r.Read(context.Background(), resource.ReadRequest{State: stateFrom(t, s, attrs)}, &driftResp)
	fatalDiags(t, driftResp.Diagnostics)
	if !driftResp.State.Raw.IsNull() {
		t.Error("expected state removed once the member is gone")
	}

	delResp := resource.DeleteResponse{State: tfsdk.State{Schema: s}}
	r.Delete(context.Background(), resource.DeleteRequest{State: stateFrom(t, s, attrs)}, &delResp)
	fatalDiags(t, delResp.Diagnostics)
	if !removed {
		t.Error("membership DELETE not issued")
	}
}

// --- janus_group_binding ---

// The never-owner rule, at plan time, with an explanation rather than a bare
// "invalid value".
func TestGroupBindingRoleValidatorRefusesOwnerAtPlanTime(t *testing.T) {
	s := resSchema(t, NewGroupBindingResource())
	attrDef, ok := s.Attributes["role"].(resschema.StringAttribute)
	if !ok || len(attrDef.Validators) == 0 {
		t.Fatal("role must carry a plan-time validator")
	}

	for _, tc := range []struct {
		name    string
		value   types.String
		wantErr bool
		wantSub string
	}{
		{"viewer", types.StringValue("viewer"), false, ""},
		{"developer", types.StringValue("developer"), false, ""},
		{"admin", types.StringValue("admin"), false, ""},
		{"owner", types.StringValue("owner"), true, "can never be owner"},
		{"typo", types.StringValue("developper"), true, "Invalid attribute value"},
		{"empty", types.StringValue(""), true, "Invalid attribute value"},
		{"null", types.StringNull(), false, ""},
		{"unknown", types.StringUnknown(), false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var resp validator.StringResponse
			attrDef.Validators[0].ValidateString(context.Background(), validator.StringRequest{
				Path: path.Root("role"), ConfigValue: tc.value,
			}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("HasError = %v, want %v (%v)", resp.Diagnostics.HasError(), tc.wantErr, resp.Diagnostics)
			}
			if tc.wantErr && !strings.Contains(resp.Diagnostics.Errors()[0].Summary(), tc.wantSub) {
				t.Errorf("summary = %q, want it to contain %q",
					resp.Diagnostics.Errors()[0].Summary(), tc.wantSub)
			}
		})
	}
}

// Even if a role only becomes concrete at apply time, owner never reaches Janus.
func TestGroupBindingCreateRefusesOwnerWithoutCallingAPI(t *testing.T) {
	var calls atomic.Int32
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	r := &groupBindingResource{client: c}
	s := resSchema(t, r)

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: planFrom(t, s, map[string]attr.Value{
			"group_id": str(testGroupID), "role": str("owner"), "project_id": str(testProjID),
		}),
	}, &createResp)

	if !createResp.Diagnostics.HasError() {
		t.Fatal("expected an error binding a group as owner")
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("API called %d times; owner must never be sent", n)
	}
}

// The scope attributes pick the route family; the id and scope_level record it.
func TestGroupBindingCreateUsesScopeRoute(t *testing.T) {
	for _, tc := range []struct {
		name      string
		attrs     map[string]attr.Value
		wantPath  string
		wantLevel string
		wantID    string
	}{
		{
			"instance",
			map[string]attr.Value{"group_id": str(testGroupID), "role": str("admin")},
			"/v1/instance/group-members/" + testGroupID,
			"instance",
			"instance/" + testGroupID,
		},
		{
			"project",
			map[string]attr.Value{"group_id": str(testGroupID), "role": str("developer"), "project_id": str(testProjID)},
			"/v1/projects/" + testProjID + "/group-members/" + testGroupID,
			"project",
			"project/" + testProjID + "/" + testGroupID,
		},
		{
			"environment",
			map[string]attr.Value{
				"group_id": str(testGroupID), "role": str("viewer"),
				"project_id": str(testProjID), "environment_id": str(testEnvID),
			},
			"/v1/projects/" + testProjID + "/environments/" + testEnvID + "/group-members/" + testGroupID,
			"environment",
			"environment/" + testProjID + "/" + testEnvID + "/" + testGroupID,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			var gotBody map[string]string
			c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_ = json.NewDecoder(r.Body).Decode(&gotBody)
				w.WriteHeader(http.StatusNoContent)
			}))
			res := &groupBindingResource{client: c}
			s := resSchema(t, res)

			createResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
			res.Create(context.Background(), resource.CreateRequest{
				Plan: planFrom(t, s, tc.attrs),
			}, &createResp)
			fatalDiags(t, createResp.Diagnostics)

			if gotPath != tc.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tc.wantPath)
			}
			var created groupBindingModel
			fatalDiags(t, createResp.State.Get(context.Background(), &created))
			if created.ScopeLevel.ValueString() != tc.wantLevel {
				t.Errorf("scope_level = %q, want %q", created.ScopeLevel.ValueString(), tc.wantLevel)
			}
			if created.ID.ValueString() != tc.wantID {
				t.Errorf("id = %q, want %q", created.ID.ValueString(), tc.wantID)
			}
		})
	}
}

// An environment binding has no addressable route without its project.
func TestGroupBindingValidateConfigRequiresProjectForEnvironment(t *testing.T) {
	r := &groupBindingResource{}
	s := resSchema(t, r)

	resp := resource.ValidateConfigResponse{}
	r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{
		Config: configFrom(t, s, map[string]attr.Value{
			"group_id": str(testGroupID), "role": str("viewer"), "environment_id": str(testEnvID),
		}),
	}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error for environment_id without project_id")
	}

	ok := resource.ValidateConfigResponse{}
	r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{
		Config: configFrom(t, s, map[string]attr.Value{
			"group_id": str(testGroupID), "role": str("viewer"),
			"project_id": str(testProjID), "environment_id": str(testEnvID),
		}),
	}, &ok)
	fatalDiags(t, ok.Diagnostics)
}

// Read finds the binding in the scope's listing and drifts it out when it is
// gone; the role refreshes from the server.
func TestGroupBindingReadRefreshesRoleAndDrifts(t *testing.T) {
	bound := true
	c := fakeJanus(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/"+testProjID+"/group-members" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		bindings := []client.GroupBinding{}
		if bound {
			bindings = append(bindings, client.GroupBinding{
				GroupID: testGroupID, ScopeLevel: "project", Role: "admin",
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"bindings": bindings, "next_cursor": nil})
	}))
	res := &groupBindingResource{client: c}
	s := resSchema(t, res)
	attrs := map[string]attr.Value{
		"id": str("project/" + testProjID + "/" + testGroupID), "group_id": str(testGroupID),
		"role": str("developer"), "project_id": str(testProjID), "scope_level": str("project"),
	}

	readResp := resource.ReadResponse{State: tfsdk.State{Schema: s}}
	res.Read(context.Background(), resource.ReadRequest{State: stateFrom(t, s, attrs)}, &readResp)
	fatalDiags(t, readResp.Diagnostics)
	var read groupBindingModel
	fatalDiags(t, readResp.State.Get(context.Background(), &read))
	if read.Role.ValueString() != "admin" {
		t.Errorf("role after read = %q, want admin (refreshed from the server)", read.Role.ValueString())
	}

	bound = false
	driftResp := resource.ReadResponse{State: stateFrom(t, s, attrs)}
	res.Read(context.Background(), resource.ReadRequest{State: stateFrom(t, s, attrs)}, &driftResp)
	fatalDiags(t, driftResp.Diagnostics)
	if !driftResp.State.Raw.IsNull() {
		t.Error("expected state removed once the group is no longer bound at the scope")
	}
}

func TestGroupBindingImportIDs(t *testing.T) {
	r := &groupBindingResource{}
	s := resSchema(t, r)

	for _, tc := range []struct {
		id      string
		wantErr bool
		level   string
	}{
		{"instance/" + testGroupID, false, "instance"},
		{"project/" + testProjID + "/" + testGroupID, false, "project"},
		{"environment/" + testProjID + "/" + testEnvID + "/" + testGroupID, false, "environment"},
		{testGroupID, true, ""},
		{"project/" + testProjID, true, ""},
		{"config/" + testProjID + "/" + testGroupID, true, ""},
	} {
		t.Run(tc.id, func(t *testing.T) {
			resp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: objectToTFValue(t, s, nil)}}
			r.ImportState(context.Background(), resource.ImportStateRequest{ID: tc.id}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("HasError = %v, want %v (%v)", resp.Diagnostics.HasError(), tc.wantErr, resp.Diagnostics)
			}
			if tc.wantErr {
				return
			}
			var got groupBindingModel
			fatalDiags(t, resp.State.Get(context.Background(), &got))
			if got.ScopeLevel.ValueString() != tc.level {
				t.Errorf("scope_level = %q, want %q", got.ScopeLevel.ValueString(), tc.level)
			}
			if got.GroupID.ValueString() != testGroupID {
				t.Errorf("group_id = %q", got.GroupID.ValueString())
			}
		})
	}
}

func TestGroupMemberImportIDs(t *testing.T) {
	r := &groupMemberResource{}
	s := resSchema(t, r)

	bad := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: objectToTFValue(t, s, nil)}}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: testGroupID}, &bad)
	if !bad.Diagnostics.HasError() {
		t.Fatal(`expected an error for an import id without "/"`)
	}

	ok := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: objectToTFValue(t, s, nil)}}
	r.ImportState(context.Background(), resource.ImportStateRequest{
		ID: testGroupID + "/" + testUserID,
	}, &ok)
	fatalDiags(t, ok.Diagnostics)
	var got groupMemberModel
	fatalDiags(t, ok.State.Get(context.Background(), &got))
	if got.GroupID.ValueString() != testGroupID || got.UserID.ValueString() != testUserID {
		t.Errorf("imported %q / %q", got.GroupID.ValueString(), got.UserID.ValueString())
	}
}
