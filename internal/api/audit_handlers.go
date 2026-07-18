package api

import (
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

// handleAuditVerify walks the chain and reports integrity. Not self-audited.
func (s *Server) handleAuditVerify(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.AuditRead, authz.Instance(), "audit.verify", "audit") {
		return
	}
	if s.audit == nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "audit is not configured")
		return
	}
	res, err := s.audit.Verify(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// auditEventsResponse is the wire shape of a page of GET /v1/audit/events.
type auditEventsResponse struct {
	Events     []auditExportRow `json:"events"`
	NextCursor *int64           `json:"next_cursor"`
}

// handleAuditEvents serves the viewer: paginated, filterable, NOT self-audited
// (precedent: verify; audit reads are not in the must-audit set — `authorize`
// only records DENIALS, never successes, so this matches /verify exactly).
func (s *Server) handleAuditEvents(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.AuditRead, authz.Instance(), "audit.events", "audit") {
		return
	}
	if s.audit == nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "audit is not configured")
		return
	}
	filter, _, err := parseAuditFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeError(w, http.StatusBadRequest, CodeValidation, "limit must be 1-200")
			return
		}
		limit = n
	}
	var cursor int64
	if v := r.URL.Query().Get("cursor"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, CodeValidation, "cursor must be a positive integer")
			return
		}
		cursor = n
	}
	rows, err := s.audit.ListPage(r.Context(), filter, cursor, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	out := make([]auditExportRow, 0, len(rows))
	for _, a := range rows {
		out = append(out, toExportRow(a))
	}
	var next *int64
	if len(rows) == limit && limit > 0 {
		last := rows[len(rows)-1].Seq
		next = &last
	}
	writeJSON(w, http.StatusOK, auditEventsResponse{Events: out, NextCursor: next})
}

// histBucket is the wire shape of one bucketed count in GET /v1/audit/histogram.
type histBucket struct {
	Start   string `json:"start"`
	Success int    `json:"success"`
	Denied  int    `json:"denied"`
	Error   int    `json:"error"`
}

// handleAuditHistogram serves the histogram: validates the required from/to
// range and bucket, guards against an absurdly large range for the requested
// bucket granularity, then pivots the store's grouped (bucket, result) rows
// into per-bucket success/denied/error counts. Value-free (counts only). Not
// self-audited (precedent: verify/events — audit reads are not in the
// must-audit set).
func (s *Server) handleAuditHistogram(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.AuditRead, authz.Instance(), "audit.histogram", "audit") {
		return
	}
	if s.audit == nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "audit is not configured")
		return
	}
	filter, _, err := parseAuditFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		return
	}
	if filter.From == nil || filter.To == nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "from and to are required")
		return
	}
	bucket := r.URL.Query().Get("bucket")
	if bucket == "" {
		bucket = "day"
	}
	if bucket != "hour" && bucket != "day" {
		writeError(w, http.StatusBadRequest, CodeValidation, "bucket must be hour or day")
		return
	}
	step := time.Hour
	if bucket == "day" {
		step = 24 * time.Hour
	}
	if filter.To.Sub(*filter.From)/step > 1000 {
		writeError(w, http.StatusBadRequest, CodeValidation, "range too large for bucket")
		return
	}
	rows, err := s.audit.Histogram(r.Context(), filter, bucket)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	byStart := map[string]*histBucket{}
	var order []string
	for _, row := range rows {
		key := row.Start.UTC().Format(time.RFC3339)
		b := byStart[key]
		if b == nil {
			b = &histBucket{Start: key}
			byStart[key] = b
			order = append(order, key)
		}
		switch row.Result {
		case "denied":
			b.Denied += row.Count
		case "error":
			b.Error += row.Count
		default:
			b.Success += row.Count
		}
	}
	out := make([]histBucket, 0, len(order))
	for _, k := range order {
		out = append(out, *byStart[k])
	}
	writeJSON(w, http.StatusOK, map[string]any{"buckets": out})
}

// handleAuditExport streams filtered events as JSONL (default) or CSV. The
// export is self-audited BEFORE any body is written, so an aborted download is
// still recorded; if that audit write fails, respond 500 before streaming.
func (s *Server) handleAuditExport(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.AuditRead, authz.Instance(), "audit.export", "audit") {
		return
	}
	if s.audit == nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "audit is not configured")
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "jsonl"
	}
	if format != "jsonl" && format != "csv" {
		writeError(w, http.StatusBadRequest, CodeValidation, "format must be jsonl or csv")
		return
	}
	filter, detail, err := parseAuditFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		return
	}

	// Self-audit before streaming so a mid-download abort is still recorded.
	if aerr := s.record(r, "audit.export", "audit", "success", "", "format="+format+","+detail); aerr != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}

	if format == "csv" {
		s.streamAuditCSV(w, r, filter)
		return
	}
	s.streamAuditJSONL(w, r, filter)
}

