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
	if !s.authorize(w, r, authz.UserManage, authz.Instance(), "user.create", "users") {
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
	if err := s.record(r, "user.create", "users/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
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
	pp, err := parsePageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		return
	}
	users, next, err := s.auth.ListUsersPage(r.Context(), pp.limit, pp.after)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	var nextTok *string
	if next != nil {
		t := encodeCursor(next.CreatedAt, next.ID)
		nextTok = &t
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users, "next_cursor": nextTok})
}

func (s *Server) handleUserDisable(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.UserManage, authz.Instance(), "user.disable", "users") {
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
	if err := s.record(r, "user.disable", "users/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUserUnlock clears an account's lockout state (an admin early-unlock).
// Mirrors handleUserDisable: instance-scoped UserManage, cannot target self.
func (s *Server) handleUserUnlock(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.UserManage, authz.Instance(), "user.unlock", "users") {
		return
	}
	id := chi.URLParam(r, "id")
	p, _ := PrincipalFrom(r.Context())
	if id == p.ID {
		// A caller who could reach this endpoint is not themselves locked out
		// (they authenticated), so self-unlock is meaningless — reject for
		// symmetry with disable and to keep the surface tight.
		writeError(w, http.StatusConflict, CodeValidation, "cannot unlock yourself")
		return
	}
	if err := s.auth.AdminUnlock(r.Context(), id); err != nil {
		s.writeAuthError(w, err) // ErrNotFound → 404
		return
	}
	if err := s.record(r, "user.unlock", "users/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
