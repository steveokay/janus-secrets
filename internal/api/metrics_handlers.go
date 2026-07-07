package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

type configReadsRow struct {
	ConfigID    string `json:"config_id"`
	ConfigName  string `json:"config_name"`
	ProjectName string `json:"project_name,omitempty"`
	Reads       int64  `json:"reads"`
}
type tokenReadsRow struct {
	TokenID   string `json:"token_id"`
	TokenName string `json:"token_name"`
	Reads     int64  `json:"reads"`
}
type reads24hResponse struct {
	Reads24h   int64            `json:"reads_24h"`
	TopConfigs []configReadsRow `json:"top_configs"`
	TopTokens  []tokenReadsRow  `json:"top_tokens"`
}

func toReads24hResponse(m store.Reads24h) reads24hResponse {
	cfgs := make([]configReadsRow, 0, len(m.TopConfigs))
	for _, c := range m.TopConfigs {
		cfgs = append(cfgs, configReadsRow{c.ConfigID, c.ConfigName, c.ProjectName, c.Reads})
	}
	toks := make([]tokenReadsRow, 0, len(m.TopTokens))
	for _, t := range m.TopTokens {
		toks = append(toks, tokenReadsRow{t.TokenID, t.TokenName, t.Reads})
	}
	return reads24hResponse{Reads24h: m.Total, TopConfigs: cfgs, TopTokens: toks}
}

// handleMetricsReads serves instance-wide read counts. Instance AuditRead.
// Not self-audited (a metadata read, consistent with /v1/audit/events).
func (s *Server) handleMetricsReads(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.AuditRead, authz.Instance(), "metrics.reads", "metrics") {
		return
	}
	m, err := store.NewMetricsRepo(s.st).Reads24h(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, toReads24hResponse(m))
}

// handleProjectMetricsReads serves per-project read counts. Project AuditRead.
func (s *Server) handleProjectMetricsReads(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if !s.authorize(w, r, authz.AuditRead, authz.Resource{ProjectID: pid}, "metrics.reads", "projects/"+pid+"/metrics") {
		return
	}
	m, err := store.NewMetricsRepo(s.st).Reads24h(r.Context(), &pid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, toReads24hResponse(m))
}
