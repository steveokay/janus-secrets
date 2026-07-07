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
