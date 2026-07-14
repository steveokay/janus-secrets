package store

import (
	"context"
	"testing"
)

// insertSecretValue inserts one secret_values row directly. It fills the
// NOT NULL ciphertext/nonce columns with sentinel bytes; RewrapBatch must never
// read them (its SELECT lists neither ciphertext nor nonce).
func insertSecretValue(t *testing.T, s *Store, configID, key string, valueVersion int, wrappedDEK []byte, dekKeyVersion int) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO secret_values (id, config_id, key, value_version, wrapped_dek, ciphertext, nonce, dek_key_version)
		 VALUES (gen_random_uuid(), $1::uuid, $2, $3, $4, E'\\x00', E'\\x00', $5)`,
		configID, key, valueVersion, wrappedDEK, dekKeyVersion); err != nil {
		t.Fatal(err)
	}
}

func TestSecretRepoRewrapBatch(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()

	projectID, _, configID := mkConfig(t, s, "prod")
	insertSecretValue(t, s, configID, "A", 1, []byte("old-A"), 1)
	insertSecretValue(t, s, configID, "B", 1, []byte("old-B"), 1)
	if _, err := s.pool.Exec(ctx, `UPDATE projects SET kek_version=2 WHERE id=$1::uuid`, projectID); err != nil {
		t.Fatal(err)
	}

	sr := NewSecretRepo(s)
	seen := map[string]bool{}
	processed, next, err := sr.RewrapBatch(ctx, projectID, 2, "", 100,
		func(row RewrapRow) ([]byte, error) {
			seen[row.Key] = true
			if row.DEKKeyVersion != 1 {
				t.Fatalf("row %s at version %d, want 1", row.Key, row.DEKKeyVersion)
			}
			return []byte("new-" + row.Key), nil
		})
	if err != nil {
		t.Fatalf("RewrapBatch: %v", err)
	}
	if processed != 2 || next != "" {
		t.Fatalf("processed=%d next=%q, want 2 and empty", processed, next)
	}
	if !seen["A"] || !seen["B"] {
		t.Fatalf("did not process both: %v", seen)
	}

	// Verify rows advanced to version 2 with the new blob.
	var ver int
	var wrapped []byte
	if err := s.pool.QueryRow(ctx,
		`SELECT dek_key_version, wrapped_dek FROM secret_values sv
		   JOIN configs c ON c.id=sv.config_id
		   JOIN environments e ON e.id=c.environment_id
		  WHERE e.project_id=$1::uuid AND sv.key='A'`, projectID).Scan(&ver, &wrapped); err != nil {
		t.Fatal(err)
	}
	if ver != 2 || string(wrapped) != "new-A" {
		t.Fatalf("A after rewrap = v%d %q", ver, wrapped)
	}

	// Second call: nothing left below version 2.
	processed2, _, err := sr.RewrapBatch(ctx, projectID, 2, "", 100, func(RewrapRow) ([]byte, error) {
		t.Fatal("rewrap called with no pending rows")
		return nil, nil
	})
	if err != nil || processed2 != 0 {
		t.Fatalf("second RewrapBatch processed=%d err=%v, want 0", processed2, err)
	}
}
