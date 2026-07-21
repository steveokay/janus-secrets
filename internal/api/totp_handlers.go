package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/steveokay/janus-secrets/internal/auth"
)

// requireUser resolves the calling user principal or writes 401. TOTP is a
// self-service, user-only surface (service tokens have no second factor).
func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, ok := PrincipalFrom(r.Context())
	if !ok || p.Kind != auth.KindUser {
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "a user session is required")
		return auth.Principal{}, false
	}
	return p, true
}

// writeTOTPError maps TOTP sentinels to the envelope.
func (s *Server) writeTOTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrTOTPState):
		writeError(w, http.StatusConflict, "totp_state", "invalid two-factor state")
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid code")
	default:
		s.writeAuthError(w, err)
	}
}

func (s *Server) handleTOTPStatus(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	enabled, remaining, err := s.auth.TOTPStatus(r.Context(), p.ID)
	if err != nil {
		s.writeTOTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "recovery_remaining": remaining})
}

func (s *Server) handleTOTPEnroll(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	secret, otpauth, err := s.auth.EnrollTOTP(r.Context(), p.ID, p.Name)
	if err != nil {
		s.writeTOTPError(w, err)
		return
	}
	if err := s.record(r, "totp.enroll", "users/"+p.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	// The secret + otpauth URI are shown once; they are never logged or audited.
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret, "otpauth_url": otpauth})
}

type totpCodeReq struct {
	Code string `json:"code"`
}

func (s *Server) handleTOTPConfirm(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req totpCodeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "a code is required")
		return
	}
	codes, err := s.auth.ConfirmTOTP(r.Context(), p.ID, req.Code)
	if err != nil {
		s.writeTOTPError(w, err)
		return
	}
	if err := s.record(r, "totp.confirm", "users/"+p.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req totpCodeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "a code is required")
		return
	}
	if err := s.auth.DisableTOTP(r.Context(), p.ID, req.Code); err != nil {
		s.writeTOTPError(w, err)
		return
	}
	if err := s.record(r, "totp.disable", "users/"+p.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTOTPRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var req totpCodeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "a code is required")
		return
	}
	codes, err := s.auth.RegenerateRecoveryCodes(r.Context(), p.ID, req.Code)
	if err != nil {
		s.writeTOTPError(w, err)
		return
	}
	if err := s.record(r, "totp.recovery.regenerate", "users/"+p.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}
