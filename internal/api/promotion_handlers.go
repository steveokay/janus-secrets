package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/promote"
	"github.com/steveokay/janus-secrets/internal/store"
)

// promoteActorUser returns the acting user's id for the config_locked_keys
// created_by FK, or "" for a service token (whose id is not a users.id and
// must not be written to the FK; the store maps "" -> NULL).
func promoteActorUser(r *http.Request) string {
	p, _ := PrincipalFrom(r.Context())
	if p.Kind == auth.KindUser {
		return p.ID
	}
	return ""
}

func (s *Server) handlePipelineGet(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if err := s.can(r, authz.ProjectRead, authz.Resource{ProjectID: pid}); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	steps, err := store.NewPipelineRepo(s.st).Get(r.Context(), pid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	ids := make([]string, 0, len(steps))
	for _, step := range steps {
		ids = append(ids, step.EnvironmentID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"environment_ids": ids})
}

func (s *Server) handlePipelinePut(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if !s.authorize(w, r, authz.PromotionManage, authz.Resource{ProjectID: pid}, "promotion.pipeline.set", "projects/"+pid+"/pipeline") {
		return
	}
	var body struct {
		EnvironmentIDs []string `json:"environment_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	if err := store.NewPipelineRepo(s.st).Set(r.Context(), pid, body.EnvironmentIDs); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "promotion.pipeline.set", "projects/"+pid+"/pipeline", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"environment_ids": body.EnvironmentIDs})
}

func (s *Server) handleLockedKeysList(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.ConfigRead, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	keys, err := store.NewLockedKeyRepo(s.st).List(r.Context(), cid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (s *Server) handleLockedKeyCreate(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.PromotionManage, res, "promotion.key.lock", "configs/"+cid+"/locked-keys") {
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "key is required")
		return
	}
	if err := store.NewLockedKeyRepo(s.st).Lock(r.Context(), cid, body.Key, promoteActorUser(r)); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "promotion.key.lock", "configs/"+cid+"/locked-keys/"+body.Key, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": body.Key, "locked": true})
}

func (s *Server) handleLockedKeyDelete(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	key := chi.URLParam(r, "key")
	if !s.authorize(w, r, authz.PromotionManage, res, "promotion.key.unlock", "configs/"+cid+"/locked-keys/"+key) {
		return
	}
	if err := store.NewLockedKeyRepo(s.st).Unlock(r.Context(), cid, key); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "promotion.key.unlock", "configs/"+cid+"/locked-keys/"+key, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "locked": false})
}

// configResourceByID resolves a config's scope chain from an explicit id.
func (s *Server) configResourceByID(ctx context.Context, cid string) (authz.Resource, error) {
	return s.resolveScopeResource(ctx, "config", cid)
}

// promoteDiffView maps a promote.Diff to its JSON wire shape.
func promoteDiffView(d promote.Diff) map[string]any {
	entries := make([]map[string]any, 0, len(d.Entries))
	for _, e := range d.Entries {
		entries = append(entries, map[string]any{
			"key":          e.Key,
			"status":       string(e.Status),
			"source_value": e.SourceValue,
			"target_value": e.TargetValue,
			"locked":       e.Locked,
		})
	}
	return map[string]any{
		"source_version": d.SourceVersion,
		"target_exists":  d.TargetExists,
		"entries":        entries,
	}
}

// writePromoteError maps promote sentinels to HTTP; else delegates.
func (s *Server) writePromoteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, promote.ErrIllegalStep), errors.Is(err, promote.ErrNoPipeline):
		writeError(w, http.StatusConflict, "pipeline_step_not_allowed", "not the next pipeline step")
	case errors.Is(err, promote.ErrLockedKey):
		writeError(w, http.StatusConflict, "key_locked", "a selected key is locked on the target")
	default:
		s.writeServiceError(w, err)
	}
}

func (s *Server) handlePromotePreview(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	toEnv := r.URL.Query().Get("to_env")
	if from == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "from config id is required")
		return
	}
	if to == "" && toEnv == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "to or to_env is required")
		return
	}
	srcRes, err := s.configResourceByID(r.Context(), from)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	// Preview reveals the source → secret:read on source, audited.
	if !s.authorize(w, r, authz.SecretRead, srcRes, "secret.reveal", "configs/"+from+"/secrets") {
		return
	}

	// Create-mode preview: target env has no config yet. Every source key is an
	// "add". Gate on config:create for the target env (only someone who could
	// create the target may preview-create it). Reveals the SOURCE only.
	if to == "" {
		dstEnvRes, err := s.resolveScopeResource(r.Context(), "environment", toEnv)
		if err != nil {
			s.writeServiceError(w, err)
			return
		}
		if err := s.can(r, authz.ConfigCreate, dstEnvRes); err != nil {
			s.writeAuthzError(w, err)
			return
		}
		diff, err := s.promote.PreviewCreate(r.Context(), from, toEnv, promoteActorUser(r))
		if err != nil {
			s.writePromoteError(w, err)
			return
		}
		if err := s.record(r, "secret.reveal", "configs/"+from+"/secrets", "success", "", "promote-preview-create"); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, promoteDiffView(diff))
		return
	}

	// Existing-target preview: reveals both sides → secret:read on both, audited.
	dstRes, err := s.configResourceByID(r.Context(), to)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.SecretRead, dstRes); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	diff, err := s.promote.Preview(r.Context(), from, to, promoteActorUser(r))
	if err != nil {
		s.writePromoteError(w, err)
		return
	}
	if err := s.record(r, "secret.reveal", "configs/"+from+"/secrets", "success", "", "promote-preview"); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if err := s.record(r, "secret.reveal", "configs/"+to+"/secrets", "success", "", "promote-preview"); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, promoteDiffView(diff))
}

func (s *Server) handlePromoteApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From          string `json:"from_config"`
		To            string `json:"to_config"`
		ToEnv         string `json:"to_env"`
		ToName        string `json:"to_name"`
		Create        bool   `json:"create"`
		SourceVersion int    `json:"source_version"`
		Selections    []struct {
			Key    string `json:"key"`
			Action string `json:"action"`
		} `json:"selections"`
	}
	// Read the raw body first so it can be hashed for idempotency, then unmarshal.
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	if body.From == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "from_config is required")
		return
	}
	srcRes, err := s.configResourceByID(r.Context(), body.From)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.SecretRead, srcRes); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	// Target authz: promote on the target. If creating, resolve by env + require config:create.
	var dstRes authz.Resource
	if body.To != "" {
		if dstRes, err = s.configResourceByID(r.Context(), body.To); err != nil {
			s.writeServiceError(w, err)
			return
		}
	} else {
		if body.ToEnv == "" {
			writeError(w, http.StatusBadRequest, CodeValidation, "to_env is required when creating the target")
			return
		}
		if dstRes, err = s.resolveScopeResource(r.Context(), "environment", body.ToEnv); err != nil {
			s.writeServiceError(w, err)
			return
		}
		if err := s.can(r, authz.ConfigCreate, dstRes); err != nil {
			s.writeAuthzError(w, err)
			return
		}
	}
	if !s.authorize(w, r, authz.SecretPromote, dstRes, "secret.promote", "configs/"+body.To) {
		return
	}

	// Idempotency: only an authorized caller reaches here, so a claim/replay is
	// safe. Scope the key by the authenticated principal so one actor's key can
	// never read another's stored response. The stored response is value-free
	// (target version + applied key NAMES only) — never a secret value.
	idemKey := r.Header.Get("Idempotency-Key")
	var idemActor string
	var idem *store.PromotionIdempotencyRepo
	if idemKey != "" {
		p, _ := PrincipalFrom(r.Context())
		idemActor = p.ID
		sum := sha256.Sum256(bodyBytes)
		reqHash := hex.EncodeToString(sum[:])
		idem = store.NewPromotionIdempotencyRepo(s.st)
		claimed, existing, cerr := idem.Claim(r.Context(), idemKey, idemActor, reqHash)
		if cerr != nil {
			s.writeServiceError(w, cerr)
			return
		}
		if !claimed {
			if existing.RequestHash != reqHash {
				writeError(w, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key reused with a different request")
				return
			}
			if existing.Response == nil {
				writeError(w, http.StatusConflict, "idempotency_in_progress", "a request with this Idempotency-Key is still in progress")
				return
			}
			// Replay the stored response. Re-encode through writeJSON (json.Encoder)
			// rather than writing the raw stored bytes: it emits a consistent
			// application/json body and avoids handing an untrusted-looking byte
			// slice straight to w.Write (the row holds only our own marshaled
			// {target_version, applied, skipped} — key names, never values).
			var replay map[string]any
			if uerr := json.Unmarshal(existing.Response, &replay); uerr != nil {
				s.writeServiceError(w, uerr)
				return
			}
			w.Header().Set("Idempotency-Replayed", "true")
			writeJSON(w, http.StatusOK, replay)
			return
		}
		// Claim won: on any early return below (apply error) we release the claim
		// so a retry with the same key can proceed.
	}

	sels := make([]promote.Selection, 0, len(body.Selections))
	for _, sel := range body.Selections {
		sels = append(sels, promote.Selection{Key: sel.Key, Action: promote.Action(sel.Action)})
	}
	res, err := s.promote.Apply(r.Context(), promote.ApplyRequest{
		SourceConfigID: body.From, TargetConfigID: body.To, TargetEnvID: body.ToEnv, TargetName: body.ToName,
		CreateTarget: body.Create, SourceVersion: body.SourceVersion, Selections: sels, Actor: promoteActorUser(r),
	})
	if err != nil {
		if idemKey != "" {
			_ = idem.Release(r.Context(), idemKey, idemActor) // best-effort; retry may reproceed
		}
		s.writePromoteError(w, err)
		return
	}
	appliedKeys := make([]string, 0, len(res.Applied))
	for _, a := range res.Applied {
		appliedKeys = append(appliedKeys, a.Key)
	}
	if err := s.record(r, "secret.promote", "configs/"+body.To, "success", "", strings.Join(appliedKeys, ",")); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	respBytes, _ := json.Marshal(map[string]any{
		"target_version": res.TargetVersion, "applied": appliedKeys, "skipped": res.Skipped,
	})
	if idemKey != "" {
		_ = idem.Complete(r.Context(), idemKey, idemActor, respBytes) // best-effort; result already applied
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBytes)
}
