package api

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// TestOIDCFederationJWTNeverLeaks drives a full CI token exchange with a captured
// logger and asserts the raw JWT (a bearer credential) appears in neither the
// logs nor any audit_events row.
func TestOIDCFederationJWTNeverLeaks(t *testing.T) {
	dsn := bootPostgres(t)
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	ctx := context.Background()

	srv, st, err := Boot(ctx, BootConfig{
		DatabaseURL: dsn, SealType: crypto.SealTypeShamir, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	var ir struct {
		Shares []string `json:"shares"`
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init",
		`{"shares":1,"threshold":1,"admin_email":"root@corp.io"}`, &ir); code != 200 {
		t.Fatalf("init: %d", code)
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal",
		fmt.Sprintf(`{"share":%q}`, ir.Shares[0]), nil); code != 200 {
		t.Fatal("unseal failed")
	}

	// A real config scope for the binding (created directly via store repos;
	// federation does not decrypt, so a dummy wrapped KEK is fine).
	id, err := st.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.NewProjectRepo(st).Create(ctx, id, "leak-proj", "P", []byte("k"), 1)
	if err != nil {
		t.Fatal(err)
	}
	e, err := store.NewEnvironmentRepo(st).Create(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := store.NewConfigRepo(st).Create(ctx, e.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}

	idp := newMockIdP(t, "janus")
	if err := srv.auth.SetFederationConfig(ctx, auth.FederationConfigInput{
		Issuer: idp.srv.URL, Audience: "janus", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.auth.CreateFederationBinding(ctx, auth.FederationBindingInput{
		Name: "prod", MatchClaims: map[string]string{"repository": "org/app"},
		ScopeKind: "config", ScopeID: cfg.ID, Access: "read", TTLSeconds: 900, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	jwtTok := idp.signClaims(t, map[string]any{
		"iss": idp.srv.URL, "aud": "janus", "sub": "repo:org/app:ref:refs/heads/main",
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"repository": "org/app", "ref": "refs/heads/main",
	})
	var ok struct {
		Token string `json:"token"`
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/auth/oidc/federate", `{"token":"`+jwtTok+`"}`, &ok); code != 200 {
		t.Fatalf("exchange: %d", code)
	}
	if !strings.HasPrefix(ok.Token, "janus_svc_") {
		t.Fatalf("no minted token: %+v", ok)
	}

	// --- Assertions: the raw JWT must appear nowhere. ---
	logs := logBuf.String()
	if strings.Contains(logs, jwtTok) {
		t.Fatal("raw CI JWT leaked into logs")
	}
	if !strings.Contains(logs, "/v1/auth/oidc/federate") {
		t.Fatalf("expected request logs proving the logger was wired, got: %q", logs)
	}
	// The minted janus_svc_ token is shown once in the response but must not be
	// logged either.
	if strings.Contains(logs, ok.Token) {
		t.Fatal("minted service token leaked into logs")
	}

	rec := store.NewAuditRepo(st)
	if err := rec.Iterate(ctx, func(row store.AuditRow) error {
		detail := ""
		if row.Detail != nil {
			detail = *row.Detail
		}
		resultCode := ""
		if row.ResultCode != nil {
			resultCode = *row.ResultCode
		}
		hay := row.Action + row.Resource + detail + row.Result + resultCode
		if strings.Contains(hay, jwtTok) || strings.Contains(hay, ok.Token) {
			t.Fatalf("credential leaked into audit row: %+v", row)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
