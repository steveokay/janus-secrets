package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

type initResp struct {
	Type   string   `json:"type"`
	Shares []string `json:"shares"`
}

type unsealResp struct {
	Sealed   bool          `json:"sealed"`
	Progress *progressBody `json:"progress"`
}

func TestShamirFullLifecycle(t *testing.T) {
	srv, ts, _ := newShamirTestServer(t)

	// Init 3-of-5.
	var ir initResp
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init", `{"shares":5,"threshold":3}`, &ir); code != 200 {
		t.Fatalf("init status = %d", code)
	}
	if ir.Type != "shamir" || len(ir.Shares) != 5 {
		t.Fatalf("init resp = %+v", ir)
	}
	for _, sh := range ir.Shares {
		if _, err := hex.DecodeString(sh); err != nil {
			t.Fatalf("share %q not hex: %v", sh, err)
		}
	}

	// Still sealed after init.
	if !srv.keyring.Sealed() {
		t.Fatal("keyring must stay sealed after shamir init")
	}

	// Double init → 409.
	var env errEnvelope
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init", `{}`, &env); code != 409 || env.Error.Code != CodeAlreadyInitialized {
		t.Fatalf("double init: code=%d env=%+v", code, env)
	}

	// Two shares → still sealed, progress reported.
	var ur unsealResp
	for i := 0; i < 2; i++ {
		body := fmt.Sprintf(`{"share":%q}`, ir.Shares[i])
		if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", body, &ur); code != 200 {
			t.Fatalf("unseal %d status = %d", i, code)
		}
	}
	if !ur.Sealed || ur.Progress == nil || ur.Progress.Submitted != 2 || ur.Progress.Required != 3 {
		t.Fatalf("after 2 shares: %+v", ur)
	}

	// Duplicate share → 400 duplicate_share.
	body := fmt.Sprintf(`{"share":%q}`, ir.Shares[0])
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", body, &env); code != 400 || env.Error.Code != CodeDuplicateShare {
		t.Fatalf("dup share: code=%d env=%+v", code, env)
	}

	// Third share → unsealed.
	body = fmt.Sprintf(`{"share":%q}`, ir.Shares[2])
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", body, &ur); code != 200 {
		t.Fatalf("third share status = %d", code)
	}
	if ur.Sealed || srv.keyring.Sealed() {
		t.Fatalf("should be unsealed: %+v", ur)
	}

	// Seal again via sys/seal.
	var sr struct {
		Sealed bool `json:"sealed"`
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/seal", "", &sr); code != 200 || !sr.Sealed {
		t.Fatalf("seal: code=%d resp=%+v", code, sr)
	}
	if !srv.keyring.Sealed() {
		t.Fatal("keyring should be sealed after sys/seal")
	}
}

func TestShamirPoisonedSetRecovery(t *testing.T) {
	_, ts, _ := newShamirTestServer(t)
	var ir initResp
	doJSON(t, "POST", ts.URL+"/v1/sys/init", `{"shares":5,"threshold":3}`, &ir)

	// Two good shares + one syntactically-valid wrong share.
	for i := 0; i < 2; i++ {
		doJSON(t, "POST", ts.URL+"/v1/sys/unseal", fmt.Sprintf(`{"share":%q}`, ir.Shares[i]), nil)
	}
	raw, _ := hex.DecodeString(ir.Shares[2])
	raw[0] ^= 0xFF // corrupt
	var env errEnvelope
	code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal",
		fmt.Sprintf(`{"share":%q}`, hex.EncodeToString(raw)), &env)
	if code != 400 || env.Error.Code != CodeKeyCheckFailed {
		t.Fatalf("poisoned set: code=%d env=%+v", code, env)
	}

	// Reset, then clean resubmission succeeds.
	var rr unsealResp
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal/reset", "", &rr); code != 200 || rr.Progress.Submitted != 0 {
		t.Fatalf("reset: code=%d resp=%+v", code, rr)
	}
	var ur unsealResp
	for i := 0; i < 3; i++ {
		doJSON(t, "POST", ts.URL+"/v1/sys/unseal", fmt.Sprintf(`{"share":%q}`, ir.Shares[i]), &ur)
	}
	if ur.Sealed {
		t.Fatalf("after clean resubmission: %+v", ur)
	}
}

