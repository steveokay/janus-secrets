package api

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// TestNoShareMaterialInLogsOrErrors drives the full init/unseal lifecycle with
// log capture and asserts that no share hex ever appears in the logs, and that
// error-path responses never echo submitted share material.
func TestNoShareMaterialInLogsOrErrors(t *testing.T) {
	var logBuf bytes.Buffer
	seals := &memSealStore{}
	kr := crypto.NewKeyring()
	u := crypto.NewShamirUnsealer(seals, 0, 0)
	srv := New(Config{SealType: crypto.SealTypeShamir}, kr, u, seals, nil, nil,
		nil, nil, nil, slog.New(slog.NewTextHandler(&logBuf, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var ir initResp
	doJSON(t, "POST", ts.URL+"/v1/sys/init", `{"shares":5,"threshold":3}`, &ir)
	if len(ir.Shares) != 5 {
		t.Fatalf("init shares = %d", len(ir.Shares))
	}

	// Collect error-path response bodies: duplicate share, poisoned set.
	var errBodies []string
	post := func(body string) string {
		resp, err := http.Post(ts.URL+"/v1/sys/unseal", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	post(fmt.Sprintf(`{"share":%q}`, ir.Shares[0]))
	errBodies = append(errBodies, post(fmt.Sprintf(`{"share":%q}`, ir.Shares[0]))) // duplicate
	post(fmt.Sprintf(`{"share":%q}`, ir.Shares[1]))
	corrupted := "ff" + ir.Shares[2][2:]
	errBodies = append(errBodies, post(fmt.Sprintf(`{"share":%q}`, corrupted))) // poisons the set

	logs := logBuf.String()
	for i, sh := range ir.Shares {
		if strings.Contains(logs, sh) {
			t.Fatalf("share %d leaked into logs", i)
		}
		for _, eb := range errBodies {
			if strings.Contains(eb, sh) {
				t.Fatalf("share %d echoed in error response: %s", i, eb)
			}
		}
	}
	if strings.Contains(logs, corrupted) {
		t.Fatal("submitted share material leaked into logs")
	}
	// Sanity: the logger did log the requests (method/path/status).
	if !strings.Contains(logs, "/v1/sys/unseal") {
		t.Fatalf("expected request logs, got: %q", logs)
	}
}
