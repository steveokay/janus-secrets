package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
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

type initRequest struct {
	Shares    int `json:"shares"`
	Threshold int `json:"threshold"`
}

type initResponse struct {
	Type   string   `json:"type"`
	Shares []string `json:"shares,omitempty"`
}

func (s *Server) handleInit(w http.ResponseWriter, r *http.Request) {
	// Serialize init: the unsealer's Init is get-then-put against an upserting
	// store, so two concurrent inits in this process could BOTH return 200,
	// handing one operator shares that fail the stored KCV — a false success
	// carrying key material. The mutex makes the loser see the winner's config
	// and get a clean 409. (Cross-process races are out of scope: single-node
	// deployment is the supported topology per the project's non-goals.)
	s.initMu.Lock()
	defer s.initMu.Unlock()

	var req initRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid JSON body")
		return
	}

	switch s.cfg.SealType {
	case crypto.SealTypeShamir:
		if !validInitParams(req.Shares, req.Threshold) {
			writeError(w, http.StatusBadRequest, CodeValidation, "invalid seal parameters")
			return
		}
		// A short-lived unsealer carries the requested share/threshold params;
		// the long-lived one (0,0) handles submission and unseal afterwards.
		u := crypto.NewShamirUnsealer(s.seals, req.Shares, req.Threshold)
		res, err := u.Init(r.Context())
		if err != nil {
			s.writeInitError(w, err)
			return
		}
		shares := make([]string, len(res.Shares))
		for i, sh := range res.Shares {
			shares[i] = hex.EncodeToString(sh)
			zero(sh) // one-time exposure: the response is the only copy
		}
		writeJSON(w, http.StatusOK, initResponse{Type: crypto.SealTypeShamir, Shares: shares})

	case crypto.SealTypeAWSKMS:
		if req.Shares != 0 || req.Threshold != 0 {
			writeError(w, http.StatusBadRequest, CodeValidation,
				"shares/threshold do not apply to a kms seal")
			return
		}
		if _, err := s.unsealer.Init(r.Context()); err != nil {
			s.writeInitError(w, err)
			return
		}
		// Auto-unseal: the operator holds nothing under KMS.
		if err := s.unsealNow(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, initResponse{Type: crypto.SealTypeAWSKMS})

	default:
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
	}
}

// validInitParams reports whether the requested shamir split parameters are
// acceptable: (0,0) means library defaults, (1,1) is the dev special case,
// otherwise mirror the shamir library's constraints
// (2 <= threshold <= shares <= 255).
func validInitParams(shares, threshold int) bool {
	if shares == 0 && threshold == 0 {
		return true
	}
	if shares == 1 && threshold == 1 {
		return true
	}
	return threshold >= 2 && shares >= threshold && shares <= 255
}

func (s *Server) writeInitError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, crypto.ErrAlreadyInitialized):
		writeError(w, http.StatusConflict, CodeAlreadyInitialized, "seal is already initialized")
	default:
		// Parameters are validated before Init is called, so anything else is
		// an infrastructure failure (store, rand, KMS) — never a 400.
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
	}
}

// unsealNow runs the unsealer and feeds the keyring, zeroizing the master.
func (s *Server) unsealNow(ctx context.Context) error {
	master, err := s.unsealer.Unseal(ctx)
	if err != nil {
		return err
	}
	defer zero(master)
	if err := s.keyring.Unseal(master); err != nil && !errors.Is(err, crypto.ErrAlreadyUnsealed) {
		return err
	}
	return nil
}

type unsealRequest struct {
	Share string `json:"share"`
}

