package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/secretsync"
	"github.com/steveokay/janus-secrets/internal/store"
)

// ── sync drift detection (roadmap 7.4) ───────────────────────────────────────
//
// Every DTO in this file is VALUE-FREE: key names, counts, booleans, timestamps,
// a capability word, and a sanitized error category. No secret value — and no
// digest of one — is ever serialized. Authorization reuses the existing
// authz.SyncManage action; drift verification reads the same destination the
// sync target already writes, so it needs no new permission.

// syncVerifyStateDTO is the per-target verification schedule + last summary.
type syncVerifyStateDTO struct {
	Enabled         bool    `json:"enabled"`
	IntervalSeconds int64   `json:"interval_seconds"`
	NextVerifyAt    string  `json:"next_verify_at"`
	LastVerifyAt    *string `json:"last_verify_at,omitempty"`
	LastStatus      *string `json:"last_status,omitempty"`
	LastDriftCount  int     `json:"last_drift_count"`
	// Capability is the provider's declared read capability
	// ("values" | "names_only" | "none"), so the UI can say plainly whether a
	// clean result means "values verified" or only "key names checked".
	Capability string `json:"capability"`
}

func toVerifyStateDTO(st store.SyncVerifyState, capability string) syncVerifyStateDTO {
	out := syncVerifyStateDTO{
		Enabled: st.Enabled, IntervalSeconds: st.IntervalSeconds,
		LastStatus: st.LastStatus, LastDriftCount: st.LastDriftCount,
		Capability: capability,
	}
	if !st.NextVerifyAt.IsZero() {
		out.NextVerifyAt = st.NextVerifyAt.UTC().Format(time.RFC3339)
	}
	if st.LastVerifyAt != nil {
		s := st.LastVerifyAt.UTC().Format(time.RFC3339)
		out.LastVerifyAt = &s
	}
	return out
}

// syncVerifyReportDTO is one verification pass, as returned by POST .../verify.
type syncVerifyReportDTO struct {
	TargetID       string `json:"target_id"`
	Status         string `json:"status"`     // clean | drift | error | unsupported
	Capability     string `json:"capability"` // values | names_only | none
	ValuesCompared bool   `json:"values_compared"`
	// ExtraIsDrift reports whether unmanaged extra keys count toward the verdict
	// (true when the target prunes, i.e. Janus owns the destination namespace).
	ExtraIsDrift    bool     `json:"extra_is_drift"`
	MissingKeys     []string `json:"missing_keys"`
	ModifiedKeys    []string `json:"modified_keys"`
	ExtraKeys       []string `json:"extra_keys"`
	UnreadableKeys  []string `json:"unreadable_keys"`
	MissingCount    int      `json:"missing_count"`
	ModifiedCount   int      `json:"modified_count"`
	ExtraCount      int      `json:"extra_count"`
	UnreadableCount int      `json:"unreadable_count"`
	CheckedCount    int      `json:"checked_count"`
	StartedAt       string   `json:"started_at"`
	EndedAt         string   `json:"ended_at"`
	Error           string   `json:"error,omitempty"`
}

func toVerifyReportDTO(rep secretsync.VerifyReport) syncVerifyReportDTO {
	return syncVerifyReportDTO{
		TargetID: rep.TargetID, Status: rep.Status, Capability: string(rep.Capability),
		ValuesCompared: rep.ValuesCompared, ExtraIsDrift: rep.ExtraIsDrift,
		MissingKeys: nonNilKeys(rep.Missing), ModifiedKeys: nonNilKeys(rep.Modified),
		ExtraKeys: nonNilKeys(rep.Extra), UnreadableKeys: nonNilKeys(rep.Unreadable),
		MissingCount: rep.MissingCount, ModifiedCount: rep.ModifiedCount,
		ExtraCount: rep.ExtraCount, UnreadableCount: rep.UnreadableCount,
		CheckedCount: rep.Checked,
		StartedAt:    rep.StartedAt.UTC().Format(time.RFC3339),
		EndedAt:      rep.EndedAt.UTC().Format(time.RFC3339),
		Error:        rep.Error,
	}
}

// syncVerifyRunDTO is one persisted verification pass in the history list.
type syncVerifyRunDTO struct {
	ID              int64    `json:"id"`
	StartedAt       string   `json:"started_at"`
	EndedAt         string   `json:"ended_at"`
	Status          string   `json:"status"`
	Capability      string   `json:"capability"`
	ValuesCompared  bool     `json:"values_compared"`
	MissingKeys     []string `json:"missing_keys"`
	ModifiedKeys    []string `json:"modified_keys"`
	ExtraKeys       []string `json:"extra_keys"`
	UnreadableKeys  []string `json:"unreadable_keys"`
	MissingCount    int      `json:"missing_count"`
	ModifiedCount   int      `json:"modified_count"`
	ExtraCount      int      `json:"extra_count"`
	UnreadableCount int      `json:"unreadable_count"`
	CheckedCount    int      `json:"checked_count"`
	Error           *string  `json:"error,omitempty"`
}

