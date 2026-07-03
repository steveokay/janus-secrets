package secrets

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

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
		{Key: "bad-key", Value: []byte("v")},
	}, "m", "u"); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid key: got %v, want ErrValidation", err)
	}
}
