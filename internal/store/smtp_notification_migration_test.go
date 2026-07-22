package store

import (
	"context"
	"testing"
)

// TestMigration027AllowsSMTPChannel asserts the 000027 CHECK-constraint change
// admits an `smtp` channel row (webhook/slack still allowed) and rejects a
// bogus type.
func TestMigration027AllowsSMTPChannel(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()

	for _, typ := range []string{"webhook", "slack", "smtp"} {
		_, err := s.pool.Exec(ctx,
			`INSERT INTO notification_channels (name, type, events, config_ct, created_by)
			 VALUES ($1, $2, '{}', '\x00', 'tester')`, "chan-"+typ, typ)
		if err != nil {
			t.Fatalf("insert %s channel should succeed after 000027: %v", typ, err)
		}
	}

	// A type outside the allowed set is still rejected by the constraint.
	_, err := s.pool.Exec(ctx,
		`INSERT INTO notification_channels (name, type, events, config_ct, created_by)
		 VALUES ('bad', 'carrier-pigeon', '{}', '\x00', 'tester')`)
	if err == nil {
		t.Fatal("expected the type CHECK to reject an unknown channel type")
	}
}
