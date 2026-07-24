package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/rotation"
)

type rotationConfigReq struct {
	AdminDSN    string `json:"admin_dsn,omitempty"`
	Role        string `json:"role,omitempty"`
	PasswordLen int    `json:"password_len,omitempty"`
	URL         string `json:"url,omitempty"`
	HMACKey     string `json:"hmac_key,omitempty"`
	// mysql
	MySQLAddr          string `json:"mysql_addr,omitempty"`
	MySQLAdminUser     string `json:"mysql_admin_user,omitempty"`
	MySQLAdminPassword string `json:"mysql_admin_password,omitempty"`
	MySQLDBName        string `json:"mysql_db_name,omitempty"`
	MySQLTLS           string `json:"mysql_tls,omitempty"`
	MySQLUser          string `json:"mysql_user,omitempty"`
	MySQLHost          string `json:"mysql_host,omitempty"`
	// redis
	RedisAddr          string `json:"redis_addr,omitempty"`
	RedisAdminUser     string `json:"redis_admin_user,omitempty"`
	RedisAdminPassword string `json:"redis_admin_password,omitempty"`
	RedisTLS           bool   `json:"redis_tls,omitempty"`
	RedisSkipVerify    bool   `json:"redis_skip_verify,omitempty"`
	RedisUser          string `json:"redis_user,omitempty"`
	RedisRules         string `json:"redis_rules,omitempty"`
	// notify
	NotifyURL     string `json:"notify_url,omitempty"`
	NotifyHMACKey string `json:"notify_hmac_key,omitempty"`
}

func (rc rotationConfigReq) toEngine() rotation.PolicyConfig {
	return rotation.PolicyConfig{
		AdminDSN: rc.AdminDSN, Role: rc.Role, PasswordLen: rc.PasswordLen,
		URL: rc.URL, HMACKey: rc.HMACKey,
		MySQLAddr: rc.MySQLAddr, MySQLAdminUser: rc.MySQLAdminUser, MySQLAdminPassword: rc.MySQLAdminPassword,
		MySQLDBName: rc.MySQLDBName, MySQLTLS: rc.MySQLTLS, MySQLUser: rc.MySQLUser, MySQLHost: rc.MySQLHost,
		RedisAddr: rc.RedisAddr, RedisAdminUser: rc.RedisAdminUser, RedisAdminPassword: rc.RedisAdminPassword,
		RedisTLS: rc.RedisTLS, RedisSkipVerify: rc.RedisSkipVerify, RedisUser: rc.RedisUser, RedisRules: rc.RedisRules,
		NotifyURL: rc.NotifyURL, NotifyHMACKey: rc.NotifyHMACKey,
	}
}

type createRotationReq struct {
	ConfigID        string            `json:"config_id"`
	SecretKey       string            `json:"secret_key"`
	Type            string            `json:"type"`
	IntervalSeconds int64             `json:"interval_seconds"`
	Config          rotationConfigReq `json:"config"`
}

type updateRotationReq struct {
	IntervalSeconds *int64             `json:"interval_seconds"`
	Status          *string            `json:"status"`
	Config          *rotationConfigReq `json:"config"`
}

// rotationView is the masked JSON projection (no secrets/DSN/keys/value).
type rotationView struct {
	ID                string  `json:"id"`
	ProjectID         string  `json:"project_id"`
	ConfigID          string  `json:"config_id"`
	SecretKey         string  `json:"secret_key"`
	Type              string  `json:"type"`
	IntervalSeconds   int64   `json:"interval_seconds"`
	Status            string  `json:"status"`
	FailureCount      int     `json:"failure_count"`
	LastError         *string `json:"last_error,omitempty"`
	NextRotationAt    string  `json:"next_rotation_at"`
	LastRotatedAt     *string `json:"last_rotated_at,omitempty"`
	LastConfigVersion *int    `json:"last_config_version,omitempty"`
	CreatedAt         string  `json:"created_at"`
}

func toRotationView(v rotation.PolicyView) rotationView {
	out := rotationView{
		ID: v.ID, ProjectID: v.ProjectID, ConfigID: v.ConfigID, SecretKey: v.SecretKey,
		Type: v.Type, IntervalSeconds: v.IntervalSeconds, Status: v.Status,
		FailureCount: v.FailureCount, LastError: v.LastError,
		NextRotationAt: v.NextRotationAt.UTC().Format(time.RFC3339), LastConfigVersion: v.LastConfigVersion,
		CreatedAt: v.CreatedAt.UTC().Format(time.RFC3339),
	}
	if v.LastRotatedAt != nil {
		s := v.LastRotatedAt.UTC().Format(time.RFC3339)
		out.LastRotatedAt = &s
	}
	return out
}

// writeRotationErr maps engine sentinels to the JSON envelope.
func (s *Server) writeRotationErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, rotation.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeRotationNotFound, "rotation policy not found")
	case errors.Is(err, rotation.ErrExists):
		writeError(w, http.StatusConflict, "conflict", "a rotation policy already exists for this config and key")
	case errors.Is(err, rotation.ErrInvalidType), errors.Is(err, rotation.ErrInvalidConfig):
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid rotation policy configuration")
	case errors.Is(err, rotation.ErrSealed):
		writeError(w, http.StatusServiceUnavailable, CodeSealed, "server is sealed")
	default:
		s.writeServiceError(w, err)
	}
}

