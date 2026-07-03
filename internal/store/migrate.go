package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers "pgx5" scheme
	"github.com/golang-migrate/migrate/v4/source/iofs"

	migrations "github.com/steveokay/janus-secrets/migrations"
)

// Migrate applies all up-migrations. It is idempotent: an already-current
// database is a success, not an error.
func (s *Store) Migrate(_ context.Context) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("store: migration source: %w", err)
	}
	// golang-migrate's pgx/v5 driver registers the "pgx5" URL scheme.
	dbURL := "pgx5://" + strings.TrimPrefix(strings.TrimPrefix(s.dsn, "postgres://"), "postgresql://")
	m, err := migrate.NewWithSourceInstance("iofs", src, dbURL)
	if err != nil {
		return fmt.Errorf("store: migrate init: %w", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("store: migrate up: %w", err)
	}
	return nil
}

// migrateDownForTest tears the schema all the way down. Used only by tests to
// verify the down migration; production never calls it.
func (s *Store) migrateDownForTest() error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return err
	}
	dbURL := "pgx5://" + strings.TrimPrefix(strings.TrimPrefix(s.dsn, "postgres://"), "postgresql://")
	m, err := migrate.NewWithSourceInstance("iofs", src, dbURL)
	if err != nil {
		return err
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}
