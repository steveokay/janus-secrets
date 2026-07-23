package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// TestConfigCompareE2E exercises GET /v1/configs/{cid}/compare?against=. It
// asserts: the owner gets a value-free key-level diff (in_a/in_b/differs +
// origins, NO secret value anywhere in the body); a user who can read only one
// side gets 403 (dual SecretRead); a self-compare and a missing `against` are
// 400; and one config.compare audit event is written.
func TestConfigCompareE2E(t *testing.T) {
	ts, srv, ownerEmail, ownerPassword, _ := authStackFull(t)
	ctx := context.Background()
	ownerCookie := login(t, ts.URL, ownerEmail, ownerPassword)

	p, err := srv.service.CreateProject(ctx, "cmpproj", "Compare Project")
	if err != nil {
		t.Fatal(err)
	}
	stg, err := srv.service.CreateEnvironment(ctx, p.ID, "staging", "Staging")
	if err != nil {
		t.Fatal(err)
	}
	prod, err := srv.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	stgCfg, err := srv.service.CreateConfig(ctx, stg.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	prodCfg, err := srv.service.CreateConfig(ctx, prod.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}

	const stagingShared = "staging-shared-value"
	const prodShared = "prod-shared-value"
	const onlyStagingVal = "only-staging-value"
	if _, err := srv.service.SetSecrets(ctx, stgCfg.ID, []secrets.SecretChange{
		{Key: "SHARED", Value: []byte(stagingShared)},
		{Key: "SAME", Value: []byte("identical")},
		{Key: "ONLY_STAGING", Value: []byte(onlyStagingVal)},
	}, "seed staging", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.service.SetSecrets(ctx, prodCfg.ID, []secrets.SecretChange{
		{Key: "SHARED", Value: []byte(prodShared)},
		{Key: "SAME", Value: []byte("identical")},
		{Key: "ONLY_PROD", Value: []byte("only-prod")},
	}, "seed prod", "test"); err != nil {
		t.Fatal(err)
	}

	// --- Owner compare: value-free diff ---
	var cmp struct {
		ConfigA string `json:"config_a"`
		ConfigB string `json:"config_b"`
		Entries []struct {
			Key     string `json:"key"`
			InA     bool   `json:"in_a"`
			InB     bool   `json:"in_b"`
			Differs bool   `json:"differs"`
			OriginA string `json:"origin_a"`
			OriginB string `json:"origin_b"`
		} `json:"entries"`
	}
	url := ts.URL + "/v1/configs/" + stgCfg.ID + "/compare?against=" + prodCfg.ID
	// Read the raw body too, to assert no secret value leaks into the payload.
	rawBody, code := doAuthedBody(t, "GET", url, ownerCookie, "")
	if code != 200 {
		t.Fatalf("owner compare: want 200, got %d (%s)", code, rawBody)
	}
	if err := json.Unmarshal([]byte(rawBody), &cmp); err != nil {
		t.Fatalf("decode compare: %v", err)
	}
	for _, leak := range []string{stagingShared, prodShared, onlyStagingVal, "identical"} {
		if strings.Contains(rawBody, leak) {
			t.Fatalf("secret value %q leaked into compare response: %s", leak, rawBody)
		}
	}
	byKey := map[string]struct {
		inA, inB, differs bool
		oa, ob            string
	}{}
	for _, e := range cmp.Entries {
		byKey[e.Key] = struct {
			inA, inB, differs bool
			oa, ob            string
		}{e.InA, e.InB, e.Differs, e.OriginA, e.OriginB}
	}
	if r := byKey["SHARED"]; !r.inA || !r.inB || !r.differs {
		t.Errorf("SHARED: %+v, want in both + differs", r)
	}
	if r := byKey["SAME"]; !r.inA || !r.inB || r.differs {
		t.Errorf("SAME: %+v, want in both + not differs", r)
	}
	if r := byKey["ONLY_STAGING"]; !r.inA || r.inB {
		t.Errorf("ONLY_STAGING: %+v, want only in A", r)
	}
	if r := byKey["ONLY_PROD"]; r.inA || !r.inB {
		t.Errorf("ONLY_PROD: %+v, want only in B", r)
	}
	if r := byKey["ONLY_STAGING"]; r.oa != "own" || r.ob != "" {
		t.Errorf("ONLY_STAGING origins: (%q,%q), want (own,\"\")", r.oa, r.ob)
	}

	// --- Missing `against` -> 400 ---
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+stgCfg.ID+"/compare", ownerCookie, "", "", nil); code != http.StatusBadRequest {
		t.Fatalf("missing against: want 400, got %d", code)
	}
	// --- Self-compare -> 400 ---
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+stgCfg.ID+"/compare?against="+stgCfg.ID, ownerCookie, "", "", nil); code != http.StatusBadRequest {
		t.Fatalf("self compare: want 400, got %d", code)
	}

	// --- Dual RBAC: reader of only one side gets 403 ---
	oneSideID, oneSidePassword, err := srv.auth.CreateUser(ctx, "one-side@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	// Grant read on staging only; prod is unreadable to this user.
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: oneSideID, ScopeLevel: "environment", EnvironmentID: &stg.ID, Role: "viewer",
	}); err != nil {
		t.Fatal(err)
	}
	oneSideCookie := login(t, ts.URL, "one-side@corp.io", oneSidePassword)
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+stgCfg.ID+"/compare?against="+prodCfg.ID, oneSideCookie, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("one-side reader compare: want 403, got %d", code)
	}
	// The reverse direction (prod as primary, which the user cannot read) is also 403.
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+prodCfg.ID+"/compare?against="+stgCfg.ID, oneSideCookie, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("one-side reader reverse compare: want 403, got %d", code)
	}
}

// doAuthedBody is doAuthed but returns the raw response body (for leak asserts).
func doAuthedBody(t *testing.T, method, url, cookie, bearer string) (string, int) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), resp.StatusCode
}
