package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/dynamic"
)

type dynamicConfigReq struct {
	AdminDSN             string `json:"admin_dsn,omitempty"`
	CreationStatements   string `json:"creation_statements,omitempty"`
	RevocationStatements string `json:"revocation_statements,omitempty"`
	RenewStatements      string `json:"renew_statements,omitempty"`
}

func (c dynamicConfigReq) toEngine() dynamic.RoleConfig {
	return dynamic.RoleConfig{
		AdminDSN: c.AdminDSN, CreationStatements: c.CreationStatements,
		RevocationStatements: c.RevocationStatements, RenewStatements: c.RenewStatements,
	}
}

type createRoleReq struct {
	ConfigID          string           `json:"config_id"`
	Name              string           `json:"name"`
	DefaultTTLSeconds int64            `json:"default_ttl_seconds"`
	MaxTTLSeconds     int64            `json:"max_ttl_seconds"`
	Config            dynamicConfigReq `json:"config"`
}

type updateRoleReq struct {
	DefaultTTLSeconds *int64            `json:"default_ttl_seconds"`
	MaxTTLSeconds     *int64            `json:"max_ttl_seconds"`
	Config            *dynamicConfigReq `json:"config"`
}

type roleViewJSON struct {
	ID                string `json:"id"`
	ProjectID         string `json:"project_id"`
	ConfigID          string `json:"config_id"`
	Name              string `json:"name"`
	DefaultTTLSeconds int64  `json:"default_ttl_seconds"`
	MaxTTLSeconds     int64  `json:"max_ttl_seconds"`
	CreatedAt         string `json:"created_at"`
}

func toRoleViewJSON(v dynamic.RoleView) roleViewJSON {
	return roleViewJSON{
		ID: v.ID, ProjectID: v.ProjectID, ConfigID: v.ConfigID, Name: v.Name,
		DefaultTTLSeconds: v.DefaultTTLSeconds, MaxTTLSeconds: v.MaxTTLSeconds,
		CreatedAt: v.CreatedAt.UTC().Format(time.RFC3339),
	}
}

type leaseViewJSON struct {
	ID           string  `json:"id"`
	RoleID       string  `json:"role_id"`
	Status       string  `json:"status"`
	DBUsername   string  `json:"db_username"`
	ExpiresAt    string  `json:"expires_at"`
	MaxExpiresAt string  `json:"max_expires_at"`
	RenewedAt    *string `json:"renewed_at,omitempty"`
	CreatedAt    string  `json:"created_at"`
}

func toLeaseViewJSON(v dynamic.LeaseView) leaseViewJSON {
	out := leaseViewJSON{
		ID: v.ID, RoleID: v.RoleID, Status: v.Status, DBUsername: v.DBUsername,
		ExpiresAt: v.ExpiresAt.UTC().Format(time.RFC3339), MaxExpiresAt: v.MaxExpiresAt.UTC().Format(time.RFC3339),
		CreatedAt: v.CreatedAt.UTC().Format(time.RFC3339),
	}
	if v.RenewedAt != nil {
		s := v.RenewedAt.UTC().Format(time.RFC3339)
		out.RenewedAt = &s
	}
	return out
}

func (s *Server) writeDynamicErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, dynamic.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeDynamicNotFound, "dynamic role or lease not found")
	case errors.Is(err, dynamic.ErrExists):
		writeError(w, http.StatusConflict, "conflict", "a dynamic role already exists for this config and name")
	case errors.Is(err, dynamic.ErrInvalidConfig):
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid dynamic role configuration")
	case errors.Is(err, dynamic.ErrNotRenewable):
		writeError(w, http.StatusConflict, "conflict", "lease is not active")
	case errors.Is(err, dynamic.ErrSealed):
		writeError(w, http.StatusServiceUnavailable, CodeSealed, "server is sealed")
	default:
		s.writeServiceError(w, err)
	}
}

func (s *Server) handleDynamicRoleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ConfigID == "" || req.Name == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "config_id, name, ttls, config are required")
		return
	}
	res, err := s.resolveScopeResource(r.Context(), "config", req.ConfigID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.DynamicManage, res, "dynamic.role.create", "configs/"+req.ConfigID) {
		return
	}
	v, err := s.dynamic.CreateRole(r.Context(), dynamic.RoleInput{
		ConfigID: req.ConfigID, Name: req.Name,
		DefaultTTLSeconds: req.DefaultTTLSeconds, MaxTTLSeconds: req.MaxTTLSeconds,
		Config: req.Config.toEngine(),
	}, principalName(r))
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if err := s.record(r, "dynamic.role.create", "dynamic/roles/"+v.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, toRoleViewJSON(v))
}

