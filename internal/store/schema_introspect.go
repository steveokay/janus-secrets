package store

import (
	"context"
	"sort"
	"strings"
)

// CheckConstraintValues returns the string literals accepted by the
// enum-style CHECK constraint currently in force for table.column — e.g.
// `CHECK (provider IN ('github','k8s'))` yields ["github","k8s"].
//
// It reads the EFFECTIVE definition from pg_constraint rather than the
// migration files on purpose: a later migration that DROPs and re-ADDs a
// constraint leaves the original CREATE TABLE text in place, so grepping
// migrations reports a stale answer. Only the live catalog knows what the
// database will actually accept.
//
// Returns an empty slice when the column has no enum-style CHECK.
func (s *Store) CheckConstraintValues(ctx context.Context, table, column string) ([]string, error) {
	const q = `
		SELECT pg_get_constraintdef(c.oid)
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE c.contype = 'c'
		  AND t.relname = $1
		  AND n.nspname = current_schema()`

	rows, err := s.pool.Query(ctx, q, table)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var values []string
	for rows.Next() {
		var def string
		if err := rows.Scan(&def); err != nil {
			return nil, mapError(err)
		}
		if vals, ok := parseCheckInList(def, column); ok {
			values = append(values, vals...)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	sort.Strings(values)
	return values, nil
}

// parseCheckInList extracts the quoted literals from a constraint definition
// for column. Postgres normalizes `col IN ('a','b')` to
// `CHECK (((col)::text = ANY ((ARRAY['a'::text, 'b'::text])::text[])))`, so the
// ARRAY[...] form is handled first; the literal `IN (...)` form is a fallback.
// ok is false when def is not an enum-style check on column.
func parseCheckInList(def, column string) ([]string, bool) {
	if !strings.Contains(def, column) {
		return nil, false
	}

	var body string
	if i := strings.Index(def, "ARRAY["); i >= 0 {
		rest := def[i+len("ARRAY["):]
		end := strings.Index(rest, "]")
		if end < 0 {
			return nil, false
		}
		body = rest[:end]
	} else {
		i := strings.Index(strings.ToUpper(def), " IN (")
		if i < 0 {
			return nil, false
		}
		rest := def[i+len(" IN ("):]
		end := strings.Index(rest, ")")
		if end < 0 {
			return nil, false
		}
		body = rest[:end]
	}

	var out []string
	for _, part := range strings.Split(body, ",") {
		part = strings.TrimSpace(part)
		if c := strings.Index(part, "::"); c >= 0 { // strip a ::text cast
			part = strings.TrimSpace(part[:c])
		}
		if len(part) >= 2 && strings.HasPrefix(part, "'") && strings.HasSuffix(part, "'") {
			out = append(out, strings.ReplaceAll(part[1:len(part)-1], "''", "'"))
		}
	}
	return out, len(out) > 0
}
