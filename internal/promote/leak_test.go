package promote

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// canary is a distinctive secret VALUE plaintext. Promotion copies a source
// config's secret values into a target config; it must never LOG a value, and
// its returned ApplyResult must carry key names only (never values). If this
// string ever surfaces in captured log output or the marshaled ApplyResult, a
// value plaintext leaked — a security bug, not a test bug.
const canary = "SENTINEL-PROMOTE-7b2c9e1f"

// TestPromoteDoesNotLeakSecretValue is the value-free proof for environment
// promotion. It seeds a sentinel value in the dev config, captures all log
// output for the duration, previews then applies a promotion of that key to
// staging, and asserts:
//   - the promotion actually succeeded (staging CANARY == canary), so the test
//     is non-vacuous;
//   - the sentinel never appears in any captured log output;
//   - the marshaled ApplyResult (applied/skipped are key names only) never
//     contains the sentinel value.
//
// The engine performs no logging of its own; this test locks in that property
// plus the value-free ApplyResult contract so a future logging addition (or a
// change that put values into ApplyResult) can't silently regress it. The Diff
// intentionally carries values for the caller, so it is deliberately NOT
// asserted value-free here.
func TestPromoteDoesNotLeakSecretValue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// 1. Seed the sentinel value in the dev (source) config.
	h.setSecrets(t, h.devCfg, map[string]string{"CANARY": canary, "OTHER": "x"})

	// 2. Capture all log output for the duration. The promote Service takes no
	//    logger, so we capture the process-wide slog default, which covers
	//    anything store/secrets/crypto might emit during preview/apply. Restore
	//    the previous default on cleanup.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// 3. Preview then Apply, promoting only CANARY from dev → staging.
	diff, err := h.svc.Preview(ctx, h.devCfg, h.stgCfg, h.actor)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	res, err := h.svc.Apply(ctx, ApplyRequest{
		SourceConfigID: h.devCfg,
		TargetConfigID: h.stgCfg,
		SourceVersion:  diff.SourceVersion,
		Selections:     []Selection{{Key: "CANARY", Action: ActionSet}},
		Actor:          h.actor,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// 4. Non-vacuous: the promotion actually landed the sentinel in staging.
	if got := h.reveal(t, h.stgCfg); got["CANARY"] != canary {
		t.Fatalf("staging CANARY = %q, want sentinel (promotion must have succeeded)", got["CANARY"])
	}

	// 5. The sentinel plaintext must NEVER appear in captured log output.
	if strings.Contains(buf.String(), canary) {
		t.Fatalf("secret value leaked into logs:\n%s", buf.String())
	}

	// 6. The marshaled ApplyResult (applied/skipped are key names only) must
	//    NEVER contain the sentinel value.
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal ApplyResult: %v", err)
	}
	if strings.Contains(string(b), canary) {
		t.Fatalf("secret value leaked into ApplyResult JSON: %s", string(b))
	}
}