// parseAuditFilter builds the store filter from query params and a human detail
// string for the audit record. Invalid RFC3339 times return an error.
func parseAuditFilter(r *http.Request) (store.AuditFilter, string, error) {
	q := r.URL.Query()
	var f store.AuditFilter
	detail := ""
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, "", errBadFilter("from must be RFC3339")
		}
		f.From = &t
		detail += "from=" + v + ","
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, "", errBadFilter("to must be RFC3339")
		}
		f.To = &t
		detail += "to=" + v + ","
	}
	if v := q.Get("actor"); v != "" {
		f.Actor = v
		detail += "actor=" + v + ","
	}
	if v := q.Get("action"); v != "" {
		f.Action = v
		detail += "action=" + v + ","
	}
	if v := q.Get("result"); v != "" {
		if v != "success" && v != "denied" && v != "error" {
			return f, "", errBadFilter("result must be success, denied, or error")
		}
		f.Result = v
		detail += "result=" + v + ","
	}
	if detail == "" {
		detail = "filters=none"
	}
	return f, detail, nil
}

type auditFilterError string

func (e auditFilterError) Error() string { return string(e) }
func errBadFilter(msg string) error      { return auditFilterError(msg) }

// auditExportRow is the wire shape of an exported event (hashes hex-encoded so
// the file is independently verifiable offline).
type auditExportRow struct {
	Seq        int64   `json:"seq"`
	OccurredAt string  `json:"occurred_at"`
	ActorKind  string  `json:"actor_kind"`
	ActorID    *string `json:"actor_id"`
	ActorName  string  `json:"actor_name"`
	Action     string  `json:"action"`
	Resource   string  `json:"resource"`
	Detail     *string `json:"detail"`
	Result     string  `json:"result"`
	ResultCode *string `json:"result_code"`
	IP         string  `json:"ip"`
	PrevHash   string  `json:"prev_hash"`
	Hash       string  `json:"hash"`
}

func toExportRow(a store.AuditRow) auditExportRow {
	return auditExportRow{
		Seq: a.Seq, OccurredAt: a.OccurredAt.UTC().Format(time.RFC3339Nano),
		ActorKind: a.ActorKind, ActorID: a.ActorID, ActorName: a.ActorName,
		Action: a.Action, Resource: a.Resource, Detail: a.Detail, Result: a.Result,
		ResultCode: a.ResultCode, IP: a.IP,
		PrevHash: hex.EncodeToString(a.PrevHash), Hash: hex.EncodeToString(a.Hash),
	}
}

func (s *Server) streamAuditJSONL(w http.ResponseWriter, r *http.Request, f store.AuditFilter) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", `attachment; filename="audit.jsonl"`)
	enc := json.NewEncoder(w)
	if err := s.audit.List(r.Context(), f, func(a store.AuditRow) error {
		return enc.Encode(toExportRow(a))
	}); err != nil {
		s.abortExport(r, err)
	}
}

func (s *Server) streamAuditCSV(w http.ResponseWriter, r *http.Request, f store.AuditFilter) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="audit.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"seq", "occurred_at", "actor_kind", "actor_id", "actor_name",
		"action", "resource", "detail", "result", "result_code", "ip", "prev_hash", "hash"})
	err := s.audit.List(r.Context(), f, func(a store.AuditRow) error {
		row := toExportRow(a)
		return cw.Write([]string{
			itoa64(row.Seq), row.OccurredAt, row.ActorKind, strOrEmpty(row.ActorID), row.ActorName,
			row.Action, row.Resource, strOrEmpty(row.Detail), row.Result, strOrEmpty(row.ResultCode),
			row.IP, row.PrevHash, row.Hash,
		})
	})
	cw.Flush()
	if err == nil {
		err = cw.Error() // a write that failed only at Flush time
	}
	if err != nil {
		s.abortExport(r, err)
	}
}

// abortExport handles a mid-stream export failure. A 200 status, headers, and
// part of the body are already committed, so we cannot switch to an error
// envelope. We log the failure server-side (never silent) and abort the
// response with http.ErrAbortHandler, so the client sees a broken transfer
// rather than mistaking a truncated file for a complete one — completeness is
// the whole point of an offline-verifiable audit export. The self-audited
// export event was recorded before streaming, so the attempt is still logged.
func (s *Server) abortExport(r *http.Request, err error) {
	s.logger.Error("audit export stream failed; response aborted (truncated)",
		"err", err, "path", r.URL.Path)
	panic(http.ErrAbortHandler)
}

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
