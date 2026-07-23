package store

import (
	"context"
	"testing"
	"time"
)

func TestOpenAndPing(t *testing.T) {
	s := requireStore(t)
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

// TestOpenWithConfigAppliesPoolTuning opens a dedicated pool (not the shared
// one) with explicit PoolConfig and asserts the values are reflected on the
// live pool's Config(). Skipped when Docker/Postgres is unavailable.
func TestOpenWithConfigAppliesPoolTuning(t *testing.T) {
	requireStore(t) // skip early if no postgres
	ctx := context.Background()

	pc := PoolConfig{
		MaxConns:        7,
		MinConns:        2,
		MaxConnLifetime: 42 * time.Minute,
		MaxConnIdleTime: 13 * time.Minute,
	}
	st, err := OpenWithConfig(ctx, testStore.dsn, pc)
	if err != nil {
		t.Fatalf("OpenWithConfig: %v", err)
	}
	defer st.Close()

	got := st.pool.Config()
	if got.MaxConns != pc.MaxConns {
		t.Errorf("MaxConns = %d, want %d", got.MaxConns, pc.MaxConns)
	}
	if got.MinConns != pc.MinConns {
		t.Errorf("MinConns = %d, want %d", got.MinConns, pc.MinConns)
	}
	if got.MaxConnLifetime != pc.MaxConnLifetime {
		t.Errorf("MaxConnLifetime = %s, want %s", got.MaxConnLifetime, pc.MaxConnLifetime)
	}
	if got.MaxConnIdleTime != pc.MaxConnIdleTime {
		t.Errorf("MaxConnIdleTime = %s, want %s", got.MaxConnIdleTime, pc.MaxConnIdleTime)
	}
}

// TestOpenWithConfigZeroKeepsDefaults verifies that a zero PoolConfig leaves
// pgx's DSN-derived defaults untouched (Open's behavior).
func TestOpenWithConfigZeroKeepsDefaults(t *testing.T) {
	requireStore(t)
	ctx := context.Background()

	st, err := OpenWithConfig(ctx, testStore.dsn, PoolConfig{})
	if err != nil {
		t.Fatalf("OpenWithConfig: %v", err)
	}
	defer st.Close()

	// pgx default MaxConns is max(4, NumCPU); it is always >= 4 and never the
	// zero value, which proves the zero PoolConfig did not clobber it.
	if got := st.pool.Config().MaxConns; got < 4 {
		t.Errorf("default MaxConns = %d, want >= 4 (pgx default preserved)", got)
	}
}
