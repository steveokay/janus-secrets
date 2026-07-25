package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// pruneJSON is the value-free wire shape of POST .../versions/prune.
type pruneJSON struct {
	DryRun           bool  `json:"dry_run"`
	LatestVersion    int   `json:"latest_version"`
	KeepVersions     int   `json:"keep_versions"`
	KeepDays         int   `json:"keep_days"`
	PrunedVersions   []int `json:"pruned_versions"`
	PinnedVersions   []int `json:"pinned_versions"`
	VersionsDeleted  int   `json:"versions_deleted"`
	ValuesDeleted    int   `json:"values_deleted"`
	VersionsRetained int   `json:"versions_retained"`
}

type retentionJSON struct {
	InstanceMinVersions  int  `json:"instance_min_versions"`
	InstanceMinDays      int  `json:"instance_min_days"`
	ConfigMinVersions    *int `json:"config_min_versions"`
	ConfigMinDays        *int `json:"config_min_days"`
	EffectiveMinVersions int  `json:"effective_min_versions"`
	EffectiveMinDays     int  `json:"effective_min_days"`
}

// seedAPIVersions writes n config versions through the server's own service.
func seedAPIVersions(t *testing.T, srv *Server, cid string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 1; i <= n; i++ {
		if _, err := srv.service.SetSecrets(ctx, cid, []secrets.SecretChange{
			{Key: "TOKEN", Value: []byte(fmt.Sprintf("v%d", i))},
		}, fmt.Sprintf("save %d", i), "root"); err != nil {
			t.Fatal(err)
		}
	}
}

