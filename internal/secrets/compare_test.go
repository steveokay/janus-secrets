package secrets

import (
	"context"
	"fmt"
	"testing"
)

// mkConfig creates a fresh config under a new env in project p and returns its id.
func mkConfig(t *testing.T, s *Service, projectID, envName, cfgName string) string {
	t.Helper()
	ctx := context.Background()
	e, err := s.CreateEnvironment(ctx, projectID, envName, envName)
	if err != nil {
		t.Fatalf("CreateEnvironment(%s): %v", envName, err)
	}
	c, err := s.CreateConfig(ctx, e.ID, cfgName, nil)
	if err != nil {
		t.Fatalf("CreateConfig(%s): %v", cfgName, err)
	}
	return c.ID
}

func TestCompareConfigs(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	slug := fmt.Sprintf("cmp-%d", slugSeq.Add(1))
	p, err := s.CreateProject(ctx, slug, "Compare Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	a := mkConfig(t, s, p.ID, "staging", "root")
	b := mkConfig(t, s, p.ID, "prod", "root")

	// A: SHARED=x, SAME=eq, ONLY_A=1
	if _, err := s.SetSecrets(ctx, a, []SecretChange{
		{Key: "SHARED", Value: []byte("x")},
		{Key: "SAME", Value: []byte("eq")},
		{Key: "ONLY_A", Value: []byte("1")},
	}, "seed a", "t"); err != nil {
		t.Fatalf("SetSecrets a: %v", err)
	}
	// B: SHARED=y (differs), SAME=eq, ONLY_B=2
	if _, err := s.SetSecrets(ctx, b, []SecretChange{
		{Key: "SHARED", Value: []byte("y")},
		{Key: "SAME", Value: []byte("eq")},
		{Key: "ONLY_B", Value: []byte("2")},
	}, "seed b", "t"); err != nil {
		t.Fatalf("SetSecrets b: %v", err)
	}

	rows, err := s.CompareConfigs(ctx, a, b)
	if err != nil {
		t.Fatalf("CompareConfigs: %v", err)
	}
	got := map[string]CompareRow{}
	for _, r := range rows {
		got[r.Key] = r
	}

	tests := []struct {
		key                  string
		inA, inB, differs    bool
	}{
		{"SHARED", true, true, true},
		{"SAME", true, true, false},
		{"ONLY_A", true, false, false},
		{"ONLY_B", false, true, false},
	}
	if len(rows) != len(tests) {
		t.Fatalf("row count = %d, want %d (%v)", len(rows), len(tests), got)
	}
	for _, tc := range tests {
		r, ok := got[tc.key]
		if !ok {
			t.Fatalf("missing key %q", tc.key)
		}
		if r.InA != tc.inA || r.InB != tc.inB || r.Differs != tc.differs {
			t.Errorf("%s: in_a=%v in_b=%v differs=%v, want in_a=%v in_b=%v differs=%v",
				tc.key, r.InA, r.InB, r.Differs, tc.inA, tc.inB, tc.differs)
		}
	}

	// Origins are populated for present sides only.
	if got["ONLY_A"].OriginA != "own" || got["ONLY_A"].OriginB != "" {
		t.Errorf("ONLY_A origin = (%q,%q), want (own,\"\")", got["ONLY_A"].OriginA, got["ONLY_A"].OriginB)
	}
}

// TestCompareConfigs_NoValueLeak asserts no secret plaintext appears anywhere in
// the returned rows — the compare is strictly value-free.
func TestCompareConfigs_NoValueLeak(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	slug := fmt.Sprintf("cmpleak-%d", slugSeq.Add(1))
	p, err := s.CreateProject(ctx, slug, "Leak Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	a := mkConfig(t, s, p.ID, "staging", "root")
	b := mkConfig(t, s, p.ID, "prod", "root")

	const secretA = "compare-value-alpha"
	const secretB = "compare-value-bravo"
	if _, err := s.SetSecrets(ctx, a, []SecretChange{{Key: "K", Value: []byte(secretA)}}, "a", "t"); err != nil {
		t.Fatalf("SetSecrets a: %v", err)
	}
	if _, err := s.SetSecrets(ctx, b, []SecretChange{{Key: "K", Value: []byte(secretB)}}, "b", "t"); err != nil {
		t.Fatalf("SetSecrets b: %v", err)
	}

	rows, err := s.CompareConfigs(ctx, a, b)
	if err != nil {
		t.Fatalf("CompareConfigs: %v", err)
	}
	blob := fmt.Sprintf("%#v", rows)
	for _, sv := range []string{secretA, secretB} {
		if containsStr(blob, sv) {
			t.Fatalf("secret value %q leaked into compare rows: %s", sv, blob)
		}
	}
	if len(rows) != 1 || !rows[0].Differs {
		t.Fatalf("expected 1 differing row, got %v", rows)
	}
}

func containsStr(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
