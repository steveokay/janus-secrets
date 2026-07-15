package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestServiceTokenRepo(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	hash := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"
	u, err := NewUserRepo(s).Create(ctx, "a@b.c", &hash)
	if err != nil {
		t.Fatal(err)
	}
	_, _, configID := mkConfig(t, s, "prod")
	repo := NewServiceTokenRepo(s)

	mac := []byte("hmac-of-raw-token-0123456789abcd")
	exp := time.Now().Add(24 * time.Hour)
	tok, err := repo.Create(ctx, "ci-token", mac, u.ID, "config", configID, "read", &exp)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Name != "ci-token" || tok.ScopeKind != "config" || tok.Access != "read" || tok.ExpiresAt == nil {
		t.Fatalf("unexpected token: %+v", tok)
	}

	got, err := repo.GetByHMAC(ctx, mac)
	if err != nil || got.ID != tok.ID {
		t.Fatalf("GetByHMAC: %+v err=%v", got, err)
	}

	list, err := repo.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: len=%d err=%v", len(list), err)
	}

	// Revoke sets revoked_at; second revoke is ErrNotFound.
	if err := repo.Revoke(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetByHMAC(ctx, mac)
	if got.RevokedAt == nil {
		t.Fatal("revoked_at not set")
	}
	if err := repo.Revoke(ctx, tok.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double revoke: %v", err)
	}

	// Invalid enum rejected by the DB CHECK.
	if _, err := repo.Create(ctx, "bad", []byte("other-mac-9999999999999999999999"), u.ID, "project", configID, "read", nil); err == nil {
		t.Fatal("scope_kind CHECK should reject 'project'")
	}
}

func TestServiceTokenRepo_ListPage(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	hash := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"
	u, err := NewUserRepo(s).Create(ctx, "a@b.c", &hash)
	if err != nil {
		t.Fatal(err)
	}
	_, _, configID := mkConfig(t, s, "prod")
	repo := NewServiceTokenRepo(s)

	for i := 0; i < 5; i++ {
		mac := []byte(fmt.Sprintf("hmac-of-raw-token-%013d", i))
		if _, err := repo.Create(ctx, fmt.Sprintf("tok-%d", i), mac, u.ID, "config", configID, "read", nil); err != nil {
			t.Fatal(err)
		}
	}

	all, err := repo.ListPage(ctx, 0, nil)
	if err != nil || len(all) != 5 {
		t.Fatalf("unbounded: len=%d err=%v", len(all), err)
	}
	// DESC order: created_at descending (with id tiebreak).
	for i := 1; i < len(all); i++ {
		prev, cur := all[i-1], all[i]
		if cur.CreatedAt.After(prev.CreatedAt) {
			t.Fatalf("not DESC by created_at at %d", i)
		}
		if cur.CreatedAt.Equal(prev.CreatedAt) && cur.ID > prev.ID {
			t.Fatalf("not DESC by id tiebreak at %d", i)
		}
	}

	seen := map[string]bool{}
	var after *Cursor
	for {
		page, err := repo.ListPage(ctx, 2, after)
		if err != nil {
			t.Fatal(err)
		}
		for _, tk := range page {
			if seen[tk.ID] {
				t.Fatalf("duplicate id %s", tk.ID)
			}
			seen[tk.ID] = true
		}
		if len(page) < 2 {
			break
		}
		last := page[len(page)-1]
		after = &Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	if len(seen) != 5 {
		t.Fatalf("covered %d of 5", len(seen))
	}
}

func TestAuthConfigStore(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewAuthConfigRepo(s)

	if _, err := repo.GetWrappedHMACKey(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty get: %v", err)
	}
	if err := repo.PutWrappedHMACKeyIfAbsent(ctx, []byte("wrapped-1")); err != nil {
		t.Fatal(err)
	}
	// Second put is a no-op (first writer wins).
	if err := repo.PutWrappedHMACKeyIfAbsent(ctx, []byte("wrapped-2")); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetWrappedHMACKey(ctx)
	if err != nil || string(got) != "wrapped-1" {
		t.Fatalf("get = %q err=%v, want wrapped-1", got, err)
	}
}
