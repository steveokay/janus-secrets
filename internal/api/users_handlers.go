package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
)

type createUserRequest struct {
	Email string `json:"email"`
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	if err := s.can(r, authz.UserManage, authz.Instance()); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "email is required")
		return
	}
	id, password, err := s.auth.CreateUser(r.Context(), req.Email)
	if err != nil {
		s.writeAuthError(w, err) // ErrValidation on duplicate email → 400
		return
	}
	// One-time credential; same pattern as the bootstrap admin.
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "email": req.Email, "password": password})
}

func (s *Server) handleUserList(w http.ResponseWriter, r *http.Request) {
	if err := s.can(r, authz.UserManage, authz.Instance()); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	users, err := s.auth.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) handleUserDisable(w http.ResponseWriter, r *http.Request) {
	if err := s.can(r, authz.UserManage, authz.Instance()); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	p, _ := PrincipalFrom(r.Context())
	if id == p.ID {
		writeError(w, http.StatusConflict, CodeValidation, "cannot disable yourself")
		return
	}
	// Never-lock-out: don't disable the last instance owner (fail closed).
	last, err := s.isLastInstanceOwner(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if last {
		writeError(w, http.StatusConflict, CodeValidation, "cannot disable the last instance owner")
		return
	}
	if err := s.auth.DisableUser(r.Context(), id); err != nil {
		s.writeAuthError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
