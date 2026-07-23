package api

import (
	"context"
	"net/http"
	"time"

	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
	"github.com/steveokay/janus-secrets/internal/version"
)

// sysStatusResponse is the value-free operational snapshot served by
// GET /v1/sys/status. No secrets, no per-user data — connection counts,
// aggregate counts, and timing only. The web health panel codes against these
// exact field names.
type sysStatusResponse struct {
	Version       string                     `json:"version"`
	Commit        string                     `json:"commit"`
	UptimeSeconds int64                      `json:"uptime_seconds"`
	Sealed        bool                       `json:"sealed"`
	SealType      string                     `json:"seal_type"`
	DB            dbStatus                   `json:"db"`
	Audit         auditStatus                `json:"audit"`
	Schedulers    map[string]schedulerStatus `json:"schedulers"`
	Runs          runsStatus                 `json:"runs"`
	Leases        leasesStatus               `json:"leases"`
	// AuditShip is the audit-shipper snapshot, present only when a destination is
	// configured (JANUS_AUDIT_SHIP_MODE=webhook|syslog). Value-free.
	AuditShip *auditShipStatus `json:"audit_ship,omitempty"`
}

// auditShipStatus mirrors auditship.Status — mode, destination label, current
// high-water seq, and last ship time/count/error. No URL/host, no secret.
type auditShipStatus struct {
	Mode          string     `json:"mode"`
	Destination   string     `json:"destination,omitempty"`
	HighWaterSeq  int64      `json:"high_water_seq"`
	LastShipAt    *time.Time `json:"last_ship_at,omitempty"`
	LastShipCount int        `json:"last_ship_count"`
	LastError     string     `json:"last_error,omitempty"`
}

type dbStatus struct {
	Reachable bool     `json:"reachable"`
	LatencyMS int64    `json:"latency_ms"`
	Pool      poolStat `json:"pool"`
}

type poolStat struct {
	Total    int32 `json:"total"`
	Idle     int32 `json:"idle"`
	Acquired int32 `json:"acquired"`
	Max      int32 `json:"max"`
}

type auditStatus struct {
	HeadSeq    int64 `json:"head_seq"`
	EventCount int64 `json:"event_count"`
}

type schedulerStatus struct {
	Enabled bool `json:"enabled"`
	// LastTickAgeSeconds is null when the scheduler has never ticked (e.g. just
	// booted, or disabled).
	LastTickAgeSeconds *int64 `json:"last_tick_age_seconds"`
	IntervalSeconds    int64  `json:"interval_seconds"`
}

type runsStatus struct {
	RotationFailed int64 `json:"rotation_failed"`
	SyncFailed     int64 `json:"sync_failed"`
}

type leasesStatus struct {
	Active int64 `json:"active"`
}

// handleSysStatus serves the admin health snapshot. Requires auth + the same
// instance-level AuditRead authority as the metrics/audit read endpoints. Never
// 500s on a DB blip — an unreachable database yields reachable:false and still
// 200 so the panel renders.
func (s *Server) handleSysStatus(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.AuditRead, authz.Instance(), "sys.status", "sys/status") {
		return
	}

	resp := sysStatusResponse{
		Version:       version.Version,
		Commit:        version.Commit,
		UptimeSeconds: int64(time.Since(s.startTime).Seconds()),
		Sealed:        s.keyring == nil || s.keyring.Sealed(),
		SealType:      s.cfg.SealType,
		Schedulers:    map[string]schedulerStatus{},
	}

	// DB reachability + latency (short ping; reachable:false on error, never 500).
	if s.st != nil {
		pingCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		start := time.Now()
		err := s.st.Ping(pingCtx)
		lat := time.Since(start)
		cancel()
		if err == nil {
			resp.DB.Reachable = true
			resp.DB.LatencyMS = lat.Milliseconds()
		}
		if stat := s.st.PoolStat(); stat != nil {
			resp.DB.Pool = poolStat{
				Total:    stat.TotalConns(),
				Idle:     stat.IdleConns(),
				Acquired: stat.AcquiredConns(),
				Max:      stat.MaxConns(),
			}
		}

		// Aggregate counts under a bounded context; best-effort (leave zero on error).
		aggCtx, cancel2 := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel2()
		h := store.NewHealthRepo(s.st)
		if v, err := h.AuditHeadSeq(aggCtx); err == nil {
			resp.Audit.HeadSeq = v
		}
		if v, err := h.AuditEventCount(aggCtx); err == nil {
			resp.Audit.EventCount = v
		}
		if v, err := h.RotationRunsFailed(aggCtx); err == nil {
			resp.Runs.RotationFailed = v
		}
		if v, err := h.SyncRunsFailed(aggCtx); err == nil {
			resp.Runs.SyncFailed = v
		}
		if v, err := h.DynamicLeasesActive(aggCtx); err == nil {
			resp.Leases.Active = v
		}
	}

	// Per-engine scheduler status: enabled when its tick interval > 0.
	for _, eng := range []struct {
		name     string
		interval time.Duration
	}{
		{"rotation", s.cfg.RotationTick},
		{"sync", s.cfg.SyncTick},
		{"dynamic", s.cfg.DynamicTick},
	} {
		st := schedulerStatus{
			Enabled:         eng.interval > 0,
			IntervalSeconds: int64(eng.interval.Seconds()),
		}
		if s.ticks != nil {
			if last, ok := s.ticks.LastTick(eng.name); ok {
				age := int64(time.Since(last).Seconds())
				st.LastTickAgeSeconds = &age
			}
		}
		resp.Schedulers[eng.name] = st
	}

	// Audit shipper: present only when a destination is configured. Value-free.
	if s.auditShip != nil {
		ss := s.auditShip.Status()
		resp.AuditShip = &auditShipStatus{
			Mode:          ss.Mode,
			Destination:   ss.Destination,
			HighWaterSeq:  ss.HighWaterSeq,
			LastShipAt:    ss.LastShipAt,
			LastShipCount: ss.LastShipCount,
			LastError:     ss.LastError,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
