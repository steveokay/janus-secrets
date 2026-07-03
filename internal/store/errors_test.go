package store

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapError(t *testing.T) {
	tests := []struct {
		name string
		in   error
		want error
	}{
		{"nil", nil, nil},
		{"no rows", pgx.ErrNoRows, ErrNotFound},
		{"unique", &pgconn.PgError{Code: "23505"}, ErrAlreadyExists},
		{"fk", &pgconn.PgError{Code: "23503"}, ErrParentNotFound},
		{"other pg", &pgconn.PgError{Code: "42P01"}, nil}, // passthrough (not mapped)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapError(tt.in)
			switch {
			case tt.want == nil && tt.name == "nil":
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
			case tt.want == nil: // passthrough: same error back
				if !errors.Is(got, tt.in) {
					t.Fatalf("got %v, want passthrough of %v", got, tt.in)
				}
			default:
				if !errors.Is(got, tt.want) {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}
