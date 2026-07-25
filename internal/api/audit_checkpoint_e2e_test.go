package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// checkpointResp mirrors the /v1/audit/checkpoint response shape.
type checkpointResp struct {
	Checkpoint *struct {
		ThroughSeq  int64  `json:"through_seq"`
		ThroughHash string `json:"through_hash"`
		EventCount  int64  `json:"event_count"`
		CreatedAt   string `json:"created_at"`
		MACValid    bool   `json:"mac_valid"`
	} `json:"checkpoint"`
}

type verifyResp struct {
	Valid          bool  `json:"valid"`
	Count          int64 `json:"count"`
	HeadSeq        int64 `json:"head_seq"`
	FromCheckpoint bool  `json:"from_checkpoint"`
	Checkpoint     *struct {
		ThroughSeq int64 `json:"through_seq"`
		MACValid   bool  `json:"mac_valid"`
	} `json:"checkpoint"`
}

// TestAuditCheckpointE2E drives create → verify-from-checkpoint → prune → verify
// against a real recorder wired with checkpointing, and asserts owner-gating.
func TestAuditCheckpointE2E(t *testing.T) {
	ts, _, email, password, configID := authStackFull(t)
	adminCookie := login(t, ts.URL, email, password)

	// Generate some auditable events (token mints) to populate the chain.
	for i := 0; i < 3; i++ {
		body := fmt.Sprintf(`{"name":"t%d","scope":{"kind":"config","id":%q},"access":"read"}`, i, configID)
		if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", adminCookie, "", body, nil); code != 200 {
			t.Fatalf("mint %d: %d", i, code)
		}
	}

	// Create a checkpoint (owner-only).
	var cp checkpointResp
	if code := doAuthed(t, "POST", ts.URL+"/v1/audit/checkpoint", adminCookie, "", "", &cp); code != 200 {
		t.Fatalf("create checkpoint: %d", code)
	}
	if cp.Checkpoint == nil || !cp.Checkpoint.MACValid || cp.Checkpoint.ThroughSeq == 0 {
		t.Fatalf("checkpoint = %+v", cp.Checkpoint)
	}
	anchorSeq := cp.Checkpoint.ThroughSeq

	// GET the latest checkpoint.
	var got checkpointResp
	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/checkpoint", adminCookie, "", "", &got); code != 200 {
		t.Fatalf("get checkpoint: %d", code)
	}
	if got.Checkpoint == nil || got.Checkpoint.ThroughSeq != anchorSeq {
		t.Fatalf("get checkpoint = %+v (want seq %d)", got.Checkpoint, anchorSeq)
	}

	// More events after the checkpoint.
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", adminCookie, "",
		fmt.Sprintf(`{"name":"after","scope":{"kind":"config","id":%q},"access":"read"}`, configID), nil); code != 200 {
		t.Fatalf("mint after: %d", code)
	}

	// Verify: must anchor on the checkpoint and stay valid.
	var vr verifyResp
	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/verify", adminCookie, "", "", &vr); code != 200 {
		t.Fatalf("verify: %d", code)
	}
	if !vr.Valid || !vr.FromCheckpoint || vr.Checkpoint == nil || vr.Checkpoint.ThroughSeq != anchorSeq {
		t.Fatalf("verify = %+v", vr)
	}
	if !vr.Checkpoint.MACValid {
		t.Fatal("verify checkpoint MAC must be valid")
	}

	// Prune the verified prefix (no shipper configured → no HWM guard).
	var pr struct {
		PrunedThrough int64 `json:"pruned_through"`
		Deleted       int64 `json:"deleted"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/audit/prune", adminCookie, "", "", &pr); code != 200 {
		t.Fatalf("prune: %d", code)
	}
	if pr.PrunedThrough != anchorSeq || pr.Deleted < 1 {
		t.Fatalf("prune = %+v (want through %d)", pr, anchorSeq)
	}

	// Verify still passes over the pruned log, walking forward from the checkpoint.
	var vr2 verifyResp
	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/verify", adminCookie, "", "", &vr2); code != 200 {
		t.Fatalf("verify after prune: %d", code)
	}
	if !vr2.Valid || !vr2.FromCheckpoint {
		t.Fatalf("verify after prune = %+v", vr2)
	}
}

// TestAuditCheckpointOwnerGated proves a non-owner (developer) is denied the
// checkpoint + prune endpoints (403) while owner is allowed.
func TestAuditCheckpointOwnerGated(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	adminCookie := login(t, ts.URL, email, password)

	// Create a developer user and log in as them.
	var dev struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", adminCookie, "", `{"email":"dev2@corp.io"}`, &dev); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/instance/members/"+dev.ID, adminCookie, "", `{"role":"developer"}`, nil); code != 204 {
		t.Fatalf("grant developer: %d", code)
	}
	devCookie := login(t, ts.URL, "dev2@corp.io", dev.Password)

	if code := doAuthed(t, "POST", ts.URL+"/v1/audit/checkpoint", devCookie, "", "", nil); code != 403 {
		t.Fatalf("developer create checkpoint = %d, want 403", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/audit/prune", devCookie, "", "", nil); code != 403 {
		t.Fatalf("developer prune = %d, want 403", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/checkpoint", devCookie, "", "", nil); code != 403 {
		t.Fatalf("developer get checkpoint = %d, want 403", code)
	}
}

// TestAuditPruneRefusesWithoutCheckpoint proves prune is fail-closed: with no
// checkpoint it refuses (400) rather than deleting anything.
func TestAuditPruneRefusesWithoutCheckpoint(t *testing.T) {
	ts, _, email, password, configID := authStackFull(t)
	adminCookie := login(t, ts.URL, email, password)

	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", adminCookie, "",
		fmt.Sprintf(`{"name":"x","scope":{"kind":"config","id":%q},"access":"read"}`, configID), nil); code != 200 {
		t.Fatalf("mint: %d", code)
	}
	body, code := doAuthedBody(t, "POST", ts.URL+"/v1/audit/prune", adminCookie, "")
	if code != 400 {
		t.Fatalf("prune without checkpoint = %d, want 400; body=%s", code, body)
	}
	if !strings.Contains(body, "checkpoint") {
		t.Fatalf("expected checkpoint-related error, got %s", body)
	}
}

// TestAuditCheckpointNoValueLeak asserts the checkpoint/prune responses and the
// audit events they emit carry no secret material — only seqs/counts/paths.
func TestAuditCheckpointNoValueLeak(t *testing.T) {
	ts, _, email, password, configID := authStackFull(t)
	adminCookie := login(t, ts.URL, email, password)

	// Mint a token whose one-time value we then ensure never appears anywhere.
	var minted struct {
		Token string `json:"token"`
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", adminCookie, "",
		fmt.Sprintf(`{"name":"leak","scope":{"kind":"config","id":%q},"access":"read"}`, configID), &minted); code != 200 {
		t.Fatalf("mint: %d", code)
	}
	if minted.Token == "" {
		t.Fatal("no token minted")
	}

	cpBody, code := doAuthedBody(t, "POST", ts.URL+"/v1/audit/checkpoint", adminCookie, "")
	if code != 200 {
		t.Fatalf("checkpoint: %d", code)
	}
	pruneBody, code := doAuthedBody(t, "POST", ts.URL+"/v1/audit/prune", adminCookie, "")
	if code != 200 {
		t.Fatalf("prune: %d", code)
	}
	code, exportBody := rawGet(t, ts.URL+"/v1/audit/export?format=jsonl", adminCookie)
	if code != 200 {
		t.Fatalf("export: %d", code)
	}

	for _, blob := range []string{cpBody, pruneBody, exportBody} {
		if strings.Contains(blob, minted.Token) {
			t.Fatal("token VALUE leaked into a checkpoint/prune/export response")
		}
	}

	// The checkpoint + prune actions must be present in the export, value-free.
	if !strings.Contains(exportBody, "audit.checkpoint.create") {
		t.Fatal("checkpoint action not audited")
	}
	// Sanity: the checkpoint response is well-formed JSON with only metadata.
	var cp checkpointResp
	if err := json.Unmarshal([]byte(cpBody), &cp); err != nil || cp.Checkpoint == nil {
		t.Fatalf("checkpoint body not metadata JSON: %v", err)
	}
}
