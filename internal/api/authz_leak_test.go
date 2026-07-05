package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// A denied request must return the generic envelope and never leak role names,
// binding rows, action strings, SQL, or the word "owner"/"admin" that would hint
// at the policy structure.
func TestForbiddenBodyHasNoPolicyInternals(t *testing.T) {
	ts, email, password, configID := authStack(t)
	cookie := login(t, ts.URL, email, password)

	// Mint a read-only token, then have it attempt a mint → 403.
	var minted struct {
		Token string `json:"token"`
	}
	body := `{"name":"ro","scope":{"kind":"config","id":"` + configID + `"},"access":"read"}`
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", cookie, "", body, &minted); code != 200 {
		t.Fatalf("mint ro token: %d", code)
	}
	raw := captureForbiddenBody(t, ts.URL, minted.Token, configID)

	for _, bad := range []string{"owner", "admin", "developer", "viewer", "role_binding", "binding", "select", "token:mint", "scope_level", "instance"} {
		if strings.Contains(strings.ToLower(raw), bad) {
			t.Errorf("403 body leaked policy internal %q: %s", bad, raw)
		}
	}
}

// captureForbiddenBody triggers a token-principal mint (denied) and returns the
// raw response body.
func captureForbiddenBody(t *testing.T, base, token, configID string) string {
	t.Helper()
	body := `{"name":"x","scope":{"kind":"config","id":"` + configID + `"},"access":"read"}`
	return rawPost(t, base+"/v1/tokens", token, body, 403)
}

// rawPost sends an authenticated (Bearer) POST and returns the raw body,
// asserting the status code.
func rawPost(t *testing.T, url, bearer, body string, wantStatus int) string {
	t.Helper()
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d (body %s)", resp.StatusCode, wantStatus, b)
	}
	return string(b)
}