func (s *Server) handleDynamicRoleList(w http.ResponseWriter, r *http.Request) {
	configID := r.URL.Query().Get("config_id")
	if configID == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "config_id is required")
		return
	}
	res, err := s.resolveScopeResource(r.Context(), "config", configID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.DynamicManage, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	vs, err := s.dynamic.ListRolesByConfig(r.Context(), configID)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	out := make([]roleViewJSON, 0, len(vs))
	for _, v := range vs {
		out = append(out, toRoleViewJSON(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": out})
}

func (s *Server) dynamicRoleResource(r *http.Request) (authz.Resource, dynamic.RoleView, error) {
	id := chi.URLParam(r, "id")
	v, err := s.dynamic.GetRole(r.Context(), id)
	if err != nil {
		return authz.Resource{}, dynamic.RoleView{}, err
	}
	res, err := s.resolveScopeResource(r.Context(), "config", v.ConfigID)
	return res, v, err
}

func (s *Server) handleDynamicRoleGet(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.dynamicRoleResource(r)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if err := s.can(r, authz.DynamicManage, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toRoleViewJSON(v))
}

func (s *Server) handleDynamicRoleUpdate(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.dynamicRoleResource(r)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.DynamicManage, res, "dynamic.role.update", "dynamic/roles/"+v.ID) {
		return
	}
	var req updateRoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	var cfg *dynamic.RoleConfig
	if req.Config != nil {
		c := req.Config.toEngine()
		cfg = &c
	}
	updated, err := s.dynamic.UpdateRole(r.Context(), v.ID, req.DefaultTTLSeconds, req.MaxTTLSeconds, cfg)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if err := s.record(r, "dynamic.role.update", "dynamic/roles/"+v.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, toRoleViewJSON(updated))
}

func (s *Server) handleDynamicRoleDelete(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.dynamicRoleResource(r)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.DynamicManage, res, "dynamic.role.delete", "dynamic/roles/"+v.ID) {
		return
	}
	if err := s.dynamic.DeleteRole(r.Context(), v.ID); err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if err := s.record(r, "dynamic.role.delete", "dynamic/roles/"+v.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) handleDynamicIssue(w http.ResponseWriter, r *http.Request) {
	res, v, err := s.dynamicRoleResource(r)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.DynamicIssue, res, "dynamic.creds.issue", "dynamic/roles/"+v.ID) {
		return
	}
	// The engine writes its own dynamic.creds.issue audit event (system actor).
	creds, err := s.dynamic.IssueCreds(r.Context(), v.ID, principalName(r))
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"lease_id":   creds.LeaseID,
		"username":   creds.Username,
		"password":   creds.Password,
		"expires_at": creds.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleDynamicLeaseList(w http.ResponseWriter, r *http.Request) {
	roleID := r.URL.Query().Get("role_id")
	if roleID == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "role_id is required")
		return
	}
	v, err := s.dynamic.GetRole(r.Context(), roleID)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	res, err := s.resolveScopeResource(r.Context(), "config", v.ConfigID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.DynamicIssue, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	vs, err := s.dynamic.ListLeasesByRole(r.Context(), roleID)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	out := make([]leaseViewJSON, 0, len(vs))
	for _, lv := range vs {
		out = append(out, toLeaseViewJSON(lv))
	}
	writeJSON(w, http.StatusOK, map[string]any{"leases": out})
}

func (s *Server) dynamicLeaseResource(r *http.Request) (authz.Resource, dynamic.LeaseView, error) {
	id := chi.URLParam(r, "id")
	lv, err := s.dynamic.GetLease(r.Context(), id)
	if err != nil {
		return authz.Resource{}, dynamic.LeaseView{}, err
	}
	role, err := s.dynamic.GetRole(r.Context(), lv.RoleID)
	if err != nil {
		return authz.Resource{}, dynamic.LeaseView{}, err
	}
	res, err := s.resolveScopeResource(r.Context(), "config", role.ConfigID)
	return res, lv, err
}

func (s *Server) handleDynamicLeaseRenew(w http.ResponseWriter, r *http.Request) {
	res, lv, err := s.dynamicLeaseResource(r)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.DynamicIssue, res, "dynamic.lease.renew", "dynamic/leases/"+lv.ID) {
		return
	}
	updated, err := s.dynamic.RenewLease(r.Context(), lv.ID)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toLeaseViewJSON(updated))
}

func (s *Server) handleDynamicLeaseRevoke(w http.ResponseWriter, r *http.Request) {
	res, lv, err := s.dynamicLeaseResource(r)
	if err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	if !s.authorize(w, r, authz.DynamicIssue, res, "dynamic.lease.revoke", "dynamic/leases/"+lv.ID) {
		return
	}
	if err := s.dynamic.RevokeLease(r.Context(), lv.ID); err != nil {
		s.writeDynamicErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}