func TestUnsealValidation(t *testing.T) {
	_, ts, _ := newShamirTestServer(t)
	var env errEnvelope

	// Unseal before init → 400 not_initialized.
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", `{"share":"abcd"}`, &env); code != 400 || env.Error.Code != CodeNotInitialized {
		t.Fatalf("pre-init unseal: code=%d env=%+v", code, env)
	}

	doJSON(t, "POST", ts.URL+"/v1/sys/init", `{}`, nil) // 3-of-5 defaults

	// Missing share under shamir → 400 validation.
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", `{}`, &env); code != 400 || env.Error.Code != CodeValidation {
		t.Fatalf("missing share: code=%d env=%+v", code, env)
	}
	// Non-hex share → 400 invalid_share.
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", `{"share":"zznothex"}`, &env); code != 400 || env.Error.Code != CodeInvalidShare {
		t.Fatalf("bad hex: code=%d env=%+v", code, env)
	}
}

// fakeKMS mirrors the crypto package's test fake: reversible prefix transform.
type fakeKMS struct{ fail bool }

func (f *fakeKMS) Encrypt(_ context.Context, pt []byte) ([]byte, error) {
	if f.fail {
		return nil, fmt.Errorf("simulated kms outage")
	}
	return append([]byte("wrapped:"), pt...), nil
}

func (f *fakeKMS) Decrypt(_ context.Context, ct []byte) ([]byte, error) {
	if f.fail {
		return nil, fmt.Errorf("simulated kms outage")
	}
	// Return a copy: a bare subslice would alias memSealStore's stored
	// WrappedMasterKey, so unsealNow's zeroization would corrupt the seal
	// config. Real stores/clients always return fresh slices.
	return append([]byte(nil), ct[len("wrapped:"):]...), nil
}

func newKMSTestServer(t *testing.T, client crypto.KMSClient) (*Server, *httptest.Server) {
	t.Helper()
	seals := &memSealStore{}
	kr := crypto.NewKeyring()
	u := crypto.NewKMSUnsealer(seals, client)
	srv := New(Config{SealType: crypto.SealTypeAWSKMS}, kr, u, seals, nil, nil,
		nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func TestKMSInitAutoUnseals(t *testing.T) {
	srv, ts := newKMSTestServer(t, &fakeKMS{})
	var ir initResp
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init", "", &ir); code != 200 || ir.Type != "awskms" || len(ir.Shares) != 0 {
		t.Fatalf("kms init: code=%d resp=%+v", code, ir)
	}
	if srv.keyring.Sealed() {
		t.Fatal("kms init must auto-unseal")
	}

	// shares/threshold under kms → 400 validation (on a fresh server).
	srv2, ts2 := newKMSTestServer(t, &fakeKMS{})
	_ = srv2
	var env errEnvelope
	if code := doJSON(t, "POST", ts2.URL+"/v1/sys/init", `{"shares":5,"threshold":3}`, &env); code != 400 || env.Error.Code != CodeValidation {
		t.Fatalf("kms init with shares: code=%d env=%+v", code, env)
	}
}

func TestKMSUnsealRetry(t *testing.T) {
	client := &fakeKMS{}
	srv, ts := newKMSTestServer(t, client)
	doJSON(t, "POST", ts.URL+"/v1/sys/init", "", nil)
	srv.keyring.Seal() // simulate a restart's sealed state

	client.fail = true
	var env errEnvelope
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", "", &env); code != 500 || env.Error.Code != CodeInternal {
		t.Fatalf("kms retry during outage: code=%d env=%+v", code, env)
	}

	client.fail = false
	var ur unsealResp
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", "", &ur); code != 200 || ur.Sealed {
		t.Fatalf("kms retry after recovery: code=%d resp=%+v", code, ur)
	}
	if srv.keyring.Sealed() {
		t.Fatal("keyring should be unsealed after retry")
	}
}