// principalName returns a non-secret display id for created_by.
func principalName(r *http.Request) string {
	p, _ := PrincipalFrom(r.Context())
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}

func (s *Server) handleRotationCreate(w http.ResponseWriter, r *http.Request) {
	var req createRotationReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ConfigID == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "config_id, secret_key, type are required")
		return
	}
	res, err := s.resolveScopeResource(r.Context(), "config", req.ConfigID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.RotationManage, res, "rotation.create", "configs/"+req.ConfigID) {
		return
	}
	v, err := s.rotation.Create(r.Context(), rotation.PolicyInput{
		ConfigID: req.ConfigID, SecretKey: req.SecretKey, Type: req.Type,
		IntervalSeconds: req.IntervalSeconds, Config: req.Config.toEngine(),
	}, principalName(r))
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	if err := s.record(r, "rotation.create", "rotation/"+v.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, toRotationView(v))
}

func (s *Server) handleRotationList(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "project_id is required")
		return
	}
	if err := s.can(r, authz.RotationManage, authz.Resource{ProjectID: projectID}); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	vs, err := s.rotation.ListByProject(r.Context(), projectID)
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	out := make([]rotationView, 0, len(vs))
	for _, v := range vs {
		out = append(out, toRotationView(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": out})
}

// rotationResource loads a policy and returns its project-scoped authz resource.
func (s *Server) rotationResource(r *http.Request) (authz.Resource, rotation.PolicyView, error) {
	id := chi.URLParam(r, "id")
	v, err := s.rotation.Get(r.Context(), id)
	if err != nil {
		return authz.Resource{}, rotation.PolicyView{}, err
	}
	res, err := s.resolveScopeResource(r.Context(), "config", v.ConfigID)
	return res, v, err
}

func (s *Server) handleRotationGet(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.rotationResource(r)
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	if err := s.can(r, authz.RotationManage, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toRotationView(v))
}

func (s *Server) handleRotationUpdate(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.rotationResource(r)
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.RotationManage, res, "rotation.update", "rotation/"+v.ID) {
		return
	}
	var req updateRotationReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	var cfg *rotation.PolicyConfig
	if req.Config != nil {
		c := req.Config.toEngine()
		cfg = &c
	}
	updated, err := s.rotation.Update(r.Context(), v.ID, req.IntervalSeconds, req.Status, cfg)
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	if err := s.record(r, "rotation.update", "rotation/"+v.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, toRotationView(updated))
}

func (s *Server) handleRotationDelete(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.rotationResource(r)
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.RotationManage, res, "rotation.delete", "rotation/"+v.ID) {
		return
	}
	if err := s.rotation.Delete(r.Context(), v.ID); err != nil {
		s.writeRotationErr(w, err)
		return
	}
	if err := s.record(r, "rotation.delete", "rotation/"+v.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

type rotationRunsResponse struct {
	Runs       []rotationRunDTO `json:"runs"`
	NextCursor *int64           `json:"next_cursor"`
}
type rotationRunDTO struct {
	ID            int64   `json:"id"`
	StartedAt     string  `json:"started_at"`
	EndedAt       string  `json:"ended_at"`
	Status        string  `json:"status"`
	Error         *string `json:"error,omitempty"`
	ConfigVersion *int    `json:"config_version,omitempty"`
	AttemptNum    int     `json:"attempt_num"`
}

func (s *Server) handleRotationRuns(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.rotationResource(r)
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	if err := s.can(r, authz.RotationManage, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	limit, cursor, ok := parseRunsPaging(w, r)
	if !ok {
		return
	}
	runs, err := s.rotation.ListRuns(r.Context(), v.ID, cursor, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	out := make([]rotationRunDTO, 0, len(runs))
	for _, x := range runs {
		out = append(out, rotationRunDTO{
			ID: x.ID, StartedAt: x.StartedAt.UTC().Format(time.RFC3339), EndedAt: x.EndedAt.UTC().Format(time.RFC3339),
			Status: x.Status, Error: x.Error, ConfigVersion: x.ConfigVersion, AttemptNum: x.AttemptNum,
		})
	}
	var next *int64
	if len(runs) == limit && limit > 0 {
		last := runs[len(runs)-1].ID
		next = &last
	}
	writeJSON(w, http.StatusOK, rotationRunsResponse{Runs: out, NextCursor: next})
}

// parseRunsPaging reads limit (default 50, 1-100) and cursor (int64 >= 0, default 0),
// writing a 400 and returning ok=false on bad input. Shared by both runs handlers.
func parseRunsPaging(w http.ResponseWriter, r *http.Request) (limit int, cursor int64, ok bool) {
	limit = 50
	if val := r.URL.Query().Get("limit"); val != "" {
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 || n > 100 {
			writeError(w, http.StatusBadRequest, CodeValidation, "limit must be 1-100")
			return 0, 0, false
		}
		limit = n
	}
	if val := r.URL.Query().Get("cursor"); val != "" {
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, CodeValidation, "cursor must be a non-negative integer")
			return 0, 0, false
		}
		cursor = n
	}
	return limit, cursor, true
}

func (s *Server) handleRotationRotateNow(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.rotationResource(r)
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.RotationManage, res, "rotation.rotate", "rotation/"+v.ID) {
		return
	}
	// The engine writes its own rotation.rotate audit event (system actor).
	ver, err := s.rotation.RotateNow(r.Context(), v.ID)
	if err != nil {
		s.writeRotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rotated": true, "config_version": ver})
}
