package promote

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// TestPreviewClassifiesAndApplyCreatesVersion is the end-to-end happy path:
// classify a diff, then apply a subset as one new target version.
func TestPreviewClassifiesAndApplyCreatesVersion(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.setSecrets(t, h.devCfg, map[string]string{"A": "1", "B": "dev", "C": "new"})
	h.setSecrets(t, h.stgCfg, map[string]string{"A": "1", "B": "stg", "LEGACY": "on"})

	diff, err := h.svc.Preview(ctx, h.devCfg, h.stgCfg, h.actor)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	got := map[string]DiffEntry{}
	for _, e := range diff.Entries {
		got[e.Key] = e
	}
	if got["A"].Status != StatusSame {
		t.Errorf("A status = %q, want same", got["A"].Status)
	}
	if got["B"].Status != StatusChange {
		t.Errorf("B status = %q, want change", got["B"].Status)
	}
	if got["B"].SourceValue != "dev" || got["B"].TargetValue != "stg" {
		t.Errorf("B src/tgt = %q/%q, want dev/stg", got["B"].SourceValue, got["B"].TargetValue)
	}
	if got["C"].Status != StatusAdd {
		t.Errorf("C status = %q, want add", got["C"].Status)
	}
	if got["LEGACY"].Status != StatusRemove {
		t.Errorf("LEGACY status = %q, want remove", got["LEGACY"].Status)
	}

	res, err := h.svc.Apply(ctx, ApplyRequest{
		SourceConfigID: h.devCfg,
		TargetConfigID: h.stgCfg,
		SourceVersion:  diff.SourceVersion,
		Selections: []Selection{
			{Key: "B", Action: ActionSet},
			{Key: "C", Action: ActionSet},
		},
		Actor: h.actor,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Skipped) != 0 {
		t.Errorf("Skipped = %v, want none", res.Skipped)
	}

	final := h.reveal(t, h.stgCfg)
	if final["A"] != "1" {
		t.Errorf("A = %q, want 1 (untouched)", final["A"])
	}
	if final["B"] != "dev" {
		t.Errorf("B = %q, want dev (promoted)", final["B"])
	}
	if final["C"] != "new" {
		t.Errorf("C = %q, want new (promoted)", final["C"])
	}
	if final["LEGACY"] != "on" {
		t.Errorf("LEGACY = %q, want on (not selected)", final["LEGACY"])
	}
}

// TestIllegalStepRejected rejects both Preview and Apply when the target is not
// the pipeline's next hop from the source.
func TestIllegalStepRejected(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.setSecrets(t, h.devCfg, map[string]string{"A": "1"})

	if _, err := h.svc.Preview(ctx, h.devCfg, h.prodCfg, h.actor); !errors.Is(err, ErrIllegalStep) {
		t.Fatalf("Preview dev→prod err = %v, want ErrIllegalStep", err)
	}
	_, err := h.svc.Apply(ctx, ApplyRequest{
		SourceConfigID: h.devCfg,
		TargetConfigID: h.prodCfg,
		SourceVersion:  1,
		Selections:     []Selection{{Key: "A", Action: ActionSet}},
		Actor:          h.actor,
	})
	if !errors.Is(err, ErrIllegalStep) {
		t.Fatalf("Apply dev→prod err = %v, want ErrIllegalStep", err)
	}
}

// TestLockedKeyRejected refuses to promote a key locked on the target, naming
// only the key, and leaves the target value unchanged.
func TestLockedKeyRejected(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.setSecrets(t, h.devCfg, map[string]string{"B": "dev"})
	h.setSecrets(t, h.stgCfg, map[string]string{"B": "stg"})

	if err := store.NewLockedKeyRepo(testStore).Lock(ctx, h.stgCfg, "B", ""); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	_, err := h.svc.Apply(ctx, ApplyRequest{
		SourceConfigID: h.devCfg,
		TargetConfigID: h.stgCfg,
		SourceVersion:  1,
		Selections:     []Selection{{Key: "B", Action: ActionSet}},
		Actor:          h.actor,
	})
	if !errors.Is(err, ErrLockedKey) {
		t.Fatalf("Apply err = %v, want ErrLockedKey", err)
	}
	if !strings.Contains(err.Error(), "B") {
		t.Errorf("error %q should name key B", err.Error())
	}
	if v := h.reveal(t, h.stgCfg)["B"]; v != "stg" {
		t.Errorf("B = %q, want stg (unchanged)", v)
	}
}

// TestRemove promotes a delete, dropping the key from the target.
func TestRemove(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.setSecrets(t, h.devCfg, map[string]string{"A": "1"})
	h.setSecrets(t, h.stgCfg, map[string]string{"A": "1", "LEGACY": "on"})

	diff, err := h.svc.Preview(ctx, h.devCfg, h.stgCfg, h.actor)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	res, err := h.svc.Apply(ctx, ApplyRequest{
		SourceConfigID: h.devCfg,
		TargetConfigID: h.stgCfg,
		SourceVersion:  diff.SourceVersion,
		Selections:     []Selection{{Key: "LEGACY", Action: ActionRemove}},
		Actor:          h.actor,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Applied) != 1 {
		t.Errorf("Applied = %v, want 1", res.Applied)
	}
	final := h.reveal(t, h.stgCfg)
	if _, ok := final["LEGACY"]; ok {
		t.Errorf("LEGACY still present after remove: %v", final)
	}
	if final["A"] != "1" {
		t.Errorf("A = %q, want 1 (untouched)", final["A"])
	}
}

// TestDrift skips a selected key that is absent from the pinned source version.
// We delete C from dev, re-Preview to pin the post-delete source version, then
// select C:set — the pinned reveal no longer holds C, so it is skipped.
func TestDrift(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.setSecrets(t, h.devCfg, map[string]string{"A": "1", "C": "temp"})
	h.setSecrets(t, h.stgCfg, map[string]string{"A": "1"})

	// Delete C from dev.
	if _, err := h.sec.SetSecrets(ctx, h.devCfg,
		[]secrets.SecretChange{{Key: "C", Delete: true}}, "drop C", h.actor); err != nil {
		t.Fatalf("delete C: %v", err)
	}

	// Re-Preview to pin the current (post-delete) source version.
	diff, err := h.svc.Preview(ctx, h.devCfg, h.stgCfg, h.actor)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	res, err := h.svc.Apply(ctx, ApplyRequest{
		SourceConfigID: h.devCfg,
		TargetConfigID: h.stgCfg,
		SourceVersion:  diff.SourceVersion,
		Selections:     []Selection{{Key: "C", Action: ActionSet}},
		Actor:          h.actor,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	found := false
	for _, k := range res.Skipped {
		if k == "C" {
			found = true
		}
	}
	if !found {
		t.Errorf("Skipped = %v, want to contain C", res.Skipped)
	}
	if len(res.Applied) != 0 {
		t.Errorf("Applied = %v, want none", res.Applied)
	}
	if _, ok := h.reveal(t, h.stgCfg)["C"]; ok {
		t.Errorf("C should not have been promoted")
	}
}

// TestCreateTarget promotes into a target config that does not yet exist,
// creating it as part of Apply.
func TestCreateTarget(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Fresh project with dev → staging pipeline; staging has NO config.
	slug := "promo-create"
	p, err := h.sec.CreateProject(ctx, uniqueSlug(slug), "Create Target")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	dev, err := h.sec.CreateEnvironment(ctx, p.ID, "dev", "Development")
	if err != nil {
		t.Fatalf("CreateEnvironment dev: %v", err)
	}
	stg, err := h.sec.CreateEnvironment(ctx, p.ID, "staging", "Staging")
	if err != nil {
		t.Fatalf("CreateEnvironment staging: %v", err)
	}
	devCfg, err := h.sec.CreateConfig(ctx, dev.ID, "default", nil)
	if err != nil {
		t.Fatalf("CreateConfig dev: %v", err)
	}
	if err := store.NewPipelineRepo(testStore).Set(ctx, p.ID, []string{dev.ID, stg.ID}); err != nil {
		t.Fatalf("pipeline Set: %v", err)
	}

	if _, err := h.sec.SetSecrets(ctx, devCfg.ID,
		[]secrets.SecretChange{{Key: "K", Value: []byte("v")}}, "seed", h.actor); err != nil {
		t.Fatalf("seed dev: %v", err)
	}

	res, err := h.svc.Apply(ctx, ApplyRequest{
		SourceConfigID: devCfg.ID,
		CreateTarget:   true,
		TargetEnvID:    stg.ID,
		TargetName:     "default",
		SourceVersion:  1,
		Selections:     []Selection{{Key: "K", Action: ActionSet}},
		Actor:          h.actor,
	})
	if err != nil {
		t.Fatalf("Apply create: %v", err)
	}
	if res.TargetVersion != 1 {
		t.Errorf("TargetVersion = %d, want 1", res.TargetVersion)
	}

	// The staging config now exists with the promoted key.
	newCfg, err := store.NewConfigRepo(testStore).GetByName(ctx, stg.ID, "default")
	if err != nil {
		t.Fatalf("GetByName staging default: %v", err)
	}
	_, vals, err := h.sec.RevealConfig(ctx, newCfg.ID)
	if err != nil {
		t.Fatalf("reveal new staging: %v", err)
	}
	if string(vals["K"].Value) != "v" {
		t.Errorf("K = %q, want v", string(vals["K"].Value))
	}
}

// TestReferencesCopiedRaw proves promotion copies the literal reference string
// without resolving it (re-encrypt of the raw stored value, no resolution).
func TestReferencesCopiedRaw(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const ref = "${projects.x.dev.K}"
	h.setSecrets(t, h.devCfg, map[string]string{"REF": ref})

	diff, err := h.svc.Preview(ctx, h.devCfg, h.stgCfg, h.actor)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if _, err := h.svc.Apply(ctx, ApplyRequest{
		SourceConfigID: h.devCfg,
		TargetConfigID: h.stgCfg,
		SourceVersion:  diff.SourceVersion,
		Selections:     []Selection{{Key: "REF", Action: ActionSet}},
		Actor:          h.actor,
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if v := h.reveal(t, h.stgCfg)["REF"]; v != ref {
		t.Errorf("REF = %q, want literal %q", v, ref)
	}
}

func uniqueSlug(base string) string {
	return base + "-" + itoa(slugSeq.Add(1))
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
