package secrets

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// TestNonCanonicalConfigIDRoundTrip writes through an uppercase spelling of the
// config UUID and reads back through the canonical lowercase one (and vice
// versa). Postgres normalizes both to the same row; the DEK AAD must too —
// it is built from the resolved cfg.ID, not the caller's spelling. Before that
// fix, mixing spellings produced differing AAD bytes and a spurious ErrDecrypt.
func TestNonCanonicalConfigIDRoundTrip(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)
	upper := strings.ToUpper(configID)

	// Write via the uppercase spelling, read via the canonical one.
	if _, err := s.SetSecrets(ctx, upper, []SecretChange{
		{Key: "K", Value: []byte("v1")},
	}, "m", "u"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSecret(ctx, configID, "K")
	if err != nil {
		t.Fatalf("read canonical after uppercase write: %v", err)
	}
	if !bytes.Equal(got.Value, []byte("v1")) {
		t.Fatalf("K = %q, want v1", got.Value)
	}

	// And the reverse: write canonical, read uppercase (covers RevealConfig too).
	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "K", Value: []byte("v2")},
	}, "m", "u"); err != nil {
		t.Fatal(err)
	}
	_, all, err := s.RevealConfig(ctx, upper)
	if err != nil {
		t.Fatalf("reveal uppercase after canonical write: %v", err)
	}
	if !bytes.Equal(all["K"].Value, []byte("v2")) {
		t.Fatalf("K = %q, want v2", all["K"].Value)
	}
}

func TestSetGetRoundTrip(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)

	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "DB_URL", Value: []byte("postgres://secret")},
		{Key: "API_KEY", Value: []byte("sk-live-123")},
	}, "initial", "alice"); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetSecret(ctx, configID, "DB_URL")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Value, []byte("postgres://secret")) || got.ValueVersion != 1 {
		t.Fatalf("GetSecret DB_URL: %q v%d", got.Value, got.ValueVersion)
	}

	cv, all, err := s.RevealConfig(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	if cv.Version != 1 || len(all) != 2 {
		t.Fatalf("RevealConfig: v%d keys=%d", cv.Version, len(all))
	}
	if !bytes.Equal(all["API_KEY"].Value, []byte("sk-live-123")) {
		t.Fatalf("API_KEY = %q", all["API_KEY"].Value)
	}
}

func TestSetThenDeleteInBatch(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)

	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "K", Value: []byte("v")},
		{Key: "K", Delete: true},
	}, "set-then-delete", "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSecret(ctx, configID, "K"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("K after set-then-delete: got %v, want ErrNotFound", err)
	}
}

func TestGetSecretMissing(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)
	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "PRESENT", Value: []byte("x")},
	}, "m", "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSecret(ctx, configID, "ABSENT"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key: got %v, want ErrNotFound", err)
	}
}

func TestSetSecretsSealed(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)
	s.keyring.Seal()
	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "K", Value: []byte("v")},
	}, "m", "u"); !errors.Is(err, ErrSealed) {
		t.Fatalf("SetSecrets sealed: got %v, want ErrSealed", err)
	}
}

func TestSetSecretsInvalidKey(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)
	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "bad key", Value: []byte("v")},
	}, "m", "u"); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid key: got %v, want ErrValidation", err)
	}
}

func TestSetSecretsCarriesTypeToReveal(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)

	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "PAYLOAD", Value: []byte(`{"a":1}`), Type: "json"},
	}, "m", "u"); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetSecret(ctx, configID, "PAYLOAD")
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != "json" {
		t.Fatalf("GetSecret Type = %q, want json", got.Type)
	}

	_, all, err := s.RevealConfig(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	if all["PAYLOAD"].Type != "json" {
		t.Fatalf("RevealConfig Type = %q, want json", all["PAYLOAD"].Type)
	}
}

func TestSetSecretsInvalidType(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)
	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "K", Value: []byte("v"), Type: "bogus"},
	}, "m", "u"); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid type: got %v, want ErrValidation", err)
	}
}
