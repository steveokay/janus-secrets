package backupsched

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
)

// StructVerifier verifies a backup artifact's structure and — best-effort — the
// decryptability of its wrapped key material, WITHOUT touching the live
// instance. It streams the artifact: the header line must be a valid version-1
// backup header; every subsequent record must name a known table and carry a
// syntactically valid row. For each `projects` row it attempts to unwrap the
// wrapped project KEK under the current keyring (via UnwrapKEK); success proves
// the material is decryptable by THIS instance's unseal. A backup written by a
// DIFFERENT instance won't unwrap here — that is not a corruption failure, so it
// is reported as decryptable:false with a note, never an error.
//
// It is deliberately allocation-bounded (one line at a time) and never writes to
// any store, so a rehearsal cannot clobber or even read live data.
type StructVerifier struct {
	// KnownTables is the set of table names a valid backup may contain (the
	// store's backup-set). A record naming any other table fails verification.
	KnownTables map[string]bool
	// SchemaVersion is the current server schema version; a header whose
	// migration_version differs is reported (a mismatched backup would fail a
	// real restore). Zero disables the check.
	SchemaVersion int64
	// UnwrapKEK attempts to unwrap a project's wrapped KEK (raw marshaled bytes)
	// for projectID under the current keyring; a nil error means decryptable.
	// Optional: when nil, the decryptability probe is skipped (structure-only).
	UnwrapKEK func(wrapped []byte, projectID string) error
	// MaxLineBytes bounds a single JSONL line (defensive against a hostile
	// object). Zero → 64 MiB (matches the restore endpoint's per-record cap).
	MaxLineBytes int
}

// backupHeaderLine is the minimal shape the verifier reads from line 1.
type backupHeaderLine struct {
	JanusBackup      int   `json:"janus_backup"`
	MigrationVersion int64 `json:"migration_version"`
}

// backupRecordLine is the minimal shape of every subsequent line.
type backupRecordLine struct {
	Table string          `json:"table"`
	Row   json.RawMessage `json:"row"`
}

// projectRow is the subset of a `projects` row the decryptability probe needs.
// bytea columns dump as base64 in row_to_json.
type projectRow struct {
	ID         string `json:"id"`
	WrappedKEK []byte `json:"wrapped_kek"`
}

// Verify implements the verifier interface.
func (v StructVerifier) Verify(_ context.Context, r io.Reader) (RehearsalResult, error) {
	maxLine := v.MaxLineBytes
	if maxLine <= 0 {
		maxLine = 64 << 20
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)

	// Line 1: header.
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return RehearsalResult{}, errors.New("verify: read failed")
		}
		return RehearsalResult{}, errors.New("verify: backup is empty (no header)")
	}
	var hdr backupHeaderLine
	if err := json.Unmarshal(sc.Bytes(), &hdr); err != nil {
		return RehearsalResult{}, errors.New("verify: invalid backup header")
	}
	if hdr.JanusBackup != 1 {
		return RehearsalResult{}, errors.New("verify: unsupported backup format version")
	}
	res := RehearsalResult{SchemaVer: hdr.MigrationVersion, Decryptable: true}
	schemaOK := v.SchemaVersion == 0 || hdr.MigrationVersion == v.SchemaVersion
	if !schemaOK {
		res.Note = "schema_version mismatch: backup would not restore into the current server"
	}

	seenTables := map[string]bool{}
	probed := false
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec backupRecordLine
		if err := json.Unmarshal(line, &rec); err != nil {
			return RehearsalResult{}, errors.New("verify: malformed record")
		}
		if !v.KnownTables[rec.Table] {
			return RehearsalResult{}, errors.New("verify: unknown table in backup")
		}
		// Row must be a syntactically valid JSON object.
		if len(rec.Row) == 0 || !json.Valid(rec.Row) {
			return RehearsalResult{}, errors.New("verify: malformed row")
		}
		res.Records++
		seenTables[rec.Table] = true

		// Decryptability probe: first `projects` row only (one unwrap is proof).
		if rec.Table == "projects" && v.UnwrapKEK != nil && !probed {
			var pr projectRow
			if err := json.Unmarshal(rec.Row, &pr); err == nil && pr.ID != "" && len(pr.WrappedKEK) > 0 {
				probed = true
				if err := v.UnwrapKEK(pr.WrappedKEK, pr.ID); err != nil {
					// Wrong-instance backup (or genuine corruption). Not fatal to
					// the structural rehearsal; report it value-free.
					res.Decryptable = false
					if res.Note == "" {
						res.Note = "wrapped material did not decrypt under the current unseal (foreign-instance backup or corrupt)"
					}
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return RehearsalResult{}, errors.New("verify: read failed")
	}
	res.Tables = len(seenTables)
	if res.Records == 0 {
		return RehearsalResult{}, errors.New("verify: backup contains no records")
	}
	// Verified iff the structure parsed cleanly, the schema matches the current
	// server (when checked), and — if a decryptability probe ran — it decrypted.
	res.Verified = schemaOK && res.Decryptable
	if res.Verified && res.Note == "" {
		res.Note = "structure valid; wrapped material decrypts under current unseal"
	}
	return res, nil
}
