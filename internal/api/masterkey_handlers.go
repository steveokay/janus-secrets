package api

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/masterkeys"
)

// handleMasterKeyStatus reports the seal type + master-key version/rotated_at and
// any in-progress rekey ceremony. A plain read gated on sys:master-key
// (owner-only); no audit event, mirroring the sibling KEK status handler.
func (s *Server) handleMasterKeyStatus(w http.ResponseWriter, r *http.Request) {
	if err := s.can(r, authz.SysMasterKey, authz.Instance()); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	st, err := s.masterKeys.Status(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	resp := map[string]any{
		"unseal_type":        st.UnsealType,
		"master_key_version": st.Version,
		"rekey_in_progress":  st.RekeyInProg,
		"submitted":          st.Submitted,
		"required":           st.Required,
		"rotated_at":         nil,
	}
	if st.RotatedAt != nil {
		resp["rotated_at"] = st.RotatedAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMasterKeyRotate performs a single-call master-key rotation. Owner-only.
// KMS seals rotate here; a Shamir seal returns 400 (a rekey ceremony is
// required). The response carries only the new version — never key material.
func (s *Server) handleMasterKeyRotate(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.SysMasterKey, authz.Instance(), "sys.master-key.rotate", "sys/master-key") {
		return
	}
	version, err := s.masterKeys.Rotate(r.Context())
	if err != nil {
		if errors.Is(err, masterkeys.ErrShamirCeremonyRequired) {
			writeError(w, http.StatusBadRequest, CodeValidation, "shamir seal requires a rekey ceremony")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "sys.master-key.rotate", "sys/master-key", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"master_key_version": version})
}

// handleMasterKeyRekeyInit opens the single Shamir rekey ceremony. Owner-only.
// Returns a nonce and the number of current shares required.
func (s *Server) handleMasterKeyRekeyInit(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.SysMasterKey, authz.Instance(), "sys.master-key.rekey.init", "sys/master-key") {
		return
	}
	nonce, required, err := s.masterKeys.RekeyInit(r.Context())
	if err != nil {
		s.writeMasterKeyErr(w, err)
		return
	}
	if err := s.record(r, "sys.master-key.rekey.init", "sys/master-key", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nonce": nonce, "required": required, "submitted": 0})
}

// handleMasterKeyRekeySubmit accepts one current share of the open ceremony.
// Owner-only. Shares are hex-encoded, matching the unseal endpoint's wire
// format. The submitted share is zeroized after use; on completion the fresh
// shares are returned exactly once (hex) then zeroized. Value-free audit.
func (s *Server) handleMasterKeyRekeySubmit(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.SysMasterKey, authz.Instance(), "sys.master-key.rekey.submit", "sys/master-key") {
		return
	}
	var body struct {
		Nonce string `json:"nonce"`
		Share string `json:"share"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid JSON body")
		return
	}
	share, derr := hex.DecodeString(body.Share)
	if derr != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidShare, "share is not valid hex")
		return
	}
	complete, shares, version, submitted, required, err := s.masterKeys.RekeySubmit(r.Context(), body.Nonce, share)
	zero(share)
	if err != nil {
		s.writeMasterKeyErr(w, err)
		return
	}
	if !complete {
		// Not yet committed: a failed audit write may safely 500, as no
		// unrecoverable secret has been produced.
		if err := s.record(r, "sys.master-key.rekey.submit", "sys/master-key", "success", "", ""); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"complete": false, "submitted": submitted, "required": required})
		return
	}
	// Completing submit: the rotation is already committed and swapped, and
	// `shares` is the operator's ONLY copy of the new unseal shares. A failed
	// audit write must NOT drop them — failing the request cannot un-rotate, it
	// only locks the operator out. Record best-effort; log a degradation on
	// failure (never the shares themselves); always return the shares.
	if err := s.record(r, "sys.master-key.rekey.complete", "sys/master-key", "success", "", ""); err != nil {
		s.logger.Error("audit write failed after master-key rotation completed; returning new shares anyway", "err", err)
	}
	encoded := make([]string, len(shares))
	for i, sh := range shares {
		encoded[i] = hex.EncodeToString(sh)
	}
	zeroShares(shares) // one-time exposure: the response is the operator's only copy
	writeJSON(w, http.StatusOK, map[string]any{
		"complete":           true,
		"master_key_version": version,
		"new_shares":         encoded,
	})
}

// handleMasterKeyRekeyCancel drops the open ceremony (zeroizing accumulated
// shares). Owner-only.
func (s *Server) handleMasterKeyRekeyCancel(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.SysMasterKey, authz.Instance(), "sys.master-key.rekey.cancel", "sys/master-key") {
		return
	}
	if err := s.masterKeys.RekeyCancel(); err != nil {
		s.writeMasterKeyErr(w, err)
		return
	}
	if err := s.record(r, "sys.master-key.rekey.cancel", "sys/master-key", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeMasterKeyErr maps the ceremony sentinels to the HTTP envelope. Sealed →
// 503 (via writeServiceError); an in-progress ceremony → 409; a KMS seal (no
// ceremony) → 400; any share/nonce/reconstruction rejection → 400, never
// leaking which check failed.
func (s *Server) writeMasterKeyErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, crypto.ErrSealed):
		s.writeServiceError(w, err)
	case errors.Is(err, masterkeys.ErrRekeyInProgress):
		writeError(w, http.StatusConflict, "conflict", "a rekey ceremony is already in progress")
	case errors.Is(err, masterkeys.ErrKMSNoCeremony):
		writeError(w, http.StatusBadRequest, CodeValidation, "kms seal does not use a rekey ceremony")
	case errors.Is(err, masterkeys.ErrNoRekey), errors.Is(err, masterkeys.ErrRekeyNonce),
		errors.Is(err, crypto.ErrInvalidShare), errors.Is(err, crypto.ErrNotEnoughShares),
		errors.Is(err, crypto.ErrKeyCheckFailed), errors.Is(err, crypto.ErrDuplicateShare):
		writeError(w, http.StatusBadRequest, CodeInvalidShare, "rekey share rejected")
	default:
		s.writeServiceError(w, err)
	}
}

// zeroShares overwrites each share slice with zeros (best-effort).
func zeroShares(ss [][]byte) {
	for _, sh := range ss {
		zero(sh)
	}
}
