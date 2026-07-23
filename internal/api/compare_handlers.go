package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
)

// handleConfigCompare serves a value-free key-level comparison of two arbitrary
// configs: GET /v1/configs/{cid}/compare?against={other_cid}.
//
// It resolves both configs' plaintext server-side (the same reveal path the
// promotion preview uses) and returns ONLY booleans + key names + per-side
// origins — never a secret value. Requires SecretRead on BOTH configs,
// authorized independently (deny by default): a caller who cannot read one side
// gets 403 without any of the other side's keys being revealed. Emits ONE
// value-free config.compare audit event (both config paths) because it touches
// secret material server-side even though it returns none.
func (s *Server) handleConfigCompare(w http.ResponseWriter, r *http.Request) {
	cid := chi.URLParam(r, "cid")
	against := r.URL.Query().Get("against")
	if against == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "against config id is required")
		return
	}
	if against == cid {
		writeError(w, http.StatusBadRequest, CodeValidation, "cannot compare a config with itself")
		return
	}

	// Authorize BOTH configs independently, deny by default. The primary side is
	// authorized (and its denial audited) first; only then is the other side's
	// existence/authz even probed, so a forbidden caller never learns the other
	// config's keys.
	resA, err := s.configResourceByID(r.Context(), cid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.SecretRead, resA, "config.compare", comparePath(cid, against)) {
		return
	}
	resB, err := s.configResourceByID(r.Context(), against)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.SecretRead, resB, "config.compare", comparePath(cid, against)) {
		return
	}

	rows, err := s.service.CompareConfigs(r.Context(), cid, against)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}

	// One value-free audit event for the whole compare (both paths in the
	// resource), fail-closed like every other secret-touching read.
	if err := s.record(r, "config.compare", comparePath(cid, against), "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}

	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"key":      row.Key,
			"in_a":     row.InA,
			"in_b":     row.InB,
			"differs":  row.Differs,
			"origin_a": row.OriginA,
			"origin_b": row.OriginB,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config_a": cid,
		"config_b": against,
		"entries":  out,
	})
}

// comparePath builds the audit resource path for a compare of a against b.
func comparePath(a, b string) string {
	return "configs/" + a + "/compare/" + b
}