func (s *Server) handleUnseal(w http.ResponseWriter, r *http.Request) {
	var req unsealRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid JSON body")
		return
	}
	initialized, cfg, err := s.sealConfig(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if !initialized {
		writeError(w, http.StatusBadRequest, CodeNotInitialized, "seal is not initialized")
		return
	}

	switch cfg.Type {
	case crypto.SealTypeAWSKMS:
		if req.Share != "" {
			writeError(w, http.StatusBadRequest, CodeValidation, "kms seal takes no share")
			return
		}
		if !s.keyring.Sealed() {
			writeJSON(w, http.StatusOK, unsealStateBody(s, cfg))
			return
		}
		if err := s.unsealNow(r.Context()); err != nil {
			// KMS outage / IAM failure: generic error, no internals.
			writeError(w, http.StatusInternalServerError, CodeInternal, "unseal failed")
			return
		}
		writeJSON(w, http.StatusOK, unsealStateBody(s, cfg))

	case crypto.SealTypeShamir:
		sh, ok := s.unsealer.(*crypto.ShamirUnsealer)
		if !ok {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		if req.Share == "" {
			writeError(w, http.StatusBadRequest, CodeValidation, "share is required")
			return
		}
		raw, err := hex.DecodeString(req.Share)
		if err != nil {
			writeError(w, http.StatusBadRequest, CodeInvalidShare, "share is not valid hex")
			return
		}
		defer zero(raw)
		if !s.keyring.Sealed() {
			writeJSON(w, http.StatusOK, unsealStateBody(s, cfg))
			return
		}
		progress, err := sh.SubmitShare(r.Context(), raw)
		if err != nil {
			switch {
			case errors.Is(err, crypto.ErrDuplicateShare):
				writeError(w, http.StatusBadRequest, CodeDuplicateShare, "share already submitted")
			case errors.Is(err, crypto.ErrInvalidShare):
				writeError(w, http.StatusBadRequest, CodeInvalidShare, "invalid share")
			default:
				writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			}
			return
		}
		// A concurrent request may have crossed the threshold while we were
		// submitting: if the keyring is already unsealed, the share we just
		// deposited is stale — clear it (Reset zeroizes) and report success.
		if !s.keyring.Sealed() {
			sh.Reset()
			writeJSON(w, http.StatusOK, unsealStateBody(s, cfg))
			return
		}
		if progress.Submitted >= progress.Required {
			if err := s.unsealNow(r.Context()); err != nil {
				// Losing racer: a concurrent winner consumed the shares and
				// unsealed. Clear any share we deposited and report success.
				if !s.keyring.Sealed() {
					sh.Reset()
					writeJSON(w, http.StatusOK, unsealStateBody(s, cfg))
					return
				}
				if errors.Is(err, crypto.ErrInvalidShare) || errors.Is(err, crypto.ErrKeyCheckFailed) {
					// Reconstruction or KCV failure: the submitted set is
					// poisoned; the operator resets and resubmits.
					writeError(w, http.StatusBadRequest, CodeKeyCheckFailed,
						"key reconstruction failed; discard submitted shares via /v1/sys/unseal/reset and resubmit")
					return
				}
				writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
				return
			}
		}
		writeJSON(w, http.StatusOK, unsealStateBody(s, cfg))

	default:
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
	}
}

// unsealStateBody is the shared unseal/reset response shape.
func unsealStateBody(s *Server, cfg *crypto.SealConfig) map[string]any {
	body := map[string]any{"sealed": s.keyring.Sealed()}
	if cfg.Type == crypto.SealTypeShamir && s.keyring.Sealed() {
		body["progress"] = s.shamirProgress(cfg.Threshold)
	}
	return body
}

func (s *Server) handleUnsealReset(w http.ResponseWriter, r *http.Request) {
	initialized, cfg, err := s.sealConfig(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if !initialized {
		writeError(w, http.StatusBadRequest, CodeNotInitialized, "seal is not initialized")
		return
	}
	sh, ok := s.unsealer.(*crypto.ShamirUnsealer)
	if !ok {
		writeError(w, http.StatusBadRequest, CodeValidation, "reset applies to shamir seals only")
		return
	}
	sh.Reset()
	writeJSON(w, http.StatusOK, unsealStateBody(s, cfg))
}

func (s *Server) handleSeal(w http.ResponseWriter, r *http.Request) {
	// NOTE: unauthenticated until the auth milestone (availability-only,
	// fail-closed). See the design spec's security posture.
	s.keyring.Seal()
	writeJSON(w, http.StatusOK, map[string]any{"sealed": true})
}
