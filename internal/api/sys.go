package api

import (
	"errors"
	"net/http"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// zero overwrites b with zeros (best-effort; see internal/crypto).
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// sealConfig returns (initialized, cfg). A missing seal config is the
// uninitialized state, not an error.
func (s *Server) sealConfig(r *http.Request) (bool, *crypto.SealConfig, error) {
	cfg, err := s.seals.Get(r.Context())
	if errors.Is(err, crypto.ErrNoSealConfig) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return true, cfg, nil
}

type progressBody struct {
	Submitted int `json:"submitted"`
	Required  int `json:"required"`
}

// shamirProgress returns submit progress when the unsealer is Shamir.
func (s *Server) shamirProgress(required int) *progressBody {
	sh, ok := s.unsealer.(*crypto.ShamirUnsealer)
	if !ok {
		return nil
	}
	return &progressBody{Submitted: sh.SubmittedShares(), Required: required}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	initialized, _, err := s.sealConfig(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"initialized": initialized,
		"sealed":      s.keyring.Sealed(),
	})
}

type sealStatusResponse struct {
	Initialized bool          `json:"initialized"`
	Sealed      bool          `json:"sealed"`
	Type        string        `json:"type"`
	Threshold   int           `json:"threshold,omitempty"`
	Shares      int           `json:"shares,omitempty"`
	Progress    *progressBody `json:"progress,omitempty"`
}

func (s *Server) handleSealStatus(w http.ResponseWriter, r *http.Request) {
	initialized, cfg, err := s.sealConfig(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	resp := sealStatusResponse{
		Initialized: initialized,
		Sealed:      s.keyring.Sealed(),
		Type:        s.cfg.SealType,
	}
	if initialized {
		resp.Type = cfg.Type
		if cfg.Type == crypto.SealTypeShamir {
			resp.Threshold = cfg.Threshold
			resp.Shares = cfg.Shares
			if resp.Sealed {
				resp.Progress = s.shamirProgress(cfg.Threshold)
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// Implemented in Task 5.
func (s *Server) handleInit(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusInternalServerError, CodeInternal, "not implemented")
}
func (s *Server) handleUnseal(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusInternalServerError, CodeInternal, "not implemented")
}
func (s *Server) handleUnsealReset(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusInternalServerError, CodeInternal, "not implemented")
}
func (s *Server) handleSeal(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusInternalServerError, CodeInternal, "not implemented")
}
