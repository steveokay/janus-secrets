package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
)

// TestSearchKeysNoValueInResponseOrLogs proves the search path never surfaces a
// secret VALUE: it boots a real stack with a captured log buffer, seeds a config
// whose secret carries a distinctive sentinel value, runs a search that matches
// the KEY name, and asserts the sentinel value appears in neither the raw search
// response body nor the server logs. (The key name itself is metadata and does
// appear — that is the feature.)
func TestSearchKeysNoValueInResponseOrLogs(t *testing.T) {
	const sentinel = "S3NT1NEL-do-not-leak-this-value-9x7q"

	var logBuf syncBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	dsn := bootPostgres(t)
	ctx := context.Background()
	srv, st, err := Boot(ctx, BootConfig{DatabaseURL: dsn, SealType: crypto.SealTypeShamir, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	var ir struct {
		Shares []string `json:"shares"`
		Admin  *struct{ Email, Password string } `json:"admin"`
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init",
		`{"shares":1,"threshold":1,"admin_email":"root@corp.io"}`, &ir); code != 200 {
		t.Fatalf("init: %d", code)
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal",
		fmt.Sprintf(`{"share":%q}`, ir.Shares[0]), nil); code != 200 {
		t.Fatalf("unseal failed")
	}

	p, err := srv.service.CreateProject(ctx, "leak", "Leak")
	if err != nil {
		t.Fatal(err)
	}
	e, err := srv.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	c, err := srv.service.CreateConfig(ctx, e.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.service.SetSecrets(ctx, c.ID,
		[]secrets.SecretChange{{Key: "LEAK_SEARCH_KEY", Value: []byte(sentinel)}}, "seed", "admin"); err != nil {
		t.Fatal(err)
	}

	cookie := login(t, ts.URL, ir.Admin.Email, ir.Admin.Password)

	// Search matching the KEY name. Capture the raw response body.
	req, _ := http.NewRequest("GET", ts.URL+"/v1/search/keys?q=LEAK_SEARCH", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body := readAllString(t, resp)
	resp.Body.Close()

	// Sanity: the key name (metadata) is present — the search actually matched.
	if !strings.Contains(body, "LEAK_SEARCH_KEY") {
		t.Fatalf("search did not match the seeded key; body=%s", body)
	}
	// The sentinel VALUE must never appear in the response.
	if strings.Contains(body, sentinel) {
		t.Fatalf("secret value LEAKED into the search response body")
	}
	// Nor in any server log line.
	if strings.Contains(logBuf.String(), sentinel) {
		t.Fatalf("secret value LEAKED into server logs")
	}
}
