package secrets

import (
	"context"
	"errors"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

func TestCreateProjectWrapsKEK(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	p, err := s.CreateProject(ctx, "billing", "Billing")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" || p.Slug != "billing" || p.KEKVersion != 1 {
		t.Fatalf("unexpected project: %+v", p)
	}
	// The stored wrapped KEK must be a parseable, unwrappable ciphertext bound
	// to the project id.
	ct, err := crypto.ParseCiphertext(p.WrappedKEK)
	if err != nil {
		t.Fatalf("wrapped KEK not parseable: %v", err)
	}
	kek, err := s.keyring.UnwrapProjectKEK(ct, p.ID)
	if err != nil {
		t.Fatalf("unwrap KEK: %v", err)
	}
	if len(kek) != crypto.KeySize {
		t.Fatalf("unwrapped KEK size = %d, want %d", len(kek), crypto.KeySize)
	}
	// Wrong project id must fail (AAD binding).
	if _, err := s.keyring.UnwrapProjectKEK(ct, "00000000-0000-0000-0000-000000000000"); err == nil {
		t.Fatal("unwrap with wrong project id should fail")
	}
}

func TestCreateProjectSealed(t *testing.T) {
	s := newService(t)
	s.keyring.Seal()
	if _, err := s.CreateProject(context.Background(), "x", "X"); !errors.Is(err, ErrSealed) {
		t.Fatalf("CreateProject sealed: got %v, want ErrSealed", err)
	}
}

func TestCreateProjectInvalidSlug(t *testing.T) {
	s := newService(t)
	if _, err := s.CreateProject(context.Background(), "  ", "X"); !errors.Is(err, ErrValidation) {
		t.Fatalf("blank slug: got %v, want ErrValidation", err)
	}
}
