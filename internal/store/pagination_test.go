package store

import (
	"testing"
	"time"
)

func TestKeyset(t *testing.T) {
	ts := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// nil cursor → empty predicate, no args
	if sql, args := keyset(nil, 1); sql != "" || args != nil {
		t.Fatalf("nil cursor: got %q %v", sql, args)
	}
	// cursor → predicate with placeholders starting at argN, id cast to uuid
	sql, args := keyset(&Cursor{CreatedAt: ts, ID: "abc"}, 3)
	if sql != "(created_at, id) < ($3, $4::uuid)" {
		t.Fatalf("sql = %q", sql)
	}
	if len(args) != 2 || args[0] != ts || args[1] != "abc" {
		t.Fatalf("args = %v", args)
	}
}

func TestLimitSQL(t *testing.T) {
	if sql, args := limitSQL(0, 5); sql != "" || args != nil {
		t.Fatalf("limit 0: got %q %v", sql, args)
	}
	if sql, args := limitSQL(-3, 5); sql != "" || args != nil {
		t.Fatalf("negative limit: got %q %v", sql, args)
	}
	sql, args := limitSQL(50, 2)
	if sql != " LIMIT $2" {
		t.Fatalf("sql = %q", sql)
	}
	if len(args) != 1 || args[0] != 50 {
		t.Fatalf("args = %v", args)
	}
}
