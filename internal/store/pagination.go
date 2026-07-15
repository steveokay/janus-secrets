package store

import (
	"fmt"
	"time"
)

// Cursor is a keyset position for (created_at DESC, id DESC) pagination.
// It is the opaque continuation token the API layer encodes for clients.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// keyset returns an SQL predicate (and its args) selecting rows strictly after
// `after` in (created_at DESC, id DESC) order, using positional placeholders
// beginning at argN. Because both sort columns descend, a single lexicographic
// row-value comparison is a correct strict keyset. Returns ("", nil) when after
// is nil (first page). The id placeholder is cast to uuid to match the pk type.
func keyset(after *Cursor, argN int) (string, []any) {
	if after == nil {
		return "", nil
	}
	return fmt.Sprintf("(created_at, id) < ($%d, $%d::uuid)", argN, argN+1),
		[]any{after.CreatedAt, after.ID}
}

// limitSQL returns " LIMIT $argN" (and its arg) when limit > 0, else ("", nil)
// so a non-positive limit produces an unbounded query (the legacy List path).
func limitSQL(limit, argN int) (string, []any) {
	if limit <= 0 {
		return "", nil
	}
	return fmt.Sprintf(" LIMIT $%d", argN), []any{limit}
}
