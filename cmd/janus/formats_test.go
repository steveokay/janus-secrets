package main

import (
	"strings"
	"testing"
)

func TestFormatEnv(t *testing.T) {
	m := map[string]string{"B": "two", "A": "has space", "C": "has'quote"}
	out := string(formatEnv(m))
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if lines[0] != `A='has space'` {
		t.Fatalf("line0 = %q", lines[0])
	}
	if lines[1] != `B=two` { // simple value needs no quotes
		t.Fatalf("line1 = %q", lines[1])
	}
	if lines[2] != `C='has'\''quote'` {
		t.Fatalf("line2 = %q", lines[2])
	}
}

func TestFormatJSONSorted(t *testing.T) {
	b, err := formatJSON(map[string]string{"B": "2", "A": "1"})
	if err != nil {
		t.Fatal(err)
	}
	// encoding/json sorts map keys.
	if !strings.HasPrefix(strings.TrimSpace(string(b)), `{`) || !strings.Contains(string(b), `"A": "1"`) {
		t.Fatalf("json = %s", b)
	}
}

func TestFormatYAML(t *testing.T) {
	b, err := formatYAML(map[string]string{"A": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "A:") || !strings.Contains(string(b), `"1"`) {
		t.Fatalf("yaml = %s", b)
	}
}

func TestFormatDispatch(t *testing.T) {
	if _, err := formatSecrets("toml", nil); err == nil {
		t.Fatal("unknown format should error")
	}
}
