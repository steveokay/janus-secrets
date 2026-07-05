package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
)

// transitUse gates a data-plane request on the transit:use action for the named
// key. It writes the authz error and returns false on denial. Data-plane
// operations are intentionally NOT audited (usage visibility is deferred to a
// later metrics sub-project); handlers here never call s.record.
func (s *Server) transitUse(w http.ResponseWriter, r *http.Request, name string) bool {
	if err := s.can(r, authz.TransitUse, authz.Resource{TransitKey: name}); err != nil {
		s.writeAuthzError(w, err)
		return false
	}
	return true
}

func (s *Server) handleTransitEncrypt(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !s.transitUse(w, r, name) {
		return
	}
	var req struct{ Plaintext, AssociatedData string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	pt, err := base64.StdEncoding.DecodeString(req.Plaintext)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "plaintext must be base64")
		return
	}
	var aad []byte
	if req.AssociatedData != "" {
		if aad, err = base64.StdEncoding.DecodeString(req.AssociatedData); err != nil {
			writeError(w, http.StatusBadRequest, CodeValidation, "associated_data must be base64")
			return
		}
	}
	ct, err := s.transit.Encrypt(r.Context(), name, pt, aad)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ciphertext": ct})
}

func (s *Server) handleTransitDecrypt(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !s.transitUse(w, r, name) {
		return
	}
	var req struct{ Ciphertext, AssociatedData string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Ciphertext == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "ciphertext is required")
		return
	}
	var aad []byte
	if req.AssociatedData != "" {
		var derr error
		if aad, derr = base64.StdEncoding.DecodeString(req.AssociatedData); derr != nil {
			writeError(w, http.StatusBadRequest, CodeValidation, "associated_data must be base64")
			return
		}
	}
	pt, err := s.transit.Decrypt(r.Context(), name, req.Ciphertext, aad)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plaintext": base64.StdEncoding.EncodeToString(pt)})
}

func (s *Server) handleTransitSign(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !s.transitUse(w, r, name) {
		return
	}
	var req struct{ Input string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	input, err := base64.StdEncoding.DecodeString(req.Input)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "input must be base64")
		return
	}
	sig, err := s.transit.Sign(r.Context(), name, input)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signature": sig})
}

func (s *Server) handleTransitVerify(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !s.transitUse(w, r, name) {
		return
	}
	var req struct{ Input, Signature string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	input, err := base64.StdEncoding.DecodeString(req.Input)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "input must be base64")
		return
	}
	// A bad signature is a valid:false result, not an error; only a real engine
	// error is surfaced via writeServiceError.
	ok, err := s.transit.Verify(r.Context(), name, input, req.Signature)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": ok})
}

func (s *Server) handleTransitRewrap(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !s.transitUse(w, r, name) {
		return
	}
	var req struct{ Ciphertext, AssociatedData string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Ciphertext == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "ciphertext is required")
		return
	}
	var aad []byte
	if req.AssociatedData != "" {
		var derr error
		if aad, derr = base64.StdEncoding.DecodeString(req.AssociatedData); derr != nil {
			writeError(w, http.StatusBadRequest, CodeValidation, "associated_data must be base64")
			return
		}
	}
	ct, err := s.transit.Rewrap(r.Context(), name, req.Ciphertext, aad)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ciphertext": ct})
}

func (s *Server) handleTransitDatakeyPlaintext(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !s.transitUse(w, r, name) {
		return
	}
	dek, ct, err := s.transit.DataKey(r.Context(), name)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	resp := map[string]any{
		"ciphertext": ct,
		"plaintext":  base64.StdEncoding.EncodeToString(dek),
	}
	// The plaintext DEK is returned deliberately (this is the explicit
	// plaintext endpoint); zero our copy once it is encoded into the response.
	for i := range dek {
		dek[i] = 0
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleTransitDatakeyWrapped(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !s.transitUse(w, r, name) {
		return
	}
	dek, ct, err := s.transit.DataKey(r.Context(), name)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	// Wrapped variant returns only the wrapped DEK. Zero the plaintext DEK we
	// were handed so it does not linger in memory or leak into the response.
	for i := range dek {
		dek[i] = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{"ciphertext": ct})
}
