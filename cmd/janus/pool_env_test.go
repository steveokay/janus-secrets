package main

import (
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// TestParsePoolConfig covers the JANUS_DB_* env parsing surface: valid values,
// unset (zero = pgx defaults), and each invalid form.
func TestParsePoolConfig(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    store.PoolConfig
		wantErr bool
	}{
		{
			name: "all unset keeps zero (pgx defaults)",
			env:  map[string]string{},
			want: store.PoolConfig{},
		},
		{
			name: "all set",
			env: map[string]string{
				"JANUS_DB_MAX_CONNS":          "20",
				"JANUS_DB_MIN_CONNS":          "3",
				"JANUS_DB_MAX_CONN_LIFETIME":  "2h",
				"JANUS_DB_MAX_CONN_IDLE_TIME": "45m",
			},
			want: store.PoolConfig{
				MaxConns:        20,
				MinConns:        3,
				MaxConnLifetime: 2 * time.Hour,
				MaxConnIdleTime: 45 * time.Minute,
			},
		},
		{
			name: "min conns zero is allowed",
			env:  map[string]string{"JANUS_DB_MIN_CONNS": "0"},
			want: store.PoolConfig{}, // 0 → left as pgx default in store.apply
		},
		{
			name:    "max conns non-numeric",
			env:     map[string]string{"JANUS_DB_MAX_CONNS": "lots"},
			wantErr: true,
		},
		{
			name:    "max conns zero rejected (positive required)",
			env:     map[string]string{"JANUS_DB_MAX_CONNS": "0"},
			wantErr: true,
		},
		{
			name:    "max conns negative rejected",
			env:     map[string]string{"JANUS_DB_MAX_CONNS": "-1"},
			wantErr: true,
		},
		{
			name:    "min conns negative rejected",
			env:     map[string]string{"JANUS_DB_MIN_CONNS": "-2"},
			wantErr: true,
		},
		{
			name:    "lifetime bad duration",
			env:     map[string]string{"JANUS_DB_MAX_CONN_LIFETIME": "1 hour"},
			wantErr: true,
		},
		{
			name:    "lifetime zero rejected",
			env:     map[string]string{"JANUS_DB_MAX_CONN_LIFETIME": "0s"},
			wantErr: true,
		},
		{
			name:    "idle time bad duration",
			env:     map[string]string{"JANUS_DB_MAX_CONN_IDLE_TIME": "later"},
			wantErr: true,
		},
	}

	// Clear all four so cases that omit a var see it unset regardless of order.
	allVars := []string{
		"JANUS_DB_MAX_CONNS", "JANUS_DB_MIN_CONNS",
		"JANUS_DB_MAX_CONN_LIFETIME", "JANUS_DB_MAX_CONN_IDLE_TIME",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range allVars {
				t.Setenv(k, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got, err := parsePoolConfig()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (config %+v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("parsePoolConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
