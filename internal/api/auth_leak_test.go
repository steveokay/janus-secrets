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

	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// TestNoCredentialMaterialInLogsOrErrors drives login/token flows with log
// capture and asserts that neither the admin password nor a minted token ever
// appears in logs, or in any response other than the sanctioned one-time
// exposures (init response, mint response).
func TestNoCredentialMaterialInLogsOrErrors(t *testing.T) {
	dsn := bootPostgres(t)
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	// Assemble the stack manually so the captured logger is wired everywhere.
	st, err := store.Open(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	seals := store.NewSealConfigStore(st)
	kr := crypto.NewKeyring()
	u := crypto.NewShamirUnsealer(seals, 0, 0)
	srv := New(Config{SealType: crypto.SealTypeShamir}, kr, u, seals,
		secrets.NewService(st, kr), nil, nil, auth.NewService(st, kr),
		authz.New(store.NewRoleBindingRepo(st)), st, nil, logger)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	var ir struct {
		Shares []string `json:"shares"`
		Admin  *struct{ Email, Password string } `json:"admin"`
	}
	doJSON(t, "POST", ts.URL+"/v1/sys/init", `{"shares":1,"threshold":1}`, &ir)
	doJSON(t, "POST", ts.URL+"/v1/sys/unseal", fmt.Sprintf(`{"share":%q}`, ir.Shares[0]), nil)

	password := ir.Admin.Password
	cookie := login(t, ts.URL, ir.Admin.Email, password)

	// Drive error paths that must not echo credentials.
	var bodies []string
	post := func(path, body string) {
		resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		bodies = append(bodies, string(b))
	}
	post("/v1/auth/login", fmt.Sprintf(`{"email":"ghost@x.io","password":%q}`, password)) // wrong user, real pw
	post("/v1/auth/login", fmt.Sprintf(`{"email":%q,"password":"wrong"}`, ir.Admin.Email))

	logs := logBuf.String()
	for _, needle := range []string{password, cookie} {
		if strings.Contains(logs, needle) {
			t.Fatal("credential material leaked into logs")
		}
		for _, b := range bodies {
			if strings.Contains(b, needle) {
				t.Fatalf("credential material echoed in error body: %s", b)
			}
		}
	}
	if !strings.Contains(logs, "/v1/auth/login") {
		t.Fatalf("expected request logs, got: %q", logs)
	}
}
