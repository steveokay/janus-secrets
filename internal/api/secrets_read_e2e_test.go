package api

import (
	"context"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
)

func TestSecretsReadE2E(t *testing.T) {
	ts, srv, email, password, cid := authStackFull(t)
	cookie := login(t, ts.URL, email, password)

	// Seed two secrets via the wired service (one save = one config version).
	if _, err := srv.service.SetSecrets(context.Background(), cid, []secrets.SecretChange{
		{Key: "DB_URL", Value: []byte("postgres://secret-conn")},
		{Key: "API_KEY", Value: []byte("sk-live-xyz")},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}

	// Masked list: values absent.
	var masked struct {
		Version int                       `json:"version"`
		Secrets map[string]map[string]any `json:"secrets"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/secrets", cookie, "", "", &masked); code != 200 {
		t.Fatalf("masked list: %d", code)
	}
	if len(masked.Secrets) != 2 {
		t.Fatalf("want 2 keys, got %d", len(masked.Secrets))
	}
	if _, hasValue := masked.Secrets["DB_URL"]["value"]; hasValue {
		t.Fatal("masked list leaked a value")
	}

	// Reveal one.
	var one struct {
		Key, Value   string
		ValueVersion int `json:"value_version"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/secrets/DB_URL", cookie, "", "", &one); code != 200 {
		t.Fatalf("reveal one: %d", code)
	}
	if one.Value != "postgres://secret-conn" {
		t.Fatalf("reveal one value: %q", one.Value)
	}

	// Reveal all.
	var all struct {
		Version int
		Secrets map[string]string
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/secrets?reveal=true", cookie, "", "", &all); code != 200 {
		t.Fatalf("reveal all: %d", code)
	}
	if all.Secrets["API_KEY"] != "sk-live-xyz" {
		t.Fatalf("reveal all: %+v", all.Secrets)
	}

	// Audit: masked list emitted nothing; reveals emitted secret.reveal.
	_, exp := rawGet(t, ts.URL+"/v1/audit/export?format=jsonl&action=secret.reveal", cookie)
	if strings.Count(exp, "secret.reveal") < 2 {
		t.Fatalf("expected >=2 secret.reveal events, export:\n%s", exp)
	}
	// The masked-list read must not appear as a reveal for DB_URL beyond the
	// explicit reveals (2: one-key + all). A third would mean the list audited.
	if strings.Count(exp, "secret.reveal") > 2 {
		t.Fatalf("masked list must not audit; too many reveals:\n%s", exp)
	}
}
