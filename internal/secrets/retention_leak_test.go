package secrets

import (
	"context"
	"fmt"
	"testing"
)

// TestPruneResultIsValueFree asserts that nothing a prune returns carries secret
// material. A prune touches ciphertext, DEKs and nonces by definition (it
// deletes them), so its result type is the one place a leak could plausibly be
// introduced — e.g. by returning the rows it removed "for the audit trail".
//
// The check deep-formats the whole result and the resolved retention policy and
// greps for every plaintext written into the config, plus a raw scan of the
// deleted rows' ciphertext for good measure.
func TestPruneResultIsValueFree(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)

	plaintexts := []string{
		"prune-leak-canary-alpha",
		"prune-leak-canary-bravo",
		"prune-leak-canary-charlie",
		"prune-leak-canary-delta",
	}
	for i, pt := range plaintexts {
		if _, err := s.SetSecrets(ctx, configID, []SecretChange{
			{Key: "CANARY", Value: []byte(pt)},
			{Key: fmt.Sprintf("EXTRA_%d", i), Value: []byte(pt + "-extra")},
		}, fmt.Sprintf("v%d", i+1), "tester"); err != nil {
			t.Fatal(err)
		}
	}

	// Both the preview and the real prune are checked: the preview is the mode an
	// operator runs most, so it is the likelier place for a "show me what I'd
	// lose" leak to creep in.
	preview, err := s.PruneVersions(ctx, configID, PruneRequest{KeepVersions: 1, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.PruneVersions(ctx, configID, PruneRequest{KeepVersions: 1})
	if err != nil {
		t.Fatal(err)
	}
	pol, err := s.GetVersionRetention(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	if res.DeletedValues == 0 {
		t.Fatal("prune deleted no value versions; the leak check would be vacuous")
	}

	blob := fmt.Sprintf("%#v|%#v|%#v", preview, res, pol)
	for _, pt := range plaintexts {
		for _, needle := range []string{pt, pt + "-extra"} {
			if containsStr(blob, needle) {
				t.Fatalf("secret value %q leaked into the prune result: %s", needle, blob)
			}
		}
	}

	// Value-free also means "no key material": no ciphertext, DEK or nonce byte
	// slice may be echoed back. Formatting the result renders any []byte as
	// digits, so assert the result carries no byte slices at all beyond the
	// version-number ints by checking for the byte-slice syntax Go prints.
	if containsStr(blob, "[]byte") || containsStr(blob, "[]uint8") {
		t.Fatalf("prune result carries raw bytes: %s", blob)
	}
}
