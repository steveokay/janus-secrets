package secrets

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func seedMaxAgeConfig(t *testing.T, s *Service) (context.Context, string) {
	t.Helper()
	ctx := context.Background()
	slug := fmt.Sprintf("maxage-%d", slugSeq.Add(1))
	p, err := s.CreateProject(ctx, slug, "P")
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConfig(ctx, e.ID, "cfg", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSecrets(ctx, c.ID, []SecretChange{
		{Key: "DATABASE_URL", Value: []byte("x")},
		{Key: "API_KEY", Value: []byte("y")},
	}, "seed", "tester"); err != nil {
		t.Fatal(err)
	}
	return ctx, c.ID
}

func TestMaxAge_EffectiveAndStaleness(t *testing.T) {
	s := newService(t)
	ctx, cid := seedMaxAgeConfig(t, s)

	// No policy → never stale, nil effective.
	metas, err := s.ListSecretsMerged(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.MaxAgeSeconds != nil || m.Stale {
			t.Fatalf("no-policy key %s: max=%v stale=%v", m.Key, m.MaxAgeSeconds, m.Stale)
		}
	}

	// Large config default (fresh keys not stale under it) + a tiny per-key
	// override on DATABASE_URL. A brief pause makes the 1s override deterministic
	// (age = now - created_at must exceed the override).
	if err := s.SetConfigMaxAge(ctx, cid, 1_000_000, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetKeyMaxAge(ctx, cid, "DATABASE_URL", 1, ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)

	metas, err = s.ListSecretsMerged(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]MergedMeta{}
	for _, m := range metas {
		byKey[m.Key] = m
	}
	db := byKey["DATABASE_URL"]
	if db.MaxAgeSeconds == nil || *db.MaxAgeSeconds != 1 {
		t.Fatalf("DATABASE_URL effective = %v, want per-key override 1", db.MaxAgeSeconds)
	}
	if !db.Stale {
		t.Fatalf("DATABASE_URL should be stale (age > 1s override)")
	}
	api := byKey["API_KEY"]
	if api.MaxAgeSeconds == nil || *api.MaxAgeSeconds != 1_000_000 {
		t.Fatalf("API_KEY effective = %v, want config default 1000000", api.MaxAgeSeconds)
	}
	if api.Stale {
		t.Fatalf("API_KEY should not be stale under a 1000000s default")
	}

	// CountStaleKeys reflects exactly the DATABASE_URL override.
	n, err := s.CountStaleKeys(ctx, cid)
	if err != nil || n != 1 {
		t.Fatalf("CountStaleKeys = %d, %v (want 1)", n, err)
	}

	// Clearing the per-key override falls back to the (large) default → not stale.
	if err := s.ClearKeyMaxAge(ctx, cid, "DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountStaleKeys(ctx, cid); n != 0 {
		t.Fatalf("after clearing override CountStaleKeys = %d, want 0", n)
	}

	// ListMaxAge shows the default under the sentinel key.
	pols, err := s.ListMaxAge(ctx, cid)
	if err != nil || len(pols) != 1 || pols[0].Key != "" || pols[0].MaxAgeSeconds != 1_000_000 {
		t.Fatalf("ListMaxAge = %v, %v", pols, err)
	}
}

func TestMaxAge_Validation(t *testing.T) {
	s := newService(t)
	ctx, cid := seedMaxAgeConfig(t, s)

	if err := s.SetConfigMaxAge(ctx, cid, 0, ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("zero seconds err = %v, want ErrValidation", err)
	}
	if err := s.SetKeyMaxAge(ctx, cid, "bad/key", 10, ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad key err = %v, want ErrValidation", err)
	}
	if err := s.SetKeyMaxAge(ctx, "00000000-0000-0000-0000-000000000000", "K", 10, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing config err = %v, want ErrNotFound", err)
	}
}
