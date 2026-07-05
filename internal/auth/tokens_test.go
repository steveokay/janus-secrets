package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// mkScope creates a project→env→config chain and returns (envID, configID).
func mkScope(t *testing.T) (string, string) {
	t.Helper()
	ctx := context.Background()
	id, err := testStore.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	emailSeq++ // reuse the sequence for unique slugs
	p, err := store.NewProjectRepo(testStore).Create(ctx, id,
		strings.ToLower(t.Name())+"-proj", "P", []byte("k"), 1)
	if err != nil {
		t.Fatal(err)
	}
	e, err := store.NewEnvironmentRepo(testStore).Create(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.NewConfigRepo(testStore).Create(ctx, e.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	return e.ID, c.ID
}

func TestServiceTokenLifecycle(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()
	cookie, _ := svc.Login(ctx, email, []byte(password))
	admin, _ := svc.VerifySession(ctx, cookie)
	_, configID := mkScope(t)

	raw, meta, err := svc.MintServiceToken(ctx, admin, "ci", "config", configID, "read", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "janus_svc_") {
		t.Fatalf("token format: %q", raw)
	}
	if meta.Name != "ci" || meta.ScopeKind != "config" || meta.Access != "read" {
		t.Fatalf("meta = %+v", meta)
	}

	p, _, err := svc.VerifyServiceToken(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != KindServiceToken || p.ID != meta.ID || p.Name != "ci" {
		t.Fatalf("principal = %+v", p)
	}

	list, err := svc.ListTokens(ctx)
	if err != nil || len(list) == 0 {
		t.Fatalf("list: %d err=%v", len(list), err)
	}

	if err := svc.RevokeToken(ctx, meta.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.VerifyServiceToken(ctx, raw); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked token verified: %v", err)
	}
	if err := svc.RevokeToken(ctx, meta.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double revoke: %v", err)
	}
}

func TestMintValidation(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()
	cookie, _ := svc.Login(ctx, email, []byte(password))
	admin, _ := svc.VerifySession(ctx, cookie)
	envID, configID := mkScope(t)

	cases := []struct {
		name, kind, id, access string
	}{
		{"", "config", configID, "read"},                                     // empty name
		{"x", "project", configID, "read"},                                   // bad kind
		{"x", "config", configID, "admin"},                                   // bad access
		{"x", "config", "00000000-0000-0000-0000-000000000000", "read"},      // missing scope
		{"x", "environment", "00000000-0000-0000-0000-000000000000", "read"}, // missing env
	}
	for _, c := range cases {
		if _, _, err := svc.MintServiceToken(ctx, admin, c.name, c.kind, c.id, c.access, nil); err == nil {
			t.Fatalf("mint %+v should fail", c)
		}
	}

	// Environment scope works.
	if _, _, err := svc.MintServiceToken(ctx, admin, "env-tok", "environment", envID, "readwrite", nil); err != nil {
		t.Fatal(err)
	}
}

func TestTokenExpiry(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()
	cookie, _ := svc.Login(ctx, email, []byte(password))
	admin, _ := svc.VerifySession(ctx, cookie)
	_, configID := mkScope(t)

	ttl := 1 * time.Millisecond
	raw, _, err := svc.MintServiceToken(ctx, admin, "flash", "config", configID, "read", &ttl)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, _, err := svc.VerifyServiceToken(ctx, raw); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired token verified: %v", err)
	}
}

func TestVerifyServiceTokenGarbage(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	for _, raw := range []string{"", "janus_svc_", "janus_svc_notbase64!!!", "bearer-junk", "janus_other_AAAA"} {
		if _, _, err := svc.VerifyServiceToken(ctx, raw); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("raw %q: %v", raw, err)
		}
	}
}
