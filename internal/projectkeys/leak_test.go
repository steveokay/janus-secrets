package projectkeys

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// sentinelKEKValue is a distinctive secret VALUE plaintext. A project-KEK
// rotation + rewrap only re-wraps 32-byte DEKs onto the latest KEK; it must
// NEVER open, log, or require a secret value. If this string ever surfaces in
// captured log output during Rotate/Rewrap, a value plaintext leaked — a
// security bug, not a test bug.
const sentinelKEKValue = "SENTINEL-KEK-ROTATE-9f3a2b"

// syncBuf is a goroutine-safe buffer for capturing slog output. The store's
// query path may log from background/pool goroutines, so writes must be
// serialized against the test's later read.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestRewrapDoesNotLeakSecretValue is the value-free proof for project-KEK
// rotation. It writes a secret with a sentinel plaintext value, then corrupts
// that exact row's value ciphertext to garbage at the STORE level, then rotates
// and rewraps the project KEK. Both operations MUST succeed despite the
// unreadable ciphertext — the strongest evidence that rewrap only touches
// wrapped_dek and never opens the value. Finally it asserts the sentinel never
// appears in any log output captured for the duration of the operation.
//
// The test is non-vacuous: the sentinel is genuinely written (landing at
// dek_key_version=1), and the corrupted-value row is genuinely processed by the
// rewrap sweep (Rewrapped >= 1).
func TestRewrapDoesNotLeakSecretValue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	projectID, configID := h.mkChain(t)

	// 1. Write the sentinel secret value. It lands at dek_key_version=1.
	writeSecret(t, h.sec, configID, "CANARY", sentinelKEKValue)

	// Sanity: the sentinel really round-trips through the read path before we
	// corrupt it, so we know it was genuinely encrypted-at-rest under KEK v1.
	if got := reveal(t, h.sec, configID, "CANARY"); got != sentinelKEKValue {
		t.Fatalf("pre-corrupt CANARY = %q, want sentinel", got)
	}

	// 2. Corrupt exactly the CANARY row's value ciphertext to garbage. This
	//    leaves wrapped_dek intact but makes the VALUE undecryptable — so if
	//    rewrap ever tried to open the value it would fail (or leak the attempt).
	if _, err := testPool.Exec(ctx,
		`UPDATE secret_values SET ciphertext = E'\\xdeadbeef'
		  WHERE config_id=$1::uuid AND key='CANARY'`, configID); err != nil {
		t.Fatalf("corrupt CANARY ciphertext: %v", err)
	}

	// 3. Capture all log output for the duration of the operation. The projectkeys
	//    Service takes no logger, so we capture the process-wide slog default,
	//    which covers anything store/secrets/crypto might emit during the sweep.
	var logBuf syncBuf
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(prev)

	// 4. Rotate then Rewrap — both must SUCCEED despite the corrupted ciphertext.
	if _, err := h.svc.Rotate(ctx, projectID); err != nil {
		t.Fatalf("Rotate must succeed despite corrupt value ciphertext: %v", err)
	}
	res, err := h.svc.Rewrap(ctx, projectID)
	if err != nil {
		// A failure here would mean rewrap read the (corrupted) value ciphertext —
		// which it must never do. That is a real product bug, not a test bug.
		t.Fatalf("Rewrap must succeed despite corrupt value ciphertext (rewrap must never open a value): %v", err)
	}

	// 6. Non-vacuous: the corrupted-value row actually went through the rewrap
	//    path. Rewrapped >= 1 proves the sweep processed the CANARY row.
	if res.Rewrapped < 1 {
		t.Fatalf("Rewrapped = %d, want >= 1 (the corrupted CANARY row must have been rewrapped, else the test is vacuous)", res.Rewrapped)
	}

	// 5. The sentinel plaintext must NEVER appear in captured log output.
	if logs := logBuf.String(); strings.Contains(logs, sentinelKEKValue) {
		t.Fatalf("secret value sentinel %q leaked into log output during rotate/rewrap:\n%s", sentinelKEKValue, logs)
	}
}
