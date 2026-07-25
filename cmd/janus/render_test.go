package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRenderTemplate(t *testing.T) {
	secrets := map[string]string{
		"DB_URL":  "postgres://localhost/app",
		"API_KEY": "sk-123",
	}
	cases := []struct {
		name    string
		src     string
		want    string
		wantErr string // substring; "" means success
	}{
		{
			name: "top_level_map",
			src:  "url={{ .DB_URL }}",
			want: "url=postgres://localhost/app",
		},
		{
			name: "index_form",
			src:  `key={{ index . "API_KEY" }}`,
			want: "key=sk-123",
		},
		{
			name: "secret_func",
			src:  `k={{ secret "API_KEY" }}`,
			want: "k=sk-123",
		},
		{
			name: "both_forms_combined",
			src:  `{{ .DB_URL }}|{{ secret "API_KEY" }}`,
			want: "postgres://localhost/app|sk-123",
		},
		{
			name:    "missing_key_dot_errors",
			src:     "{{ .NOPE }}",
			wantErr: "NOPE",
		},
		{
			name:    "missing_key_func_errors",
			src:     `{{ secret "NOPE" }}`,
			wantErr: "not found",
		},
		{
			name:    "parse_error",
			src:     "{{ .DB_URL ",
			wantErr: "parse template",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := renderTemplate("t", tc.src, secrets)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want err containing %q, got %v (out=%q)", tc.wantErr, err, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if string(out) != tc.want {
				t.Fatalf("render = %q, want %q", out, tc.want)
			}
		})
	}
}

// TestRenderTemplateNoValueLeakOnMissing ensures a missing-key error does not
// embed any secret value in its message.
func TestRenderTemplateNoValueLeakOnMissing(t *testing.T) {
	secrets := map[string]string{"SECRET": "super-sensitive-value"}
	_, err := renderTemplate("t", `{{ .MISSING }}`, secrets)
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	if strings.Contains(err.Error(), "super-sensitive-value") {
		t.Fatalf("error leaked a secret value: %v", err)
	}
}

func TestRenderWatchReRendersOnVersionBump(t *testing.T) {
	// versions: baseline read (=1), then ticks see 1,2,2. Only the 1→2 bump
	// should trigger a re-render.
	vf := &fakeVersionFetcher{seq: []int{1, 1, 2, 2}}
	fake := newFakeTicker()
	renders := 0
	render := func() error { renders++; return nil }

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_ = renderWatch(vf, "c1", time.Second, func(time.Duration) ticker { return fake }, render, &buf)
		close(done)
	}()

	fake.tick() // sees 1, no change
	fake.tick() // sees 2, re-render
	fake.tick() // sees 2, no change
	fake.close()
	<-done

	if renders != 1 {
		t.Fatalf("re-render count = %d, want 1 (output: %s)", renders, buf.String())
	}
}
