package api

import (
	"net/http"
	"time"

	"github.com/steveokay/janus-secrets/internal/authz"
)

// handleReadInsights serves per-key advisory read insights for a config: the
// last per-key reveal time and a fixed-length daily reveal-count sparkline over
// the trailing 30-day window. Value-free — counts and timestamps only, never a
// secret value.
//
// Authorization rides the same secret:read gate as the masked list, and — like
// the masked list — it is NOT self-audited: it reads only audit metadata (key
// names + timestamps + counts), so it emits no audit event of its own.
//
// Response shape:
//
//	{
//	  "window_days": 30,
//	  "keys": {
//	    "DATABASE_URL": { "last_read_at": "2026-07-23T...Z", "daily": [0,0,...,3] },
//	    "API_KEY":      { "last_read_at": null,               "daily": [0,...,0] }
//	  }
//	}
//
// daily[i] is the reveal count for the UTC calendar day (today - (29-i)); index
// 29 (the last element) is today. Keys never revealed per-key are absent from
// "keys" (the UI treats absence as "never read").
func (s *Server) handleReadInsights(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.SecretRead, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	insights, err := s.service.ReadInsights(r.Context(), cid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	keys := make(map[string]any, len(insights))
	for k, ki := range insights {
		entry := map[string]any{"daily": ki.Daily}
		if ki.LastReadAt != nil {
			entry["last_read_at"] = ki.LastReadAt.UTC().Format(time.RFC3339)
		} else {
			entry["last_read_at"] = nil
		}
		keys[k] = entry
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"window_days": s.service.ReadInsightsWindowDays(),
		"keys":        keys,
	})
}
