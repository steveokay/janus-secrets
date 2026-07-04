package store

import (
	"context"
	"errors"
	"testing"
)

func TestUserRepoCRUD(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewUserRepo(s)

	hash := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"
	u, err := repo.Create(ctx, "Admin@Example.com", &hash)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == "" || u.Email != "Admin@Example.com" || u.PasswordHash == nil || *u.PasswordHash != hash {
		t.Fatalf("unexpected user: %+v", u)
	}

	// Lookup is case-insensitive on email.
	got, err := repo.GetByEmail(ctx, "admin@EXAMPLE.com")
	if err != nil || got.ID != u.ID {
		t.Fatalf("GetByEmail: %+v err=%v", got, err)
	}
	if _, err := repo.Get(ctx, u.ID); err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Duplicate email (any case) rejected.
	if _, err := repo.Create(ctx, "ADMIN@example.COM", &hash); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup email: got %v, want ErrAlreadyExists", err)
	}

	// Nullable hash (future OIDC user).
	nop, err := repo.Create(ctx, "oidc@example.com", nil)
	if err != nil || nop.PasswordHash != nil {
		t.Fatalf("nil-hash user: %+v err=%v", nop, err)
	}

	// UpdatePassword.
	newHash := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$bmV3aGFzaA"
	if err := repo.UpdatePassword(ctx, u.ID, newHash); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Get(ctx, u.ID)
	if *got.PasswordHash != newHash {
		t.Fatalf("hash not updated: %q", *got.PasswordHash)
	}

	// Count.
	n, err := repo.Count(ctx)
	if err != nil || n != 2 {
		t.Fatalf("Count = %d err=%v, want 2", n, err)
	}

	// Missing lookups.
	if _, err := repo.GetByEmail(ctx, "nobody@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing email: %v", err)
	}
	if err := repo.UpdatePassword(ctx, "00000000-0000-0000-0000-000000000000", newHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
}
