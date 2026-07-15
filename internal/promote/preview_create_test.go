package promote

import (
	"context"
	"errors"
	"testing"
)

// TestPreviewCreate covers the create-target preview: promoting a source config
// into a target ENV that has no config yet. Every source key must come back as
// an "add" with its source value, TargetExists false, and SourceVersion pinned
// to the source's latest. A non-adjacent target env is an illegal step.
func TestPreviewCreate(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Seed dev; staging holds NO config version (create-target scenario).
	h.setSecrets(t, h.devCfg, map[string]string{"A": "aval", "B": "bval"})

	diff, err := h.svc.PreviewCreate(ctx, h.devCfg, h.stgEnv, h.actor)
	if err != nil {
		t.Fatalf("PreviewCreate dev->staging: %v", err)
	}
	if diff.TargetExists {
		t.Fatalf("PreviewCreate: want TargetExists=false, got true")
	}
	if diff.SourceVersion != 1 {
		t.Fatalf("PreviewCreate: want SourceVersion=1, got %d", diff.SourceVersion)
	}
	got := map[string]string{}
	for _, e := range diff.Entries {
		if e.Status != StatusAdd {
			t.Fatalf("PreviewCreate: key %q want status add, got %q", e.Key, e.Status)
		}
		if e.Locked {
			t.Fatalf("PreviewCreate: key %q want Locked=false", e.Key)
		}
		if e.TargetValue != "" {
			t.Fatalf("PreviewCreate: key %q want empty TargetValue, got %q", e.Key, e.TargetValue)
		}
		got[e.Key] = e.SourceValue
	}
	if got["A"] != "aval" || got["B"] != "bval" {
		t.Fatalf("PreviewCreate: want A=aval,B=bval, got %+v", got)
	}
	if len(got) != 2 {
		t.Fatalf("PreviewCreate: want 2 entries, got %d (%+v)", len(got), got)
	}

	// Non-adjacent target env (dev -> prod) is not the next pipeline step.
	if _, err := h.svc.PreviewCreate(ctx, h.devCfg, h.prodEnv, h.actor); !errors.Is(err, ErrIllegalStep) {
		t.Fatalf("PreviewCreate dev->prod: want ErrIllegalStep, got %v", err)
	}
}
