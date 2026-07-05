package store

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// AuditRepo persists the append-only, hash-chained audit log.
type AuditRepo struct{ s *Store }

// NewAuditRepo returns an audit repository.
func NewAuditRepo(s *Store) *AuditRepo { return &AuditRepo{s: s} }

// auditAdvisoryKey serializes all chain appends (one fixed key for the log).
const auditAdvisoryKey int64 = 0x6A616E75736C6F67 // "januslog"

const auditCols = `seq, occurred_at, actor_kind, actor_id, actor_name, action,
	resource, detail, result, result_code, ip, prev_hash, hash`

func scanAuditRow(row interface{ Scan(...any) error }) (AuditRow, error) {
	var a AuditRow
	if err := row.Scan(&a.Seq, &a.OccurredAt, &a.ActorKind, &a.ActorID, &a.ActorName,
		&a.Action, &a.Resource, &a.Detail, &a.Result, &a.ResultCode, &a.IP,
		&a.PrevHash, &a.Hash); err != nil {
		return AuditRow{}, mapError(err)
	}
	return a, nil
}

// Append serializes on the advisory lock, reads the chain head, lets compute
// build the next row (seq/prev_hash/hash), and inserts it — all in one tx, so
// concurrent appends produce a contiguous, unbroken chain.
func (r *AuditRepo) Append(ctx context.Context, compute func(AuditHead) (AuditRow, error)) (AuditRow, error) {
	var out AuditRow
	err := r.s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, auditAdvisoryKey); err != nil {
			return mapError(err)
		}
		var head AuditHead
		err := tx.QueryRow(ctx, `SELECT seq, hash FROM audit_events ORDER BY seq DESC LIMIT 1`).
			Scan(&head.Seq, &head.Hash)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return mapError(err)
		}
		row, err := compute(head)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO audit_events (`+auditCols+`)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			row.Seq, row.OccurredAt, row.ActorKind, row.ActorID, row.ActorName,
			row.Action, row.Resource, row.Detail, row.Result, row.ResultCode,
			row.IP, row.PrevHash, row.Hash); err != nil {
			return mapError(err)
		}
		out = row
		return nil
	})
	return out, err
}

// Iterate calls fn for every event in ascending seq order (chain verification).
func (r *AuditRepo) Iterate(ctx context.Context, fn func(AuditRow) error) error {
	rows, err := r.s.pool.Query(ctx, `SELECT `+auditCols+` FROM audit_events ORDER BY seq ASC`)
	if err != nil {
		return mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanAuditRow(rows)
		if err != nil {
			return err
		}
		if err := fn(a); err != nil {
			return err
		}
	}
	return mapError(rows.Err())
}

// List calls fn for every event matching f, in ascending seq order (export).
func (r *AuditRepo) List(ctx context.Context, f AuditFilter, fn func(AuditRow) error) error {
	var where []string
	var args []any
	add := func(cond string, val any) { args = append(args, val); where = append(where, cond) }
	if f.From != nil {
		add("occurred_at >= $"+itoa(len(args)+1), *f.From)
	}
	if f.To != nil {
		add("occurred_at <= $"+itoa(len(args)+1), *f.To)
	}
	if f.Action != "" {
		add("action = $"+itoa(len(args)+1), f.Action)
	}
	if f.Result != "" {
		add("result = $"+itoa(len(args)+1), f.Result)
	}
	if f.Actor != "" {
		n := itoa(len(args) + 1)
		args = append(args, f.Actor)
		where = append(where, "(actor_id = $"+n+" OR actor_name = $"+n+")")
	}
	sql := `SELECT ` + auditCols + ` FROM audit_events`
	if len(where) > 0 {
		sql += ` WHERE ` + strings.Join(where, " AND ")
	}
	sql += ` ORDER BY seq ASC`
	rows, err := r.s.pool.Query(ctx, sql, args...)
	if err != nil {
		return mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanAuditRow(rows)
		if err != nil {
			return err
		}
		if err := fn(a); err != nil {
			return err
		}
	}
	return mapError(rows.Err())
}

func itoa(n int) string { return strconv.Itoa(n) }
