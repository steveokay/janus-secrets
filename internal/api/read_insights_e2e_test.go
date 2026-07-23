package api

import (
	"context"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
)

// TestReadInsightsAPIE2E exercises GET /v1/configs/{cid}/read-insights: it rides
// the existing secret:read gate (no new permission), is value-free (counts +
// timestamps only), and reflects per-key reveals recorded in the audit ledger.
func TestReadInsightsAPIE2E(t *testing.T) {
	ts, srv, email, password, cid := authStackFull(t)
	ctx := context.Background()

	// OBVIOUSLY-FAKE low-entropy fixture values so a secret scanner does not flag
	// them; these are exactly the strings we assert never appear in the response.
	const dbValue = "value-database-one"
	const apiValue = "value-api-two"
	if _, err := srv.service.SetSecrets(ctx, cid, []secrets.SecretChange{
		{Key: "DATABASE_URL", Value: []byte(dbValue)},
		{Key: "API_KEY", Value: []byte(apiValue)},
	}, "seed", "root"); err != nil {
		t.Fatal(err)
	}

	adminCookie := login(t, ts.URL, email, password)

	type keyInsight struct {
		LastReadAt *string `json:"last_read_at"`
		Daily      []int   `json:"daily"`
	}
	type resp struct {
		WindowDays int                   `json:"window_days"`
		Keys       map[string]keyInsight `json:"keys"`
	}
	fetch := func() resp {
		var out resp
		if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/read-insights", adminCookie, "", "", &out); code != 200 {
			t.Fatalf("read-insights GET: want 200, got %d", code)
		}
		return out
	}

	// Before any reveal: no keys have per-key reveals → empty map, 30-day window.
	before := fetch()
	if before.WindowDays != 30 {
		t.Fatalf("window_days = %d, want 30", before.WindowDays)
	}
	if len(before.Keys) != 0 {
		t.Fatalf("pre-reveal keys = %v, want empty", before.Keys)
	}

	// Reveal DATABASE_URL twice via the per-key raw path (records secret.reveal on
	// the per-key resource) — the exact endpoint the UI reveal / "Reveal all" call.
	for i := 0; i < 2; i++ {
		if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/secrets/DATABASE_URL?raw=true", adminCookie, "", "", nil); code != 200 {
			t.Fatalf("reveal DATABASE_URL: want 200, got %d", code)
		}
	}

	after := fetch()
	db, ok := after.Keys["DATABASE_URL"]
	if !ok {
		t.Fatal("DATABASE_URL absent after reveal")
	}
	if db.LastReadAt == nil {
		t.Error("DATABASE_URL last_read_at = null, want a timestamp")
	}
	if len(db.Daily) != 30 {
		t.Fatalf("daily len = %d, want 30", len(db.Daily))
	}
	// Both reveals land today = the last bucket.
	if db.Daily[29] != 2 {
		t.Errorf("today bucket = %d, want 2", db.Daily[29])
	}
	var sum int
	for _, v := range db.Daily {
		sum += v
	}
	if sum != 2 {
		t.Errorf("DATABASE_URL total reveals = %d, want 2", sum)
	}
	// API_KEY was never revealed per-key → still absent.
	if _, present := after.Keys["API_KEY"]; present {
		t.Error("API_KEY present, want absent (never revealed per-key)")
	}
}

// TestReadInsightsValueFree asserts the read-insights response body carries no
// secret value — only key names, counts, and timestamps.
func TestReadInsightsValueFree(t *testing.T) {
	ts, srv, email, password, cid := authStackFull(t)
	ctx := context.Background()

	const dbValue = "value-database-one"
	const apiValue = "value-api-two"
	if _, err := srv.service.SetSecrets(ctx, cid, []secrets.SecretChange{
		{Key: "DATABASE_URL", Value: []byte(dbValue)},
		{Key: "API_KEY", Value: []byte(apiValue)},
	}, "seed", "root"); err != nil {
		t.Fatal(err)
	}
	adminCookie := login(t, ts.URL, email, password)

	// Reveal both so the aggregate carries data for both keys.
	for _, k := range []string{"DATABASE_URL", "API_KEY"} {
		if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/secrets/"+k+"?raw=true", adminCookie, "", "", nil); code != 200 {
			t.Fatalf("reveal %s: want 200, got %d", k, code)
		}
	}

	body, code := doAuthedBody(t, "GET", ts.URL+"/v1/configs/"+cid+"/read-insights", adminCookie, "")
	if code != 200 {
		t.Fatalf("read-insights GET: want 200, got %d", code)
	}
	// Key NAMES may appear (they are metadata); secret VALUES must not.
	for _, v := range []string{dbValue, apiValue} {
		if strings.Contains(body, v) {
			t.Fatalf("read-insights body leaked a secret value %q: %s", v, body)
		}
	}
	// Sanity: the response really did carry the key metadata we expect.
	if !strings.Contains(body, "DATABASE_URL") || !strings.Contains(body, "daily") {
		t.Fatalf("read-insights body missing expected metadata: %s", body)
	}
}
