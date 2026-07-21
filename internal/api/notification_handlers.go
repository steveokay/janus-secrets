package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/notification"
)

// writeNotificationError maps notification sentinels to the HTTP envelope.
func (s *Server) writeNotificationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, notification.ErrValidation):
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid notification channel")
	case errors.Is(err, notification.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "notification channel not found")
	case errors.Is(err, notification.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "a channel with that name already exists")
	default:
		s.writeServiceError(w, err)
	}
}

type createNotificationReq struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Events  []string `json:"events"`
	URL     string   `json:"url"`
	HMACKey string   `json:"hmac_key"`
}

func (s *Server) handleNotificationCreate(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.NotificationManage, authz.Instance(), "notification.channel.create", "notification/channels") {
		return
	}
	var req createNotificationReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid request body")
		return
	}
	v, err := s.notification.CreateChannel(r.Context(), notification.ChannelInput{
		Name: req.Name, Type: req.Type, Events: req.Events, URL: req.URL, HMACKey: req.HMACKey,
		CreatedBy: principalName(r),
	})
	if err != nil {
		s.writeNotificationError(w, err)
		return
	}
	if err := s.record(r, "notification.channel.create", "notification/channels/"+v.ID, "success", "", "type="+v.Type); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (s *Server) handleNotificationList(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.NotificationManage, authz.Instance(), "notification.channels.list", "notification/channels") {
		return
	}
	vs, err := s.notification.ListChannels(r.Context())
	if err != nil {
		s.writeNotificationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": vs})
}

func (s *Server) handleNotificationGet(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.NotificationManage, authz.Instance(), "notification.channels.get", "notification/channels") {
		return
	}
	v, err := s.notification.GetChannel(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.writeNotificationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type updateNotificationReq struct {
	Enabled *bool     `json:"enabled"`
	Events  *[]string `json:"events"`
	URL     *string   `json:"url"`
	HMACKey string    `json:"hmac_key"`
}

func (s *Server) handleNotificationUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.NotificationManage, authz.Instance(), "notification.channel.update", "notification/channels") {
		return
	}
	id := chi.URLParam(r, "id")
	var req updateNotificationReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid request body")
		return
	}
	urlSet := req.URL != nil
	u := ""
	if urlSet {
		u = *req.URL
	}
	v, err := s.notification.UpdateChannel(r.Context(), id, req.Enabled, req.Events, urlSet, u, req.HMACKey)
	if err != nil {
		s.writeNotificationError(w, err)
		return
	}
	if err := s.record(r, "notification.channel.update", "notification/channels/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleNotificationDelete(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.NotificationManage, authz.Instance(), "notification.channel.delete", "notification/channels") {
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.notification.DeleteChannel(r.Context(), id); err != nil {
		s.writeNotificationError(w, err)
		return
	}
	if err := s.record(r, "notification.channel.delete", "notification/channels/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleNotificationTest(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.NotificationManage, authz.Instance(), "notification.channel.test", "notification/channels") {
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.notification.TestChannel(r.Context(), id); err != nil {
		if errors.Is(err, notification.ErrNotFound) {
			s.writeNotificationError(w, err)
			return
		}
		// A delivery failure is a 502-style outcome, not a server fault; report it
		// as a failed test with a sanitized reason (never the URL).
		if err := s.record(r, "notification.channel.test", "notification/channels/"+id, "failure", "", ""); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		writeError(w, http.StatusBadGateway, "delivery_failed", "test delivery failed")
		return
	}
	if err := s.record(r, "notification.channel.test", "notification/channels/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"delivered": true})
}

func (s *Server) handleNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.NotificationManage, authz.Instance(), "notification.channels.deliveries", "notification/channels") {
		return
	}
	ds, err := s.notification.ListDeliveries(r.Context(), chi.URLParam(r, "id"), 50)
	if err != nil {
		s.writeNotificationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": ds})
}