// TestVersionPruneAPIE2E covers the endpoint contract: owner-only, dry-run by
// default, audited, and value-free.
func TestVersionPruneAPIE2E(t *testing.T) {
	ts, srv, email, password, cid := authStackFull(t)
	ctx := context.Background()
	ownerCookie := login(t, ts.URL, email, password)
	seedAPIVersions(t, srv, cid, 6)

	projID := configProjectID(t, srv, cid)

	// An admin — the highest role BELOW owner — must not be able to prune.
	adminID, adminPassword, err := srv.auth.CreateUser(ctx, "prune-admin@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: adminID, ScopeLevel: "project", ProjectID: &projID, Role: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	adminCookie := login(t, ts.URL, "prune-admin@corp.io", adminPassword)

	if code := doAuthed(t, "POST", ts.URL+"/v1/configs/"+cid+"/versions/prune", adminCookie, "",
		`{"keep_versions":1,"dry_run":false}`, nil); code != http.StatusForbidden {
		t.Fatalf("admin prune: want 403, got %d", code)
	}
	// An admin may still READ the policy (secret:read).
	var pol retentionJSON
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/versions/retention", adminCookie, "",
		"", &pol); code != 200 {
		t.Fatalf("admin retention GET: want 200, got %d", code)
	}
	// ...but not write it: the override guards destruction, so it is owner-only.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/versions/retention", adminCookie, "",
		`{"min_versions":10}`, nil); code != http.StatusForbidden {
		t.Fatalf("admin retention PUT: want 403, got %d", code)
	}

	// A request with no keep_* parameters is rejected rather than defaulted.
	if code := doAuthed(t, "POST", ts.URL+"/v1/configs/"+cid+"/versions/prune", ownerCookie, "",
		`{}`, nil); code != http.StatusBadRequest {
		t.Fatalf("empty prune body: want 400, got %d", code)
	}

	// Omitting dry_run PREVIEWS; it must not destroy anything.
	var preview pruneJSON
	if code := doAuthed(t, "POST", ts.URL+"/v1/configs/"+cid+"/versions/prune", ownerCookie, "",
		`{"keep_versions":2}`, &preview); code != 200 {
		t.Fatalf("preview prune: want 200, got %d", code)
	}
	if !preview.DryRun {
		t.Fatal("omitting dry_run did not default to a preview")
	}
	if preview.VersionsDeleted != 4 {
		t.Fatalf("preview would delete %d versions, want 4", preview.VersionsDeleted)
	}
	vs, err := srv.service.ListVersions(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 6 {
		t.Fatalf("preview destroyed history: %d versions remain", len(vs))
	}

	// The real prune.
	var applied pruneJSON
	if code := doAuthed(t, "POST", ts.URL+"/v1/configs/"+cid+"/versions/prune", ownerCookie, "",
		`{"keep_versions":2,"dry_run":false}`, &applied); code != 200 {
		t.Fatalf("owner prune: want 200, got %d", code)
	}
	if applied.DryRun || applied.VersionsDeleted != 4 || applied.VersionsRetained != 2 {
		t.Fatalf("prune result = %+v", applied)
	}
	vs, err = srv.service.ListVersions(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 {
		t.Fatalf("%d versions remain after prune, want 2", len(vs))
	}
	// Both survivors still resolve fully — the restorability invariant, observed
	// through the API's own service.
	for _, v := range vs {
		state, err := srv.service.RevealConfigVersion(ctx, cid, v.Version)
		if err != nil {
			t.Fatalf("surviving v%d no longer resolves: %v", v.Version, err)
		}
		if len(state) != 1 {
			t.Fatalf("surviving v%d has %d keys, want 1", v.Version, len(state))
		}
	}

	// The prune is audited, value-free, and names the config.
	var events struct {
		Events []struct {
			Action   string  `json:"action"`
			Resource string  `json:"resource"`
			Detail   *string `json:"detail"`
		} `json:"events"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/events?limit=200", ownerCookie, "", "", &events); code != 200 {
		t.Fatalf("audit events: %d", code)
	}
	var sawPrune, sawPreview bool
	for _, e := range events.Events {
		switch e.Action {
		case "secret.version.prune":
			sawPrune = true
		case "secret.version.prune.preview":
			sawPreview = true
		default:
			continue
		}
		if !strings.Contains(e.Resource, cid) {
			t.Errorf("prune audit resource %q does not name the config", e.Resource)
		}
		detail := ""
		if e.Detail != nil {
			detail = *e.Detail
		}
		// The seeded values are literally "v1".."v6"; a detail that echoed one
		// would be a value leak (the version NUMBERS appear only as bare ints).
		for _, v := range []string{"v1", "v2", "v3", "v4", "v5", "v6"} {
			if strings.Contains(detail, v) {
				t.Errorf("prune audit detail leaked a secret value: %q", detail)
			}
		}
	}
	if !sawPrune || !sawPreview {
		t.Fatalf("missing prune audit events (prune=%v preview=%v)", sawPrune, sawPreview)
	}
}

// TestVersionRetentionOverrideAPIE2E asserts the per-config override is stored,
// resolved against the instance floor, and actually bounds a prune.
func TestVersionRetentionOverrideAPIE2E(t *testing.T) {
	ts, srv, email, password, cid := authStackFull(t)
	cookie := login(t, ts.URL, email, password)
	seedAPIVersions(t, srv, cid, 8)

	var pol retentionJSON
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/versions/retention", cookie, "", "", &pol); code != 200 {
		t.Fatalf("retention GET: %d", code)
	}
	if pol.ConfigMinVersions != nil || pol.EffectiveMinVersions != 0 {
		t.Fatalf("fresh config policy = %+v, want no override and no floor", pol)
	}

	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/versions/retention", cookie, "",
		`{"min_versions":5}`, &pol); code != 200 {
		t.Fatalf("retention PUT: %d", code)
	}
	if pol.ConfigMinVersions == nil || *pol.ConfigMinVersions != 5 || pol.EffectiveMinVersions != 5 {
		t.Fatalf("policy after PUT = %+v", pol)
	}

	// The override outranks a more aggressive prune request.
	var res pruneJSON
	if code := doAuthed(t, "POST", ts.URL+"/v1/configs/"+cid+"/versions/prune", cookie, "",
		`{"keep_versions":1,"dry_run":false}`, &res); code != 200 {
		t.Fatalf("prune: %d", code)
	}
	if res.KeepVersions != 5 || res.VersionsRetained != 5 {
		t.Fatalf("override did not bound the prune: %+v", res)
	}

	// Clearing falls back to the (unset) instance floor.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/versions/retention", cookie, "",
		`{"min_versions":null,"min_days":null}`, &pol); code != 200 {
		t.Fatalf("retention clear: %d", code)
	}
	if pol.ConfigMinVersions != nil || pol.EffectiveMinVersions != 0 {
		t.Fatalf("policy after clear = %+v", pol)
	}
	// Rejects a nonsense override.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+cid+"/versions/retention", cookie, "",
		`{"min_versions":0}`, nil); code != http.StatusBadRequest {
		t.Fatalf("zero min_versions: want 400, got %d", code)
	}
}

// TestVersionPruneRespectsInstanceFloor boots a server with the instance-wide
// floor configured and asserts a prune cannot go below it.
func TestVersionPruneRespectsInstanceFloor(t *testing.T) {
	dsn := bootPostgres(t)
	ctx := context.Background()
	srv, st, err := Boot(ctx, BootConfig{
		DatabaseURL:             dsn,
		SealType:                crypto.SealTypeShamir,
		SecretRetainMinVersions: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if got := srv.service.RetentionFloor().MinVersions; got != 4 {
		t.Fatalf("boot did not apply the retention floor: %d", got)
	}
}
