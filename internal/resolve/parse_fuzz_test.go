package resolve

import (
	"strings"
	"testing"
)

// FuzzParseSegments feeds arbitrary secret VALUES to the reference parser.
// This is the most attacker-reachable parser in the codebase: any user who can
// write a secret controls this input, and a parsed reference's tokens are then
// used to look up another project/environment/config. The invariants below are
// what stop a crafted value from panicking the resolver or smuggling odd tokens
// into those lookups.
func FuzzParseSegments(f *testing.F) {
	for _, seed := range []string{
		"plain", "", "$", "$$", "${KEY}", "${projects.p.e.c.KEY}",
		"pre ${KEY} post", "${", "${}", "${.}", "${..}", "${a.b}",
		"${projects.p.e.c}", "${projects.p.e.c.K.EXTRA}",
		"${projects..e.c.K}", "${projects.p.e.c.}", "$${KEY}",
		"${KEY", "}${", "${${}}", "${a/b}", "${a b}", "${A_1-2}",
		strings.Repeat("$", 64), strings.Repeat("${K}", 32),
		"${projects." + strings.Repeat("p", 300) + ".e.c.K}",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		segs, err := parseSegments(value)
		if err != nil {
			if segs != nil {
				t.Fatalf("parseSegments(%q) returned err AND segments", value)
			}
			return
		}

		totalLiteral := 0
		for i, s := range segs {
			// A segment is literal XOR reference — never both, never neither.
			if s.ref != nil && s.literal != "" {
				t.Fatalf("segment %d of %q is both literal (%q) and reference", i, value, s.literal)
			}
			if s.ref == nil {
				if s.literal == "" {
					t.Fatalf("segment %d of %q is empty (neither literal nor reference)", i, value)
				}
				totalLiteral += len(s.literal)
				continue
			}

			r := s.ref
			// Every accepted reference must carry a usable key.
			if r.key == "" {
				t.Fatalf("accepted a reference with an empty key from %q", value)
			}
			if !validSegmentToken(r.key) {
				t.Fatalf("accepted reference key %q (invalid token) from %q", r.key, value)
			}
			if r.local {
				// A local ref must not carry coordinates.
				if r.coord.Project != "" || r.coord.Env != "" || r.coord.Config != "" {
					t.Fatalf("local ref from %q carries coordinates: %+v", value, r.coord)
				}
				continue
			}
			// An absolute ref's coordinates feed store lookups: each must be a
			// valid token, so a crafted value cannot smuggle separators or
			// wildcards into a project/env/config resolution.
			for name, tok := range map[string]string{
				"project": r.coord.Project, "env": r.coord.Env, "config": r.coord.Config,
			} {
				if !validSegmentToken(tok) {
					t.Fatalf("accepted %s token %q (invalid) from %q", name, tok, value)
				}
				if strings.ContainsAny(tok, "./\\${}") {
					t.Fatalf("accepted %s token %q containing a structural char, from %q", name, tok, value)
				}
			}
		}

		// Literals are only ever slices of the input (or a single "$" from "$$"),
		// so parsing can never amplify memory beyond the input size.
		if totalLiteral > len(value) {
			t.Fatalf("literal bytes (%d) exceed input length (%d) for %q", totalLiteral, len(value), value)
		}
	})
}
