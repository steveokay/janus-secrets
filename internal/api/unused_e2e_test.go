package api

import (
	"context"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
)

// TestUnusedAPIE2E exercises advisory unused-secret detection surfaced in the
// masked-secrets response. It rides the existing secret:read masked list (no new
// permission). A real per-key reveal (GET one key) records a secret.reveal audit
// event, which marks that key "read" and clears its unused flag; an unrevealed
// key stays "never read". Advisory only — nothing is blocked.
func TestUnusedAPIE2E(t *testing.T) {
	ts, srv, email, password, cid := authStackFull(t)
	ctx := context.Background()

	if _, err := srv.service.SetSecrets(ctx, cid, []secrets.SecretChange{
		{Key: "DATABASE_URL", Value: []byte("pg://x")},
		{Key: "API_KEY", Value: []byte("k")},
	}, "seed", "root"); err != nil {
		t.Fatal(err)
	}

	adminCookie := login(t, ts.URL, email, password)

	type maskedEntry struct {
		Stale      bool    `json:"stale"`
		Unused     bool    `json:"unused"`
		LastReadAt *string `json:"last_read_at"`
	}
	fetch := func() map[string]maskedEntry {
		var masked struct {
			Secrets map[string]maskedEntry `json:"secrets"`
		}
		if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/secrets", adminCookie, "", "", &masked); code != 200 {
			t.Fatalf("masked GET: want 200, got %d", code)
		}
		return masked.Secrets
	}

	// Before any reveal: both keys are never-read → unused, last_read_at null.
	before := fetch()
	for _, k := range []string{"DATABASE_URL", "API_KEY"} {
		if !before[k].Unused || before[k].LastReadAt != nil {
			t.Fatalf("%s pre-reveal = %+v, want unused=true last_read_at=null", k, before[k])
		}
	}

	// Reveal DATABASE_URL once via the raw per-key path (?raw=true) — the exact
	// endpoint the UI's reveal / "Reveal all" calls, which records a secret.reveal
	// on the PER-KEY resource configs/{cid}/secrets/{key}. (The default resolved
	// reveal path records the aggregate resource and is NOT per-key attributable.)
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/secrets/DATABASE_URL?raw=true", adminCookie, "", "", nil); code != 200 {
		t.Fatalf("reveal DATABASE_URL: want 200, got %d", code)
	}

	after := fetch()
	db := after["DATABASE_URL"]
	if db.Unused {
		t.Errorf("DATABASE_URL after reveal Unused = true, want false (just read)")
	}
	if db.LastReadAt == nil {
		t.Errorf("DATABASE_URL after reveal LastReadAt = null, want a timestamp")
	}
	// API_KEY was never revealed → still unused.
	if !after["API_KEY"].Unused || after["API_KEY"].LastReadAt != nil {
		t.Errorf("API_KEY = %+v, want still never-read", after["API_KEY"])
	}
}
