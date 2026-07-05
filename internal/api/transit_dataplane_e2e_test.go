package api

import (
	"encoding/base64"
	"testing"
)

func TestTransitDataPlaneE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	cookie := login(t, ts.URL, email, password)
	mk := func(path, body string, out any) int { return doAuthed(t, "POST", ts.URL+path, cookie, "", body, out) }

	_ = doAuthed(t, "POST", ts.URL+"/v1/transit/keys", cookie, "", `{"name":"app","type":"aes256-gcm"}`, nil)

	pt := base64.StdEncoding.EncodeToString([]byte("hello"))
	var enc struct {
		Ciphertext string `json:"ciphertext"`
	}
	if code := mk("/v1/transit/encrypt/app", `{"plaintext":"`+pt+`"}`, &enc); code != 200 || enc.Ciphertext == "" {
		t.Fatalf("encrypt: %d %q", code, enc.Ciphertext)
	}
	var dec struct {
		Plaintext string `json:"plaintext"`
	}
	if code := mk("/v1/transit/decrypt/app", `{"ciphertext":"`+enc.Ciphertext+`"}`, &dec); code != 200 {
		t.Fatalf("decrypt: %d", code)
	}
	if got, _ := base64.StdEncoding.DecodeString(dec.Plaintext); string(got) != "hello" {
		t.Fatalf("decrypt roundtrip: %q", got)
	}
}