func TestInitParamValidation(t *testing.T) {
	_, ts, _ := newShamirTestServer(t)

	// Invalid parameter combinations are rejected before Init runs, so the
	// seal stays uninitialized across all of them.
	cases := []string{
		`{"shares":3,"threshold":5}`,   // threshold > shares
		`{"shares":5,"threshold":0}`,   // partial defaults
		`{"shares":300,"threshold":3}`, // shares > 255
		`{"shares":1,"threshold":2}`,   // not the 1-of-1 dev case
	}
	for _, body := range cases {
		var env errEnvelope
		if code := doJSON(t, "POST", ts.URL+"/v1/sys/init", body, &env); code != 400 || env.Error.Code != CodeValidation {
			t.Fatalf("init %s: code=%d env=%+v", body, code, env)
		}
	}

	// The 1-of-1 dev special case succeeds on a fresh server.
	_, ts2, _ := newShamirTestServer(t)
	var ir initResp
	if code := doJSON(t, "POST", ts2.URL+"/v1/sys/init", `{"shares":1,"threshold":1}`, &ir); code != 200 || ir.Type != "shamir" || len(ir.Shares) != 1 {
		t.Fatalf("1-of-1 init: code=%d resp=%+v", code, ir)
	}
}

func TestUnsealResetPreInitAndKMS(t *testing.T) {
	// Reset before init → 400 not_initialized.
	_, ts, _ := newShamirTestServer(t)
	var env errEnvelope
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal/reset", "", &env); code != 400 || env.Error.Code != CodeNotInitialized {
		t.Fatalf("pre-init reset: code=%d env=%+v", code, env)
	}

	// Reset on an initialized KMS seal → 400 validation (shamir-only op).
	_, kts := newKMSTestServer(t, &fakeKMS{})
	doJSON(t, "POST", kts.URL+"/v1/sys/init", "", nil)
	if code := doJSON(t, "POST", kts.URL+"/v1/sys/unseal/reset", "", &env); code != 400 || env.Error.Code != CodeValidation {
		t.Fatalf("kms reset: code=%d env=%+v", code, env)
	}
}

func TestConcurrentThresholdRace(t *testing.T) {
	srv, ts, _ := newShamirTestServer(t)
	var ir initResp
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init", `{"shares":5,"threshold":3}`, &ir); code != 200 {
		t.Fatalf("init status = %d", code)
	}
	for i := 0; i < 2; i++ {
		doJSON(t, "POST", ts.URL+"/v1/sys/unseal", fmt.Sprintf(`{"share":%q}`, ir.Shares[i]), nil)
	}

	// Fire the 3rd and 4th shares concurrently: both must observe success —
	// the loser's stale share is cleared, not left lingering, and it must not
	// see key_check_failed while the server is actually unsealed.
	type result struct {
		code int
		body unsealResp
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, idx := range []int{2, 3} {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			var ur unsealResp
			code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", fmt.Sprintf(`{"share":%q}`, ir.Shares[i]), &ur)
			results <- result{code: code, body: ur}
		}(idx)
	}
	close(start)
	wg.Wait()
	close(results)

	for res := range results {
		if res.code != 200 || res.body.Sealed {
			t.Fatalf("racer response: code=%d body=%+v", res.code, res.body)
		}
	}
	if srv.keyring.Sealed() {
		t.Fatal("keyring should be unsealed after the race")
	}

	// Re-seal and confirm no share lingers from the losing racer.
	doJSON(t, "POST", ts.URL+"/v1/sys/seal", "", nil)
	var ss struct {
		Sealed   bool          `json:"sealed"`
		Progress *progressBody `json:"progress"`
	}
	if code := doJSON(t, "GET", ts.URL+"/v1/sys/seal-status", "", &ss); code != 200 {
		t.Fatalf("seal-status code = %d", code)
	}
	if !ss.Sealed || ss.Progress == nil || ss.Progress.Submitted != 0 {
		t.Fatalf("post-race seal-status: %+v", ss)
	}
}

