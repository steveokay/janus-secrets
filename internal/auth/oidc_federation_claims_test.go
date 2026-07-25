package auth

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// TestFlattenClaims pins the projection federated bindings match on: nested
// objects become dotted paths (Kubernetes service-account identity lives in a
// nested "kubernetes.io" object), non-string scalars are still dropped rather
// than coerced, and any dotted-path collision rejects the whole claim set.
func TestFlattenClaims(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    map[string]string
		wantErr error
	}{
		{
			name: "kubernetes service account identity flattens",
			payload: `{"iss":"https://k8s","sub":"system:serviceaccount:prod:api","aud":["janus"],
			           "kubernetes.io":{"namespace":"prod","pod":{"name":"api-7c9","uid":"pod-uid"},
			                            "serviceaccount":{"name":"api","uid":"sa-uid"}},
			           "exp":1700003600,"iat":1700000000}`,
			want: map[string]string{
				"iss": "https://k8s", "sub": "system:serviceaccount:prod:api", "aud": "janus",
				"kubernetes.io.namespace":           "prod",
				"kubernetes.io.pod.name":            "api-7c9",
				"kubernetes.io.pod.uid":             "pod-uid",
				"kubernetes.io.serviceaccount.name": "api",
				"kubernetes.io.serviceaccount.uid":  "sa-uid",
			},
		},
		{
			name:    "literal dotted key survives (CircleCI style)",
			payload: `{"oidc.circleci.com/project-id":"proj-uuid","sub":"x"}`,
			want:    map[string]string{"oidc.circleci.com/project-id": "proj-uuid", "sub": "x"},
		},
		{
			name:    "non-string scalars still dropped, never coerced",
			payload: `{"repository":"acme/app","repository_id":42,"private":true,"arr":["a"],"nil":null}`,
			want:    map[string]string{"repository": "acme/app"},
		},
		{
			name:    "multi-valued aud is dropped (which element would a binding mean?)",
			payload: `{"aud":["janus","other"],"sub":"x"}`,
			want:    map[string]string{"sub": "x"},
		},
		{
			name:    "single-string aud kept as-is",
			payload: `{"aud":"janus","sub":"x"}`,
			want:    map[string]string{"aud": "janus", "sub": "x"},
		},
		{
			name:    "one-element non-string aud dropped",
			payload: `{"aud":[42],"sub":"x"}`,
			want:    map[string]string{"sub": "x"},
		},
		{
			// {"a.b":"x"} vs {"a":{"b":"y"}} — the ambiguity rule.
			name:    "literal dotted key colliding with a nested path is rejected",
			payload: `{"a.b":"x","a":{"b":"y"}}`,
			wantErr: ErrFederationClaims,
		},
		{
			name:    "collision with a dropped non-string literal key is still rejected",
			payload: `{"a.b":42,"a":{"b":"y"}}`,
			wantErr: ErrFederationClaims,
		},
		{
			name:    "two nested branches meeting on one path are rejected",
			payload: `{"a":{"b.c":"x"},"a.b":{"c":"y"}}`,
			wantErr: ErrFederationClaims,
		},
		{
			name:    "nested object shadowing a literal key is rejected early",
			payload: `{"a":{"b":{"c":"x"}},"a.b":{"c":"y"}}`,
			wantErr: ErrFederationClaims,
		},
		{
			name:    "distinct dotted paths do not collide",
			payload: `{"a":{"b":"x","b.c":"y"}}`,
			want:    map[string]string{"a.b": "x", "a.b.c": "y"},
		},
		{
			name:    "over-deep nesting is rejected",
			payload: `{"a":{"b":{"c":{"d":{"e":{"f":{"g":{"h":"deep"}}}}}}}}`,
			wantErr: ErrFederationClaims,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var raw map[string]any
			if err := json.Unmarshal([]byte(tc.payload), &raw); err != nil {
				t.Fatalf("bad test payload: %v", err)
			}
			got, err := flattenClaims(raw)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("flattenClaims: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("flatten mismatch:\n got %#v\nwant %#v", got, tc.want)
			}
		})
	}
}

// TestUnverifiedIssuer covers the routing hint used to pick a verifier. It must
// never panic or accept a malformed token; the value it returns is only a hint
// (the chosen verifier re-checks `iss` against its own configured issuer).
func TestUnverifiedIssuer(t *testing.T) {
	// {"iss":"https://issuer.example"} base64url, unpadded.
	good := "aGVhZGVy.eyJpc3MiOiJodHRwczovL2lzc3Vlci5leGFtcGxlIn0.c2ln"
	tests := []struct {
		name, raw, want string
		wantErr         bool
	}{
		{name: "well formed", raw: good, want: "https://issuer.example"},
		{name: "not three parts", raw: "a.b", wantErr: true},
		{name: "empty payload", raw: "a..c", wantErr: true},
		{name: "payload not base64", raw: "a.!!!.c", wantErr: true},
		{name: "payload not json", raw: "a.bm90LWpzb24.c", wantErr: true},
		{name: "no iss claim", raw: "a.e30.c", wantErr: true}, // {}
		{name: "blank iss", raw: "a.eyJpc3MiOiIgIn0.c", wantErr: true},
		{name: "empty string", raw: "", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := unverifiedIssuer(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
