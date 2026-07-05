package secrets

import (
	"context"
	"fmt"
	"testing"

	"github.com/steveokay/janus-secrets/internal/resolve"
)

func TestReadRawByCoordAndID(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	slug := fmt.Sprintf("raw-%d", slugSeq.Add(1))
	p, err := s.CreateProject(ctx, slug, "Raw Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	e, err := s.CreateEnvironment(ctx, p.ID, "prod", "Production")
	if err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	c, err := s.CreateConfig(ctx, e.ID, "web", nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	if _, err := s.SetSecrets(ctx, c.ID, []SecretChange{{Key: "K", Value: []byte("v")}}, "seed", "tester"); err != nil {
		t.Fatalf("SetSecrets: %v", err)
	}

	rc, err := s.ReadRaw(ctx, resolve.Coord{Project: slug, Env: "prod", Config: "web"})
	if err != nil {
		t.Fatalf("ReadRaw: %v", err)
	}
	if got := string(rc.Values["K"]); got != "v" {
		t.Errorf("ReadRaw value = %q, want %q", got, "v")
	}
	if rc.Config != "web" {
		t.Errorf("ReadRaw Config = %q, want %q", rc.Config, "web")
	}
	if rc.ConfigID == "" {
		t.Error("ReadRaw ConfigID is empty")
	}
	if rc.Project != slug {
		t.Errorf("ReadRaw Project = %q, want %q", rc.Project, slug)
	}

	byID, err := s.ReadRawByID(ctx, rc.ConfigID)
	if err != nil {
		t.Fatalf("ReadRawByID: %v", err)
	}
	if got := string(byID.Values["K"]); got != "v" {
		t.Errorf("ReadRawByID value = %q, want %q", got, "v")
	}

	if _, err := s.ReadRaw(ctx, resolve.Coord{Project: "nope-does-not-exist", Env: "prod", Config: "web"}); err == nil {
		t.Error("ReadRaw with missing project: expected error, got nil")
	}
}