type syncVerifyRunsResponse struct {
	Runs       []syncVerifyRunDTO `json:"runs"`
	NextCursor *int64             `json:"next_cursor"`
}

func nonNilKeys(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// handleSyncVerify runs an immediate drift verification for one target.
func (s *Server) handleSyncVerify(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.syncResource(r)
	if err != nil {
		s.writeSyncErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.SyncManage, res, "sync.verify.request", "sync/"+v.ID) {
		return
	}
	// The engine writes its own sync.verify audit event (system actor) carrying
	// the value-free result summary.
	rep, err := s.sync.Verify(r.Context(), v.ID)
	if err != nil {
		if errors.Is(err, secretsync.ErrApplyFailed) || errors.Is(err, secretsync.ErrInvalidConfig) {
			// The pass ran and was recorded; surface the sanitized outcome rather
			// than a bare 500 so the operator sees "error" in the history.
			writeJSON(w, http.StatusOK, toVerifyReportDTO(rep))
			return
		}
		s.writeSyncErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toVerifyReportDTO(rep))
}

// handleSyncVerifyRuns lists recorded verification passes for one target.
func (s *Server) handleSyncVerifyRuns(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.syncResource(r)
	if err != nil {
		s.writeSyncErr(w, err)
		return
	}
	if err := s.can(r, authz.SyncManage, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	limit, cursor, ok := parseRunsPaging(w, r)
	if !ok {
		return
	}
	runs, err := s.sync.ListVerifyRuns(r.Context(), v.ID, cursor, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	out := make([]syncVerifyRunDTO, 0, len(runs))
	for _, x := range runs {
		out = append(out, syncVerifyRunDTO{
			ID:        x.ID,
			StartedAt: x.StartedAt.UTC().Format(time.RFC3339),
			EndedAt:   x.EndedAt.UTC().Format(time.RFC3339),
			Status:    x.Status, Capability: x.Capability, ValuesCompared: x.ValuesCompared,
			MissingKeys: nonNilKeys(x.MissingKeys), ModifiedKeys: nonNilKeys(x.ModifiedKeys),
			ExtraKeys: nonNilKeys(x.ExtraKeys), UnreadableKeys: nonNilKeys(x.UnreadableKeys),
			MissingCount: x.MissingCount, ModifiedCount: x.ModifiedCount,
			ExtraCount: x.ExtraCount, UnreadableCount: x.UnreadableCount,
			CheckedCount: x.CheckedCount, Error: x.Error,
		})
	}
	var next *int64
	if len(runs) == limit && limit > 0 {
		last := runs[len(runs)-1].ID
		next = &last
	}
	writeJSON(w, http.StatusOK, syncVerifyRunsResponse{Runs: out, NextCursor: next})
}

// updateSyncVerifyReq is the PATCH .../verify-schedule body.
type updateSyncVerifyReq struct {
	Enabled         *bool  `json:"enabled"`
	IntervalSeconds *int64 `json:"interval_seconds"`
}

// handleSyncVerifySchedule updates a target's verification schedule (per-target
// opt-out and pace). The GLOBAL switch remains JANUS_SYNC_VERIFY_TICK.
func (s *Server) handleSyncVerifySchedule(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.syncResource(r)
	if err != nil {
		s.writeSyncErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.SyncManage, res, "sync.verify.schedule", "sync/"+v.ID) {
		return
	}
	var req updateSyncVerifyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	if req.IntervalSeconds != nil && (*req.IntervalSeconds < 60 || *req.IntervalSeconds > 30*24*3600) {
		writeError(w, http.StatusBadRequest, CodeValidation, "interval_seconds must be between 60 and 2592000")
		return
	}
	if err := s.sync.SetVerifySchedule(r.Context(), v.ID, req.Enabled, req.IntervalSeconds); err != nil {
		s.writeSyncErr(w, err)
		return
	}
	if err := s.record(r, "sync.verify.schedule", "sync/"+v.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	st, err := s.sync.GetVerifyState(r.Context(), v.ID)
	if err != nil {
		s.writeSyncErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toVerifyStateDTO(st, string(s.sync.ProviderCapability(v.Provider))))
}
