package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers "pgx5" scheme
	"github.com/golang-migrate/migrate/v4/source/iofs"

	migrations "github.com/steveokay/janus-secrets/migrations"
)

// newMigrator builds a migrate.Migrate over the embedded migrations bound to
// this store's database. It is the single place that translates the store DSN
// into the golang-migrate driver URL, so Migrate and migrateDownForTest can
// never drift apart.
func (s *Store) newMigrator() (*migrate.Migrate, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("store: migration source: %w", err)
	}
	// golang-migrate's pgx/v5 driver registers the "pgx5" URL scheme. Parse the
	// DSN and swap the scheme rather than string-trimming a prefix: this
	// preserves userinfo/host/port/query exactly and fails loudly on a non-URL
	// (e.g. libpq keyword) DSN instead of producing a garbage driver URL.
	u, err := url.Parse(s.dsn)
	if err != nil {
		return nil, fmt.Errorf("store: parse dsn: %w", err)
	}
	u.Scheme = "pgx5"
	m, err := migrate.NewWithSourceInstance("iofs", src, u.String())
	if err != nil {
		return nil, fmt.Errorf("store: migrate init: %w", err)
	}
	return m, nil
}

// Migrate applies all up-migrations. It is idempotent: an already-current
// database is a success, not an error.
func (s *Store) Migrate(_ context.Context) error {
	m, err := s.newMigrator()
	if err != nil {
		return err
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
	m, err := s.newMigrator()
	if err != nil {
		return err
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}
