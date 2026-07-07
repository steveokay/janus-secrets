package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
)

// metricsReadsResp mirrors reads24hResponse's wire shape for decode assertions.
type metricsReadsResp struct {
	Reads24h   int64 `json:"reads_24h"`
	TopConfigs []struct {
		ConfigID    string `json:"config_id"`
		ConfigName  string `json:"config_name"`
		ProjectName string `json:"project_name"`
		Reads       int64  `json:"reads"`
	} `json:"top_configs"`
	TopTokens []struct {
		TokenID   string `json:"token_id"`
		TokenName string `json:"token_name"`
		Reads     int64  `json:"reads"`
	} `json:"top_tokens"`
}

// TestMetricsReadsInstance drives GET /v1/metrics/reads-24h against the real
// stack: a real secret.reveal is generated through the HTTP handler (which emits
// the audit event MetricsRepo aggregates), then an audit-capable admin sees a
// non-zero count while a config-scoped read service token (no instance
// AuditRead) is denied.
func TestMetricsReadsInstance(t *testing.T) {
	ts, srv, email, password, cid := authStackFull(t)
	adminCookie := login(t, ts.URL, email, password)

	// Seed a secret through the wired service, then reveal it through the stack
	// so a real successful secret.reveal audit event is recorded.
	if _, err := srv.service.SetSecrets(context.Background(), cid, []secrets.SecretChange{
		{Key: "DB_URL", Value: []byte("postgres://secret-conn")},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/secrets/DB_URL", adminCookie, "", "", nil); code != 200 {
		t.Fatalf("reveal: %d", code)
	}

	// Audit-capable admin → 200 with a non-zero count.
	var m metricsReadsResp
	if code := doAuthed(t, "GET", ts.URL+"/v1/metrics/reads-24h", adminCookie, "", "", &m); code != 200 {
		t.Fatalf("metrics as admin: %d", code)
	}
	if m.Reads24h < 1 {
		t.Fatalf("expected reads_24h >= 1, got %d (%+v)", m.Reads24h, m)
	}

	// A config-scoped read service token lacks instance AuditRead → 403.
	var minted struct {
		Token string `json:"token"`
		ID    string `json:"id"`
	}
	mintBody := fmt.Sprintf(`{"name":"ro","scope":{"kind":"config","id":%q},"access":"read"}`, cid)
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", adminCookie, "", mintBody, &minted); code != 200 || minted.Token == "" {
		t.Fatalf("mint ro token: %d %+v", code, minted)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/metrics/reads-24h", "", minted.Token, "", nil); code != 403 {
		t.Fatalf("expected 403 for non-audit-reader, got %d", code)
	}

	// Unauthenticated → 401 (RequireAuth gate).
	if code := doAuthed(t, "GET", ts.URL+"/v1/metrics/reads-24h", "", "", "", nil); code != 401 {
		t.Fatalf("expected 401 unauthenticated, got %d", code)
	}
}

// TestMetricsReadsProjectIsolation proves the project-scoped endpoint counts
// only reveals against configs belonging to that project: N reveals against
// project A and M != N reveals against project B are seeded, and
// GET /v1/projects/{pidA}/metrics/reads-24h must report exactly N — project B's
// reveals are excluded — with every top_configs entry belonging to project A.
func TestMetricsReadsProjectIsolation(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	adminCookie := login(t, ts.URL, email, password)
	ctx := context.Background()

	// Build two independent projects, each with its own env + root config, via
	// the server's wired secrets service (same pattern as authStackFull).
	makeProject := func(slug, name string) (projectID, configID string) {
		t.Helper()
		p, err := srv.service.CreateProject(ctx, slug, name)
		if err != nil {
			t.Fatalf("create project %s: %v", slug, err)
		}
		e, err := srv.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
		if err != nil {
			t.Fatalf("create env %s: %v", slug, err)
		}
		c, err := srv.service.CreateConfig(ctx, e.ID, "root", nil)
		if err != nil {
			t.Fatalf("create config %s: %v", slug, err)
		}
		if _, err := srv.service.SetSecrets(ctx, c.ID, []secrets.SecretChange{
			{Key: "DB_URL", Value: []byte("postgres://secret-conn")},
		}, "seed", "test"); err != nil {
			t.Fatalf("seed secret %s: %v", slug, err)
		}
		return p.ID, c.ID
	}

	pidA, cidA := makeProject("iso-a", "IsolationA")
	_, cidB := makeProject("iso-b", "IsolationB")

	// Emit N reveals on A and M reveals on B (N != M so isolation is
	// unambiguous). Each successful GET is one secret.reveal audit event.
	const (
		n = 2 // project A
		m = 1 // project B
	)
	reveal := func(cid string) {
		t.Helper()
		if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/secrets/DB_URL", adminCookie, "", "", nil); code != 200 {
			t.Fatalf("reveal %s: %d", cid, code)
		}
	}
	for i := 0; i < n; i++ {
		reveal(cidA)
	}
	for i := 0; i < m; i++ {
		reveal(cidB)
	}

	// Project A's scoped metrics must count only its own N reveals.
	var mA metricsReadsResp
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+pidA+"/metrics/reads-24h", adminCookie, "", "", &mA); code != 200 {
		t.Fatalf("project A metrics: %d", code)
	}
	if mA.Reads24h != n {
		t.Fatalf("project A reads_24h = %d, want %d (project B's %d reveals must be excluded); %+v", mA.Reads24h, n, m, mA)
	}

	// Every top-config attributed to A must belong to project A (name + id),
	// never project B's config.
	if len(mA.TopConfigs) == 0 {
		t.Fatalf("expected at least one top_configs entry for project A, got none: %+v", mA)
	}
	for _, c := range mA.TopConfigs {
		if c.ConfigID == cidB {
			t.Fatalf("project B config %s leaked into project A top_configs: %+v", cidB, mA.TopConfigs)
		}
		if c.ProjectName != "IsolationA" {
			t.Fatalf("top_configs entry not owned by project A: %+v", c)
		}
	}
}
