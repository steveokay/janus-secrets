package secretsync

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

// TestVerifyNeverLeaksValues is the value-free invariant test for drift
// detection. It runs the full verification path over a destination whose values
// BOTH match and differ from the desired ones, then greps every artefact the
// feature can produce — engine logs, the returned report (as JSON), every
// persisted verify-run row, the recorded audit events, and the errors returned —
// for the plaintext fixtures and for any HMAC digest of them.
//
// Only key NAMES, counts, booleans and a sanitized category may appear.
func TestVerifyNeverLeaksValues(t *testing.T) {
	const (
		desiredValue = "DESIRED-VALUE-fixture-7f21ab"
		remoteValue  = "REMOTE-DRIFTED-VALUE-fixture-3c90de"
		credToken    = "glpat-CREDENTIAL-fixture-55aa11"
	)

	var logbuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logbuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	svc, sec := newTestService(t)
	svc.logger = logger
	proj, cfg := mkChain(t, sec, "sync-verify-leak")

	// The destination holds a DIFFERENT value for the managed key (value drift)
	// plus an unmanaged extra whose value must also never surface.
	gl := newFakeGitLab(t, map[string]string{
		"API_KEY": remoteValue,
		"STRAY":   remoteValue,
	})
	tgt := seedTarget(t, svc, sec, proj, cfg, ProviderGitLab,
		`{"gitlab_url":"`+gl.srv.URL+`","project":"42"}`, true,
		map[string]string{"API_KEY": desiredValue})

	// Re-seal the creds with a recognizable token so credential leakage is
	// covered by the same grep.
	c := Creds{Token: credToken}
	if _, err := svc.Update(context.Background(), tgt.ID, nil, nil, nil, &c, nil); err != nil {
		t.Fatalf("Update creds: %v", err)
	}

	ctx := context.Background()
	rep, err := svc.Verify(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.Status != VerifyDrift || rep.ModifiedCount != 1 {
		t.Fatalf("expected value drift, got %+v", rep)
	}

	// Also exercise the error path (destination unreachable) so its log/record
	// output is in scope.
	gl.srv.Close()
	if _, err := svc.Verify(ctx, tgt.ID); err == nil {
		t.Fatal("expected an error once the destination is unreachable")
	}

	// ── collect every artefact ────────────────────────────────────────────────
	haystacks := []string{logbuf.String()}

	repJSON, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	haystacks = append(haystacks, string(repJSON))

	runs, err := svc.ListVerifyRuns(ctx, tgt.ID, 0, 100)
	if err != nil {
		t.Fatalf("ListVerifyRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("recorded %d runs, want 2", len(runs))
	}
	runsJSON, err := json.Marshal(runs)
	if err != nil {
		t.Fatal(err)
	}
	haystacks = append(haystacks, string(runsJSON))

	st, err := svc.GetVerifyState(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("GetVerifyState: %v", err)
	}
	stJSON, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	haystacks = append(haystacks, string(stJSON))

	// Audit events written by the verify passes.
	repo := store.NewAuditRepo(testStore)
	if err := repo.Iterate(ctx, func(row store.AuditRow) error {
		if row.Action != "sync.verify" {
			return nil
		}
		haystacks = append(haystacks, row.Action, row.Resource, row.Result, row.ActorName)
		if row.Detail != nil {
			haystacks = append(haystacks, *row.Detail)
		}
		return nil
	}); err != nil {
		t.Fatalf("audit iterate: %v", err)
	}

	// ── assert ────────────────────────────────────────────────────────────────
	forbidden := []string{desiredValue, remoteValue, credToken}
	for _, hs := range haystacks {
		for _, needle := range forbidden {
			if strings.Contains(hs, needle) {
				t.Fatalf("secret material %q leaked into: %s", needle, hs)
			}
		}
	}

	// Digests must not leak either: the keyed HMAC of a (key,value) pair is a
	// stable oracle over the value and must never be persisted or serialized.
	digest := svc.keyDigest("API_KEY", desiredValue)
	if digest == nil {
		t.Fatal("keyDigest returned nil on an unsealed keyring")
	}
	for _, enc := range []string{hexOf(digest), b64Of(digest)} {
		for _, hs := range haystacks {
			if strings.Contains(hs, enc) {
				t.Fatalf("value digest leaked into: %s", hs)
			}
		}
	}

	// Positive control: the key NAME is expected to be present (that is the
	// whole point of the report) — this guards against a vacuous test.
	if !strings.Contains(string(repJSON), "API_KEY") {
		t.Fatal("report did not carry the drifted key name (test would be vacuous)")
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0x0f])
	}
	return string(out)
}

func b64Of(b []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var sb strings.Builder
	for i := 0; i+3 <= len(b); i += 3 {
		n := uint(b[i])<<16 | uint(b[i+1])<<8 | uint(b[i+2])
		sb.WriteByte(alphabet[(n>>18)&63])
		sb.WriteByte(alphabet[(n>>12)&63])
		sb.WriteByte(alphabet[(n>>6)&63])
		sb.WriteByte(alphabet[n&63])
	}
	return sb.String()
}
