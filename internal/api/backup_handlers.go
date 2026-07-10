package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
)

// backupHeader is line 1 of a dump; restore validates it before any insert.
type backupHeader struct {
	JanusBackup      int    `json:"janus_backup"`
	MigrationVersion int64  `json:"migration_version"`
	JanusVersion     string `json:"janus_version"`
	CreatedAt        string `json:"created_at"`
}

// backupRecord is every subsequent line.
type backupRecord struct {
	Table string          `json:"table"`
	Row   json.RawMessage `json:"row"`
}

// handleBackup streams a key-preserving full-instance dump. Auth-gated in
// production via RequireAuth + sys:backup (see New); rows are emitted exactly
// as stored, so the stream contains no plaintext secrets by construction.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	ver, err := s.st.SchemaVersion(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	// Audit BEFORE streaming: once the body starts we cannot switch to an
	// error response (same rule as the audit export handler). Audit-write
	// failure fails the request.
	if err := s.record(r, "sys.backup", "", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	now := time.Now().UTC()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", "janus-backup-"+now.Format("20060102T150405Z")+".jsonl"))
	hdr, err := json.Marshal(backupHeader{
		JanusBackup:      1,
		MigrationVersion: ver,
		JanusVersion:     s.cfg.Version,
		CreatedAt:        now.Format(time.RFC3339),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if _, err := w.Write(append(hdr, '\n')); err != nil {
		return // client went away; nothing to do
	}
	if err := s.st.DumpBackup(r.Context(), w); err != nil {
		// Headers are committed; a truncated stream fails restore's
		// transaction safely on the other end. Log and stop.
		s.logger.Warn("backup stream failed", "err", err)
	}
}

// handleRestore rebuilds an EMPTY instance from a dump. Like /v1/sys/init it
// is a pre-auth bootstrap operation: no credentials exist to check on an
// empty instance, and the emptiness gate (plus initMu) is the guard.
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	// Serialize against init (and concurrent restores) — same mutex, same
	// reasoning as handleInit. The store re-checks emptiness inside the
	// restore transaction as well; this lock closes the remaining window.
	s.initMu.Lock()
	defer s.initMu.Unlock()

	empty, err := s.st.IsEmptyForRestore(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if !empty {
		writeError(w, http.StatusConflict, CodeNotEmpty,
			"restore requires an empty instance (fresh database, before init)")
		return
	}

	dec := json.NewDecoder(bufio.NewReaderSize(r.Body, 1<<20))
	var hdr backupHeader
	if err := dec.Decode(&hdr); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "missing or invalid backup header")
		return
	}
	if hdr.JanusBackup != 1 {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			fmt.Sprintf("unsupported backup format version %d (this server reads version 1)", hdr.JanusBackup))
		return
	}
	ver, err := s.st.SchemaVersion(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if hdr.MigrationVersion != ver {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			fmt.Sprintf("backup schema version %d does not match server schema version %d; run the janus version that wrote this backup",
				hdr.MigrationVersion, ver))
		return
	}

	err = s.st.RestoreBackup(r.Context(), func() (string, []byte, error) {
		var rec backupRecord
		if err := dec.Decode(&rec); err != nil {
			return "", nil, err // io.EOF terminates cleanly
		}
		return rec.Table, rec.Row, nil
	})
	if err != nil {
		// Store errors may carry driver detail (never row plaintext, but
		// possibly emails/paths) — log the specifics, return a generic body.
		s.logger.Warn("restore failed; transaction rolled back", "err", err)
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			"restore failed; the instance is unchanged (see server log)")
		return
	}

	// Append sys.restore to the restored hash chain: the audit store's Append
	// reads the chain head from the table, so this event continues the
	// RESTORED chain and GET /v1/audit/verify passes across the restore
	// boundary.
	if err := s.recordActor(r, audit.Actor{Kind: "anonymous"},
		"sys.restore", "", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"restored": true, "sealed": true})
}
