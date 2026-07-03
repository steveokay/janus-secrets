package store

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapError(t *testing.T) {
	tests := []struct {
		name string
		in   error
		want error // asserted with errors.Is; ignored when passthrough is true
		// passthrough means mapError must return tt.in unchanged (including nil).
		passthrough bool
	}{
		{name: "nil", in: nil, passthrough: true},
		{name: "no rows", in: pgx.ErrNoRows, want: ErrNotFound},
		{name: "wrapped no rows", in: fmt.Errorf("query: %w", pgx.ErrNoRows), want: ErrNotFound},
		{name: "unique", in: &pgconn.PgError{Code: "23505"}, want: ErrAlreadyExists},
		{name: "wrapped unique", in: fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23505"}), want: ErrAlreadyExists},
		{name: "fk", in: &pgconn.PgError{Code: "23503"}, want: ErrParentNotFound},
		{name: "unmapped pg code", in: &pgconn.PgError{Code: "42P01"}, passthrough: true},
		{name: "generic error", in: errors.New("boom"), passthrough: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapError(tt.in)
			if tt.passthrough {
				if got != tt.in {
					t.Fatalf("got %v, want passthrough of %v", got, tt.in)
				}
				return
			}
			if !errors.Is(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
