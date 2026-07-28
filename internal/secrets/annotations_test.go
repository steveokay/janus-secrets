package secrets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func seedAnnotationConfig(t *testing.T, s *Service) (context.Context, string) {
	t.Helper()
	ctx := context.Background()
	slug := fmt.Sprintf("annot-%d", slugSeq.Add(1))
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
	}, "seed", ""); err != nil {
		t.Fatal(err)
	}
	return ctx, c.ID
}

func sp(s string) *string { return &s }

func TestAnnotation_SetClearAndMerged(t *testing.T) {
	s := newService(t)
	ctx, cid := seedAnnotationConfig(t, s)

	// No annotation → nil note in the merged view.
	metas, err := s.ListSecretsMerged(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.Note != nil {
			t.Fatalf("unannotated key %s: note=%v", m.Key, m.Note)
		}
	}

	// Set a note (trimmed).
	outNote, cleared, err := s.SetAnnotation(ctx, cid, "DATABASE_URL", sp("  primary DB dsn  "), "")
	if err != nil || cleared {
		t.Fatalf("set note: cleared=%v err=%v", cleared, err)
	}
	if outNote == nil || *outNote != "primary DB dsn" {
		t.Fatalf("normalized set = %v", outNote)
	}
	if _, cleared, err := s.SetAnnotation(ctx, cid, "API_KEY", sp("third-party key"), ""); err != nil || cleared {
		t.Fatalf("set second note: cleared=%v err=%v", cleared, err)
	}

	metas, err = s.ListSecretsMerged(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]MergedMeta{}
	for _, m := range metas {
		byKey[m.Key] = m
	}
	db := byKey["DATABASE_URL"]
	if db.Note == nil || *db.Note != "primary DB dsn" {
		t.Fatalf("DATABASE_URL annotation = %v", db.Note)
	}
	api := byKey["API_KEY"]
	if api.Note == nil || *api.Note != "third-party key" {
		t.Fatalf("API_KEY annotation = %v", api.Note)
	}

	// ListAnnotations reflects both.
	anns, err := s.ListAnnotations(ctx, cid)
	if err != nil || len(anns) != 2 {
		t.Fatalf("ListAnnotations = %v, %v", anns, err)
	}

	// An empty note clears the annotation.
	_, cleared, err = s.SetAnnotation(ctx, cid, "DATABASE_URL", sp("   "), "")
	if err != nil || !cleared {
		t.Fatalf("empty set should clear: cleared=%v err=%v", cleared, err)
	}
	metas, _ = s.ListSecretsMerged(ctx, cid)
	for _, m := range metas {
		if m.Key == "DATABASE_URL" && m.Note != nil {
			t.Fatalf("DATABASE_URL should be cleared, got %v", m.Note)
		}
	}

	// Explicit ClearAnnotation is a no-op when already absent.
	if err := s.ClearAnnotation(ctx, cid, "DATABASE_URL"); err != nil {
		t.Fatalf("ClearAnnotation no-op: %v", err)
	}
}

func TestAnnotation_Validation(t *testing.T) {
	s := newService(t)
	ctx, cid := seedAnnotationConfig(t, s)

	// Bad key.
	if _, _, err := s.SetAnnotation(ctx, cid, "bad/key", sp("x"), ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad key err = %v, want ErrValidation", err)
	}
	// Over-length note.
	if _, _, err := s.SetAnnotation(ctx, cid, "API_KEY", sp(strings.Repeat("b", MaxAnnotationNoteLen+1)), ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("long note err = %v, want ErrValidation", err)
	}
	// Missing config.
	if _, _, err := s.SetAnnotation(ctx, "00000000-0000-0000-0000-000000000000", "K", sp("x"), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing config err = %v, want ErrNotFound", err)
	}
	// Over-length PROJECT owner — the field moved, so its bound lives here now.
	if _, err := s.SetProjectOwner(ctx, "00000000-0000-0000-0000-000000000000",
		sp(strings.Repeat("a", MaxProjectOwnerLen+1))); !errors.Is(err, ErrValidation) {
		t.Fatalf("long owner err = %v, want ErrValidation", err)
	}
}