func TestUnsealShortCircuitWhenUnsealed(t *testing.T) {
	// Shamir: unseal while already unsealed → 200 sealed:false, and the
	// share is NOT consumed.
	_, ts, _ := newShamirTestServer(t)
	var ir initResp
	doJSON(t, "POST", ts.URL+"/v1/sys/init", `{"shares":5,"threshold":3}`, &ir)
	for i := 0; i < 3; i++ {
		doJSON(t, "POST", ts.URL+"/v1/sys/unseal", fmt.Sprintf(`{"share":%q}`, ir.Shares[i]), nil)
	}
	var ur unsealResp
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", fmt.Sprintf(`{"share":%q}`, ir.Shares[0]), &ur); code != 200 || ur.Sealed {
		t.Fatalf("unseal while unsealed: code=%d resp=%+v", code, ur)
	}

	// Re-seal; resubmitting the same share must NOT report duplicate_share,
	// because the short-circuited request never recorded it.
	doJSON(t, "POST", ts.URL+"/v1/sys/seal", "", nil)
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", fmt.Sprintf(`{"share":%q}`, ir.Shares[0]), &ur); code != 200 {
		t.Fatalf("resubmit after re-seal: code = %d", code)
	}
	if !ur.Sealed || ur.Progress == nil || ur.Progress.Submitted != 1 || ur.Progress.Required != 3 {
		t.Fatalf("resubmit after re-seal: %+v", ur)
	}

	// KMS: unseal while already unsealed (right after init) → 200.
	_, kts := newKMSTestServer(t, &fakeKMS{})
	doJSON(t, "POST", kts.URL+"/v1/sys/init", "", nil)
	if code := doJSON(t, "POST", kts.URL+"/v1/sys/unseal", "", &ur); code != 200 || ur.Sealed {
		t.Fatalf("kms unseal while unsealed: code=%d resp=%+v", code, ur)
	}
}

// TestConcurrentInitExactlyOneSucceeds guards the init serialization: without
// it, two racing inits could both return 200 while only one share set matches
// the stored seal — a false success carrying key material.
func TestConcurrentInitExactlyOneSucceeds(t *testing.T) {
	_, ts, _ := newShamirTestServer(t)

	const n = 4
	codes := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i] = doJSON(t, "POST", ts.URL+"/v1/sys/init", `{"shares":5,"threshold":3}`, nil)
		}(i)
	}
	close(start)
	wg.Wait()

	ok, conflict := 0, 0
	for _, c := range codes {
		switch c {
		case 200:
			ok++
		case 409:
			conflict++
		}
	}
	if ok != 1 || conflict != n-1 {
		t.Fatalf("concurrent init codes = %v, want exactly one 200 and %d 409s", codes, n-1)
	}
}

// TestComposedRouterDefaultClosesWhileSealed pins the wiring the sealed-state
// guarantee rests on: middleware installed before routes in New, so ANY
// non-sys path — including unregistered ones — returns 503 while sealed.
func TestComposedRouterDefaultClosesWhileSealed(t *testing.T) {
	_, ts, _ := newShamirTestServer(t)
	var env errEnvelope
	if code := doJSON(t, "GET", ts.URL+"/v1/projects", "", &env); code != 503 || env.Error.Code != CodeSealed {
		t.Fatalf("sealed non-sys route: code=%d env=%+v, want 503 sealed", code, env)
	}
}
