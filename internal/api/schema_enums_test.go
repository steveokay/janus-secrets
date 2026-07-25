package api

import (
	"sort"
	"testing"

	"github.com/steveokay/janus-secrets/internal/notification"
	"github.com/steveokay/janus-secrets/internal/rotation"
	"github.com/steveokay/janus-secrets/internal/secretsync"
	"github.com/steveokay/janus-secrets/internal/store"
)

// TestSchemaEnumsMatchCode guards a bug class that has now shipped TWICE: a
// CREATE TABLE pins a column to `CHECK (col IN (...))`, later features add
// values in Go, and no migration ever widens the constraint — so the feature is
// documented, reachable through the API, and impossible to persist.
//
//   - 000010 pinned rotation_policies.type to postgres/webhook; mysql and redis
//     shipped anyway and only became usable in 000037.
//   - 000011 pinned sync_targets.provider to github/k8s; six further providers
//     shipped and none could be saved until 000041.
//
// The check reads the EFFECTIVE constraint from pg_constraint — not the
// migration text, since a later DROP/ADD leaves the original CREATE wording in
// the file and grepping it reports a stale answer — and compares it against the
// list each package derives from the same registry its factory dispatches on.
// So a provider/rotator that is constructible is necessarily in the list, and
// the list is necessarily required of the database.
//
// Equality is asserted in BOTH directions:
//   - code accepts, DB rejects  → the shipped-but-unusable bug above
//   - DB accepts, code doesn't  → a stale constraint; such rows would be
//     persistable but fail at dispatch time
//
// This test lives in internal/api because it is the only package that already
// imports secretsync, rotation and notification together (each of those imports
// store, so hosting it in store would be an import cycle).
func TestSchemaEnumsMatchCode(t *testing.T) {
	dsn := bootPostgres(t) // skips when Docker is unavailable
	st, err := store.Open(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		table, column string
		code          []string
		source        string
	}{
		{"sync_targets", "provider", secretsync.AllProviders(),
			"secretsync.providerRegistry (every constructible sync provider)"},
		{"rotation_policies", "type", rotation.AllTypes(),
			"rotation.rotatorRegistry (every constructible rotator)"},
		{"notification_channels", "type", notification.AllChannelTypes(),
			"notification.channelTypes (every deliverable channel)"},
	} {
		t.Run(tc.table+"."+tc.column, func(t *testing.T) {
			db, err := st.CheckConstraintValues(t.Context(), tc.table, tc.column)
			if err != nil {
				t.Fatalf("read CHECK constraint: %v", err)
			}
			if len(db) == 0 {
				t.Fatalf("no enum-style CHECK found on %s.%s — the guard would be vacuous;"+
					" if the constraint was intentionally dropped, drop this case too",
					tc.table, tc.column)
			}
			code := append([]string(nil), tc.code...)
			sort.Strings(code)

			if missing := difference(code, db); len(missing) > 0 {
				t.Errorf(
					"%s.%s: code accepts %v but the DATABASE REJECTS them.\n"+
						"  code source : %s\n"+
						"  db accepts  : %v\n"+
						"  These are advertised but CANNOT BE PERSISTED. Add a migration widening\n"+
						"  the constraint — see 000037 and 000041 for the pattern.",
					tc.table, tc.column, missing, tc.source, db)
			}
			if extra := difference(db, code); len(extra) > 0 {
				t.Errorf(
					"%s.%s: the database accepts %v but the code does not.\n"+
						"  code source : %s\n"+
						"  Rows with those values would persist and then fail at dispatch.",
					tc.table, tc.column, extra, tc.source)
			}
		})
	}
}

// difference returns elements of a not present in b.
func difference(a, b []string) []string {
	set := make(map[string]bool, len(b))
	for _, v := range b {
		set[v] = true
	}
	var out []string
	for _, v := range a {
		if !set[v] {
			out = append(out, v)
		}
	}
	return out
}
