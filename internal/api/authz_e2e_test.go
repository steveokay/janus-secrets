package api

import (
	"fmt"
	"strings"
	"testing"
)

// The bootstrap admin is instance owner, so the M5 token lifecycle still works.
// New here: a service-token principal cannot mint another token, and an
// unauthenticated seal is 401 while an owner seal is 200.
func TestTokenPrincipalCannotMint(t *testing.T) {
	ts, email, password, configID := authStack(t)
	cookie := login(t, ts.URL, email, password)

	// Owner mints a readwrite token.
	var minted struct {
		Token string `json:"token"`
	}
	body := fmt.Sprintf(`{"name":"ci","scope":{"kind":"config","id":%q},"access":"readwrite"}`, configID)
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", cookie, "", body, &minted); code != 200 {
		t.Fatalf("owner mint: %d", code)
	}

	// That token tries to mint another → 403 (tokens lack token:mint).
	var env errEnvelope
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", "", minted.Token,
		fmt.Sprintf(`{"name":"x","scope":{"kind":"config","id":%q},"access":"read"}`, configID), &env); code != 403 || env.Error.Code != "forbidden" {
		t.Fatalf("token mint: %d %+v", code, env)
	}
}

func TestSealAuthzGate(t *testing.T) {
	ts, email, password, _ := authStack(t)
	// Unauthenticated → 401 (RequireAuth first).
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/seal", "", "", "", nil); code != 401 {
		t.Fatalf("unauth seal: %d", code)
	}
	// Owner → 200.
	cookie := login(t, ts.URL, email, password)
	var sealResp struct{ Sealed bool }
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/seal", cookie, "", "", &sealResp); code != 200 || !sealResp.Sealed {
		t.Fatalf("owner seal: %d %+v", code, sealResp)
	}
	_ = strings.TrimSpace
}
