package masterkeys

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// sentinelMasterValue is a distinctive secret VALUE plaintext. A master-key
// rotation only re-wraps master-wrapped blobs (project KEKs, auth key, etc.)
// under a fresh master; it must NEVER open, log, or require a secret value. If
// this string ever surfaces in captured log output during Rotate, a value
// plaintext leaked — a security bug, not a test bug.
const sentinelMasterValue = "SENTINEL-MASTER-ROTATE-7b1c"

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

// TestRotateDoesNotLeakSecretValue is the value-free proof for master-key
// rotation. It writes a secret with a sentinel plaintext value, then corrupts
// that exact row's value ciphertext to garbage at the STORE level, then rotates
// the master key. The rotation MUST succeed despite the unreadable ciphertext —
// the strongest evidence that rotation only re-wraps KEKs and never opens the
// value. Finally it asserts the sentinel never appears in any log output
// captured for the duration of the operation.
func TestRotateDoesNotLeakSecretValue(t *testing.T) {
	h := newKMSHarness(t)
	ctx := context.Background()
	_, configID := h.mkChain(t)

	// 1. Write the sentinel secret value.
	writeSecret(t, h.sec, configID, "CANARY", sentinelMasterValue)

	// Sanity: the sentinel really round-trips before we corrupt it, so we know it
	// was genuinely encrypted-at-rest.
	if got := reveal(t, h.sec, configID, "CANARY"); got != sentinelMasterValue {
		t.Fatalf("pre-corrupt CANARY = %q, want sentinel", got)
	}

	// 2. Corrupt exactly the CANARY row's value ciphertext to garbage. This leaves
	//    the KEK/wrapped_dek intact but makes the VALUE undecryptable — so if
	//    rotation ever tried to open the value it would fail (or leak the attempt).
	if _, err := testPool.Exec(ctx,
		`UPDATE secret_values SET ciphertext = E'\\xdeadbeef'
		  WHERE config_id=$1::uuid AND key='CANARY'`, configID); err != nil {
		t.Fatalf("corrupt CANARY ciphertext: %v", err)
	}

	// 3. Capture all log output for the duration of the operation. The masterkeys
	//    Service takes no logger, so we capture the process-wide slog default,
	//    which covers anything store/secrets/crypto might emit during rotation.
	var logBuf syncBuf
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(prev)

	// 4. Rotate — must SUCCEED despite the corrupted ciphertext.
	if _, err := h.svc.Rotate(ctx); err != nil {
		t.Fatalf("Rotate must succeed despite corrupt value ciphertext (rotation must never open a value): %v", err)
	}

	// 5. The sentinel plaintext must NEVER appear in captured log output.
	if logs := logBuf.String(); strings.Contains(logs, sentinelMasterValue) {
		t.Fatalf("secret value sentinel %q leaked into log output during rotation:\n%s", sentinelMasterValue, logs)
	}
}
