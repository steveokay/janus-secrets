package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

// TestTransitNoMaterialInLogsOrAudit drives a full transit flow with a known
// sentinel plaintext and proves (a) neither the plaintext nor a returned data
// key ever lands in the request logs, and (b) the audit policy holds:
// management ops (create/rotate) are recorded while data-plane ops
// (encrypt/decrypt/datakey) are not, and no audit row carries key material.
func TestTransitNoMaterialInLogsOrAudit(t *testing.T) {
	var logBuf syncBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(prev)

	ts, srv, email, password, _ := authStackFull(t)
	cookie := login(t, ts.URL, email, password)

	const sentinel = "TRANSIT-SENTINEL-CANARY-7b2e"
	ptB64 := base64.StdEncoding.EncodeToString([]byte(sentinel))

	// Management: create + rotate.
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/keys", cookie, "",
		`{"name":"app","type":"aes256-gcm"}`, nil); code != 201 {
		t.Fatalf("create key: %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/keys/app/rotate", cookie, "", "", nil); code != 200 {
		t.Fatalf("rotate key: %d", code)
	}

	// Data-plane: encrypt the sentinel, then decrypt and verify the roundtrip.
	var enc struct {
		Ciphertext string `json:"ciphertext"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/encrypt/app", cookie, "",
		fmt.Sprintf(`{"plaintext":%q}`, ptB64), &enc); code != 200 || enc.Ciphertext == "" {
		t.Fatalf("encrypt: %d %q", code, enc.Ciphertext)
	}
	var dec struct {
		Plaintext string `json:"plaintext"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/decrypt/app", cookie, "",
		fmt.Sprintf(`{"ciphertext":%q}`, enc.Ciphertext), &dec); code != 200 {
		t.Fatalf("decrypt: %d", code)
	}
	if got, _ := base64.StdEncoding.DecodeString(dec.Plaintext); string(got) != sentinel {
		t.Fatalf("decrypt roundtrip: %q, want sentinel", got)
	}

	// Data-plane: generate a plaintext data key.
	var dk struct {
		Plaintext string `json:"plaintext"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/transit/datakey/plaintext/app", cookie, "", "", &dk); code != 200 || dk.Plaintext == "" {
		t.Fatalf("datakey: %d %q", code, dk.Plaintext)
	}
	dekB64 := dk.Plaintext

	// (a) Leak assertions against the captured logs.
	logs := logBuf.String()
	if strings.Contains(logs, sentinel) {
		t.Fatal("sentinel plaintext leaked into log output")
	}
	if strings.Contains(logs, dekB64) {
		t.Fatal("datakey plaintext leaked into log output")
	}
	// Sanity: the request logger did capture the encrypt request.
	if !strings.Contains(logs, "/v1/transit/encrypt/app") {
		t.Fatalf("expected transit request logs, got: %q", logs)
	}

	// (b) Audit-policy assertions: collect actions + dump columns for material scan.
	repo := store.NewAuditRepo(srv.st)
	var actions []string
	var dump strings.Builder
	rows := 0
	if err := repo.Iterate(context.Background(), func(a store.AuditRow) error {
		rows++
		actions = append(actions, a.Action)
		fmt.Fprintf(&dump, "%d|%s|%s|%s|%s|%s|%s|%s|%x|%x\n",
			a.Seq, a.ActorKind, derefStr(a.ActorID), a.ActorName, a.Action,
			a.Resource, derefStr(a.Detail), a.Result, a.PrevHash, a.Hash)
		return nil
	}); err != nil {
		t.Fatalf("iterate audit rows: %v", err)
	}
	if rows == 0 {
		t.Fatal("no audit rows written; flow did not exercise the recorder")
	}

	has := func(action string) bool {
		for _, a := range actions {
			if a == action {
				return true
			}
		}
		return false
	}
	if !has("transit.key.create") {
		t.Fatalf("expected transit.key.create in audit actions, got: %v", actions)
	}
	if !has("transit.key.rotate") {
		t.Fatalf("expected transit.key.rotate in audit actions, got: %v", actions)
	}
	// Data-plane ops must never be audited.
	for _, a := range actions {
		if strings.Contains(a, "encrypt") || strings.Contains(a, "decrypt") || strings.Contains(a, "datakey") {
			t.Fatalf("data-plane op audited: %q (all: %v)", a, actions)
		}
	}
	// Belt-and-braces: no audit row carries the sentinel or the data key.
	if strings.Contains(dump.String(), sentinel) {
		t.Fatal("sentinel plaintext leaked into an audit_events row")
	}
	if strings.Contains(dump.String(), dekB64) {
		t.Fatal("datakey plaintext leaked into an audit_events row")
	}
}
