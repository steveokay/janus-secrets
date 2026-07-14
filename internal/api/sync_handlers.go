package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/secretsync"
)

type syncCredsReq struct {
	PAT    string `json:"pat,omitempty"`
	APIURL string `json:"api_url,omitempty"`
	CACert string `json:"ca_cert,omitempty"`
	Token  string `json:"token,omitempty"`
}

func (c syncCredsReq) toEngine() secretsync.Creds {
	return secretsync.Creds{PAT: c.PAT, APIURL: c.APIURL, CACert: c.CACert, Token: c.Token}
}

type syncAddrReq struct {
	Owner       string `json:"owner,omitempty"`
	Repo        string `json:"repo,omitempty"`
	Environment string `json:"environment,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	SecretName  string `json:"secret_name,omitempty"`
}

func (a syncAddrReq) toEngine() secretsync.Addr {
	return secretsync.Addr{
		Owner: a.Owner, Repo: a.Repo, Environment: a.Environment,
		Namespace: a.Namespace, SecretName: a.SecretName,
	}
}

type createSyncReq struct {
	ConfigID        string       `json:"config_id"`
	Provider        string       `json:"provider"`
	Prune           *bool        `json:"prune"` // nil → default true
	IntervalSeconds int64        `json:"interval_seconds"`
	Addr            syncAddrReq  `json:"addr"`
	Creds           syncCredsReq `json:"creds"`
}

type updateSyncReq struct {
	IntervalSeconds *int64        `json:"interval_seconds"`
	Prune           *bool         `json:"prune"`
	Status          *string       `json:"status"`
	Addr            *syncAddrReq  `json:"addr"`
	Creds           *syncCredsReq `json:"creds"`
}

// syncView is the masked JSON projection (no creds/fingerprint).
type syncView struct {
	ID              string      `json:"id"`
	ProjectID       string      `json:"project_id"`
	ConfigID        string      `json:"config_id"`
	Provider        string      `json:"provider"`
	Prune           bool        `json:"prune"`
	IntervalSeconds int64       `json:"interval_seconds"`
	Addr            syncAddrReq `json:"addr"`
	Status          string      `json:"status"`
	FailureCount    int         `json:"failure_count"`
	LastError       *string     `json:"last_error,omitempty"`
	NextSyncAt      string      `json:"next_sync_at"`
	LastSyncedAt    *string     `json:"last_synced_at,omitempty"`
	ManagedKeys     []string    `json:"managed_keys"`
	CreatedAt       string      `json:"created_at"`
}

func toSyncView(v secretsync.TargetView) syncView {
	keys := v.ManagedKeys
	if keys == nil {
		keys = []string{}
	}
	out := syncView{
		ID: v.ID, ProjectID: v.ProjectID, ConfigID: v.ConfigID, Provider: v.Provider,
		Prune: v.Prune, IntervalSeconds: v.IntervalSeconds, Status: v.Status,
		FailureCount: v.FailureCount, LastError: v.LastError,
		NextSyncAt:  v.NextSyncAt.UTC().Format(time.RFC3339),
		ManagedKeys: keys,
		CreatedAt:   v.CreatedAt.UTC().Format(time.RFC3339),
		Addr: syncAddrReq{
			Owner: v.Addr.Owner, Repo: v.Addr.Repo, Environment: v.Addr.Environment,
			Namespace: v.Addr.Namespace, SecretName: v.Addr.SecretName,
		},
	}
	if v.LastSyncedAt != nil {
		s := v.LastSyncedAt.UTC().Format(time.RFC3339)
		out.LastSyncedAt = &s
	}
	return out
}

// writeSyncErr maps engine sentinels to the JSON envelope.
func (s *Server) writeSyncErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, secretsync.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeSyncNotFound, "sync target not found")
	case errors.Is(err, secretsync.ErrExists):
		writeError(w, http.StatusConflict, "conflict", "a sync target already exists for this config, provider, and destination")
	case errors.Is(err, secretsync.ErrInvalidType), errors.Is(err, secretsync.ErrInvalidConfig):
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid sync target configuration")
	case errors.Is(err, secretsync.ErrSealed):
		writeError(w, http.StatusServiceUnavailable, CodeSealed, "server is sealed")
	default:
		s.writeServiceError(w, err)
	}
}

func (s *Server) handleSyncCreate(w http.ResponseWriter, r *http.Request) {
	var req createSyncReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ConfigID == "" || req.Provider == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "config_id and provider are required")
		return
	}
	res, err := s.resolveScopeResource(r.Context(), "config", req.ConfigID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.SyncManage, res, "sync.create", "configs/"+req.ConfigID) {
		return
	}
	prune := true
	if req.Prune != nil {
		prune = *req.Prune
	}
	v, err := s.sync.Create(r.Context(), secretsync.TargetInput{
		ConfigID: req.ConfigID, Provider: req.Provider, Prune: prune,
		IntervalSeconds: req.IntervalSeconds, Addr: req.Addr.toEngine(), Creds: req.Creds.toEngine(),
	}, principalName(r))
	if err != nil {
		s.writeSyncErr(w, err)
		return
	}
	if err := s.record(r, "sync.create", "sync/"+v.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, toSyncView(v))
}

func (s *Server) handleSyncList(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "project_id is required")
		return
	}
	if err := s.can(r, authz.SyncManage, authz.Resource{ProjectID: projectID}); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	vs, err := s.sync.ListByProject(r.Context(), projectID)
	if err != nil {
		s.writeSyncErr(w, err)
		return
	}
	out := make([]syncView, 0, len(vs))
	for _, v := range vs {
		out = append(out, toSyncView(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": out})
}

// syncResource loads a target and returns its project-scoped authz resource.
func (s *Server) syncResource(r *http.Request) (authz.Resource, secretsync.TargetView, error) {
	id := chi.URLParam(r, "id")
	v, err := s.sync.Get(r.Context(), id)
	if err != nil {
		return authz.Resource{}, secretsync.TargetView{}, err
	}
	res, err := s.resolveScopeResource(r.Context(), "config", v.ConfigID)
	return res, v, err
}

func (s *Server) handleSyncGet(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.syncResource(r)
	if err != nil {
		s.writeSyncErr(w, err)
		return
	}
	if err := s.can(r, authz.SyncManage, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSyncView(v))
}

func (s *Server) handleSyncUpdate(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.syncResource(r)
	if err != nil {
		s.writeSyncErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.SyncManage, res, "sync.update", "sync/"+v.ID) {
		return
	}
	var req updateSyncReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	var addr *secretsync.Addr
	if req.Addr != nil {
		a := req.Addr.toEngine()
		addr = &a
	}
	var creds *secretsync.Creds
	if req.Creds != nil {
		c := req.Creds.toEngine()
		creds = &c
	}
	updated, err := s.sync.Update(r.Context(), v.ID, req.IntervalSeconds, req.Prune, req.Status, creds, addr)
	if err != nil {
		s.writeSyncErr(w, err)
		return
	}
	if err := s.record(r, "sync.update", "sync/"+v.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, toSyncView(updated))
}

func (s *Server) handleSyncDelete(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.syncResource(r)
	if err != nil {
		s.writeSyncErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.SyncManage, res, "sync.delete", "sync/"+v.ID) {
		return
	}
	if err := s.sync.Delete(r.Context(), v.ID); err != nil {
		s.writeSyncErr(w, err)
		return
	}
	if err := s.record(r, "sync.delete", "sync/"+v.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

type syncRunsResponse struct {
	Runs       []syncRunDTO `json:"runs"`
	NextCursor *int64       `json:"next_cursor"`
}
type syncRunDTO struct {
	ID            int64   `json:"id"`
	StartedAt     string  `json:"started_at"`
	EndedAt       string  `json:"ended_at"`
	Status        string  `json:"status"`
	Error         *string `json:"error,omitempty"`
	ConfigVersion *int    `json:"config_version,omitempty"`
	KeysCount     int     `json:"keys_count"`
	AttemptNum    int     `json:"attempt_num"`
}

func (s *Server) handleSyncRuns(w http.ResponseWriter, r *http.Request) {
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
	runs, err := s.sync.ListRuns(r.Context(), v.ID, cursor, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	out := make([]syncRunDTO, 0, len(runs))
	for _, x := range runs {
		out = append(out, syncRunDTO{
			ID: x.ID, StartedAt: x.StartedAt.UTC().Format(time.RFC3339), EndedAt: x.EndedAt.UTC().Format(time.RFC3339),
			Status: x.Status, Error: x.Error, ConfigVersion: x.ConfigVersion, KeysCount: x.KeysCount, AttemptNum: x.AttemptNum,
		})
	}
	var next *int64
	if len(runs) == limit && limit > 0 {
		last := runs[len(runs)-1].ID
		next = &last
	}
	writeJSON(w, http.StatusOK, syncRunsResponse{Runs: out, NextCursor: next})
}

func (s *Server) handleSyncNow(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.syncResource(r)
	if err != nil {
		s.writeSyncErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.SyncManage, res, "sync.sync", "sync/"+v.ID) {
		return
	}
	// The engine writes its own sync.reconcile audit event (system actor).
	if err := s.sync.SyncNow(r.Context(), v.ID); err != nil {
		s.writeSyncErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"synced": true})
}
