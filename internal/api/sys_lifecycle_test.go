package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
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
	srv := New(Config{SealType: crypto.SealTypeAWSKMS}, kr, u, seals, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
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
