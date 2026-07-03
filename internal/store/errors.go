package store

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrNotFound is returned when a row does not exist (or is soft-deleted).
	ErrNotFound = errors.New("store: not found")
	// ErrAlreadyExists is returned on a unique-constraint violation.
	ErrAlreadyExists = errors.New("store: already exists")
	// ErrParentNotFound is returned on a foreign-key violation (SQLSTATE 23503).
	// Under the schema's NO ACTION foreign keys this covers both directions:
	// inserting/updating a row whose parent is missing, and (once hard-destroy
	// lands) deleting a row still referenced by a child.
	ErrParentNotFound = errors.New("store: foreign key violation")
	// ErrConflict is returned when an operation targets an absent or
	// soft-deleted config (e.g. saving a version to a deleted config).
	ErrConflict = errors.New("store: conflict")
)

// mapError translates pgx/pgconn driver errors into package sentinels. Errors
// it does not recognize are returned unchanged. nil maps to nil.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return ErrAlreadyExists
		case "23503": // foreign_key_violation
			return ErrParentNotFound
		}
	}
	return err
}
