package main

import "testing"

func TestParseMaxAge(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"90d", 90 * 24 * 3600, false},
		{"2160h", 2160 * 3600, false},
		{"1d", 24 * 3600, false},
		{"2w", 2 * 7 * 24 * 3600, false},
		{"30m", 30 * 60, false},
		{"1h30m", 3600 + 1800, false},
		{"", 0, true},
		{"0d", 0, true},
		{"0s", 0, true},
		{"-5h", 0, true},
		{"abc", 0, true},
		{"90x", 0, true},
	}
	for _, tt := range tests {
		got, err := parseMaxAge(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseMaxAge(%q) = %d, want error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseMaxAge(%q) error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseMaxAge(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestHumanizeSeconds(t *testing.T) {
	if got := humanizeSeconds(90 * 24 * 3600); got != "90d" {
		t.Errorf("humanizeSeconds(90d) = %q", got)
	}
	if got := humanizeSeconds(3600 + 1800); got != "1h30m0s" {
		t.Errorf("humanizeSeconds(1h30m) = %q", got)
	}
}
