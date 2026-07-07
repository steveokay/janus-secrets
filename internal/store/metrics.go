package store

import "context"

// ConfigReads / TokenReads / Reads24h are aggregated read counts for the usage
// dashboard. All counts cover successful secret.reveal events in the trailing
// 24 hours (DB clock).
type ConfigReads struct {
	ConfigID    string
	ConfigName  string
	ProjectName string
	Reads       int64
}
type TokenReads struct {
	TokenID   string
	TokenName string
	Reads     int64
}
type Reads24h struct {
	Total      int64
	TopConfigs []ConfigReads
	TopTokens  []TokenReads
}

// MetricsRepo derives read counts on demand from audit_events. Stateless;
// construct one per request from the shared *Store.
type MetricsRepo struct{ s *Store }

func NewMetricsRepo(s *Store) *MetricsRepo { return &MetricsRepo{s: s} }

// baseWhere selects successful in-window reveals; alias the table as `ae`.
const baseWhere = `ae.action = 'secret.reveal' AND ae.result = 'success'
	AND ae.occurred_at > now() - interval '24 hours'`

// Reads24h aggregates trailing-24h reveal counts. projectID == nil →
// instance-wide; non-nil → restricted to configs belonging to that project.
func (r *MetricsRepo) Reads24h(ctx context.Context, projectID *string) (Reads24h, error) {
	var out Reads24h

	// --- Total ---
	// Instance total counts all reveals (even to since-destroyed configs).
	// Project total joins the parsed config id to filter by project.
	if projectID == nil {
		if err := r.s.pool.QueryRow(ctx,
			`SELECT count(*) FROM audit_events ae WHERE `+baseWhere).Scan(&out.Total); err != nil {
			return out, mapError(err)
		}
	} else {
		totalSQL := `SELECT count(*) FROM audit_events ae
			JOIN configs c ON c.id::text = substring(ae.resource from 'configs/([^/]+)/secrets')
			JOIN environments e ON e.id = c.environment_id
			WHERE ` + baseWhere + ` AND e.project_id = $1`
		if err := r.s.pool.QueryRow(ctx, totalSQL, *projectID).Scan(&out.Total); err != nil {
			return out, mapError(err)
		}
	}

	// --- Top configs (names required → always join; project filter optional) ---
	cfgSQL := `SELECT c.id::text, c.name, p.name, count(*) AS reads
		FROM audit_events ae
		JOIN configs c ON c.id::text = substring(ae.resource from 'configs/([^/]+)/secrets')
		JOIN environments e ON e.id = c.environment_id
		JOIN projects p ON p.id = e.project_id
		WHERE ` + baseWhere
	cfgArgs := []any{}
	if projectID != nil {
		cfgSQL += ` AND e.project_id = $1`
		cfgArgs = append(cfgArgs, *projectID)
	}
	cfgSQL += ` GROUP BY c.id, c.name, p.name ORDER BY reads DESC, c.name ASC LIMIT 5`
	rows, err := r.s.pool.Query(ctx, cfgSQL, cfgArgs...)
	if err != nil {
		return out, mapError(err)
	}
	for rows.Next() {
		var cr ConfigReads
		if err := rows.Scan(&cr.ConfigID, &cr.ConfigName, &cr.ProjectName, &cr.Reads); err != nil {
			rows.Close()
			return out, mapError(err)
		}
		out.TopConfigs = append(out.TopConfigs, cr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, mapError(err)
	}

	// --- Top tokens (service tokens only; project filter joins config chain) ---
	tokSQL := `SELECT st.id::text, st.name, count(*) AS reads
		FROM audit_events ae
		JOIN service_tokens st ON st.id::text = ae.actor_id`
	if projectID != nil {
		tokSQL += `
		JOIN configs c ON c.id::text = substring(ae.resource from 'configs/([^/]+)/secrets')
		JOIN environments e ON e.id = c.environment_id`
	}
	tokSQL += ` WHERE ` + baseWhere + ` AND ae.actor_kind = 'service_token'`
	tokArgs := []any{}
	if projectID != nil {
		tokSQL += ` AND e.project_id = $1`
		tokArgs = append(tokArgs, *projectID)
	}
	tokSQL += ` GROUP BY st.id, st.name ORDER BY reads DESC, st.name ASC LIMIT 5`
	trows, err := r.s.pool.Query(ctx, tokSQL, tokArgs...)
	if err != nil {
		return out, mapError(err)
	}
	for trows.Next() {
		var tr TokenReads
		if err := trows.Scan(&tr.TokenID, &tr.TokenName, &tr.Reads); err != nil {
			trows.Close()
			return out, mapError(err)
		}
		out.TopTokens = append(out.TopTokens, tr)
	}
	trows.Close()
	if err := trows.Err(); err != nil {
		return out, mapError(err)
	}

	return out, nil
}
