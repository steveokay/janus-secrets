package rotation

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebhookRotatorSignsAndCommitsOn2xx(t *testing.T) {
	const key = "shhh"
	var gotSig, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotSig = r.Header.Get("X-Janus-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rot := webhookRotator{hc: srv.Client()}
	cfg := PolicyConfig{URL: srv.URL, HMACKey: key}
	err := rot.apply(context.Background(), cfg, "pol1", "secretKey", "newval123")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(gotBody))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != want {
		t.Fatalf("sig = %q, want %q", gotSig, want)
	}
	if !strings.Contains(gotBody, `"new_value":"newval123"`) {
		t.Fatalf("body missing value: %s", gotBody)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookRotatorFailsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	rot := webhookRotator{hc: srv.Client()}
	err := rot.apply(context.Background(), PolicyConfig{URL: srv.URL, HMACKey: "k"}, "p", "K", "v")
	if err == nil {
		t.Fatal("want error on 500")
	}
	if strings.Contains(err.Error(), "v") && strings.Contains(err.Error(), "secret") {
		t.Fatal("error must not leak the value")
	}
}
