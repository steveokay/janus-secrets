package auth

import (
	"errors"
	"reflect"
	"testing"
)

func TestResolveGroupClaim(t *testing.T) {
	tests := []struct {
		name    string
		raw     map[string]any
		path    string
		want    []string
		known   bool
		wantErr bool
	}{
		{
			name:  "array of strings is the common shape",
			raw:   map[string]any{"groups": []any{"payments", "platform"}},
			path:  "groups",
			want:  []string{"payments", "platform"},
			known: true,
		},
		{
			name:  "a lone string is one group, never split",
			raw:   map[string]any{"groups": "team a,team b"},
			path:  "groups",
			want:  []string{"team a,team b"},
			known: true,
		},
		{
			name:  "absent claim means the user is in no groups",
			raw:   map[string]any{"sub": "u1"},
			path:  "groups",
			want:  nil,
			known: true,
		},
		{
			name:  "explicit null is treated as absent, not as a bad shape",
			raw:   map[string]any{"groups": nil},
			path:  "groups",
			want:  nil,
			known: true,
		},
		{
			name:  "empty array clears membership",
			raw:   map[string]any{"groups": []any{}},
			path:  "groups",
			want:  []string{},
			known: true,
		},
		{
			// The Entra overage case. Membership is UNKNOWN, so the caller must
			// keep the existing snapshot rather than clear every group.
			name:  "overage marker means unknown, not empty",
			raw:   map[string]any{"_claim_names": map[string]any{"groups": "src1"}},
			path:  "groups",
			want:  nil,
			known: false,
		},
		{
			name:  "overage marker naming a different claim does not apply",
			raw:   map[string]any{"_claim_names": map[string]any{"roles": "src1"}},
			path:  "groups",
			want:  nil,
			known: true,
		},
		{
			name:  "empty values are skipped",
			raw:   map[string]any{"groups": []any{"payments", "", "platform"}},
			path:  "groups",
			want:  []string{"payments", "platform"},
			known: true,
		},
		{
			name:  "duplicates collapse",
			raw:   map[string]any{"groups": []any{"payments", "payments"}},
			path:  "groups",
			want:  []string{"payments"},
			known: true,
		},
		{
			name:  "dotted path walks nested objects",
			raw:   map[string]any{"realm": map[string]any{"access": []any{"payments"}}},
			path:  "realm.access",
			want:  []string{"payments"},
			known: true,
		},
		{
			name:  "deep dotted path",
			raw:   map[string]any{"a": map[string]any{"b": map[string]any{"c": []any{"g"}}}},
			path:  "a.b.c",
			want:  []string{"g"},
			known: true,
		},
		{
			// Same fail-closed rule CI federation applies: a path two different
			// constructions could produce is rejected, never resolved by
			// precedence.
			name:    "ambiguous path is rejected",
			raw:     map[string]any{"a.b": []any{"x"}, "a": map[string]any{"b": []any{"y"}}},
			path:    "a.b",
			wantErr: true,
		},
		{
			name:    "non-string element is an error, not a skipped entry",
			raw:     map[string]any{"groups": []any{"payments", 42}},
			path:    "groups",
			wantErr: true,
		},
		{
			name:    "nested object element is an error",
			raw:     map[string]any{"groups": []any{map[string]any{"id": "x"}}},
			path:    "groups",
			wantErr: true,
		},
		{
			name:    "a number claim is an error",
			raw:     map[string]any{"groups": 7},
			path:    "groups",
			wantErr: true,
		},
		{
			name:    "an object claim is an error",
			raw:     map[string]any{"groups": map[string]any{"a": "b"}},
			path:    "groups",
			wantErr: true,
		},
		{
			name:  "empty path disables sync",
			raw:   map[string]any{"groups": []any{"payments"}},
			path:  "",
			known: false,
		},
		{
			name:  "a literal dotted claim name resolves when nothing nests to it",
			raw:   map[string]any{"a.b": []any{"x"}},
			path:  "a.b",
			want:  []string{"x"},
			known: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, known, err := resolveGroupClaim(tc.raw, tc.path)
			if tc.wantErr {
				if !errors.Is(err, ErrOIDCGroupClaim) {
					t.Fatalf("err = %v, want ErrOIDCGroupClaim", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if known != tc.known {
				t.Fatalf("known = %v, want %v", known, tc.known)
			}
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("values = %v, want %v", got, tc.want)
			}
		})
	}
}

// A hostile issuer must not be able to make one login write an unbounded
// membership set; rejecting beats truncating, which would look like a
// legitimate removal from the dropped groups.
func TestResolveGroupClaimBoundsCount(t *testing.T) {
	vals := make([]any, maxGroupClaimValues+1)
	for i := range vals {
		vals[i] = "g"
	}
	if _, _, err := resolveGroupClaim(map[string]any{"groups": vals}, "groups"); !errors.Is(err, ErrOIDCGroupClaim) {
		t.Fatalf("err = %v, want ErrOIDCGroupClaim", err)
	}
}
