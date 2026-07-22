package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

// Search bounds. The store is asked for up to searchStoreCap candidates so that
// authz filtering (which silently drops configs the caller can't read) still
// has enough survivors to fill a page; at most searchMaxResults are returned.
const (
	searchStoreCap    = 200
	searchMaxResults  = 50
	searchMinQueryLen = 2
)

// searchKeyResult is one enriched key hit for navigation. It carries NO secret
// value — key names + structural ids/names are metadata.
type searchKeyResult struct {
	Key             string `json:"key"`
	ProjectID       string `json:"project_id"`
	ProjectName     string `json:"project_name"`
	ProjectSlug     string `json:"project_slug"`
	EnvironmentID   string `json:"environment_id"`
	EnvironmentSlug string `json:"environment_slug"`
	ConfigID        string `json:"config_id"`
	ConfigName      string `json:"config_name"`
}

type searchKeysResponse struct {
	Results   []searchKeyResult `json:"results"`
	Truncated bool              `json:"truncated"`
}

// configDisplay is the per-config authz decision + display metadata, cached
// within a single request so a config with many matching keys is resolved once.
type configDisplay struct {
	allowed         bool
	projectID       string
	projectName     string
	projectSlug     string
	environmentID   string
	environmentSlug string
	configName      string
}

// handleSearchKeys serves GET /v1/search/keys?q=<substr>&limit=<n>: a
// cross-config search over secret KEY NAMES (never values), deny-by-default.
//
// Any authenticated principal may call it; results are authz-filtered per
// config: a config the caller cannot SecretRead (or that errors during
// resolution) is silently dropped — no existence oracle, no audit event.
// This is a metadata list view (per CLAUDE.md, masked list views emit no audit
// event and no secret value ever enters the query, response, or logs).
func (s *Server) handleSearchKeys(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < searchMinQueryLen {
		writeError(w, http.StatusBadRequest, CodeValidation, "q must be at least 2 characters")
		return
	}

	limit := searchMaxResults
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, CodeValidation, "limit must be a positive integer")
			return
		}
		if n < limit {
			limit = n
		}
	}

	matches, err := store.NewSecretRepo(s.st).SearchKeys(r.Context(), q, searchStoreCap)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}

	cache := map[string]configDisplay{}
	results := make([]searchKeyResult, 0, limit)
	extraVisible := false

	for _, m := range matches {
		disp, ok := cache[m.ConfigID]
		if !ok {
			disp = s.resolveConfigDisplay(r, m.ConfigID)
			cache[m.ConfigID] = disp
		}
		if !disp.allowed {
			continue
		}
		if len(results) >= limit {
			// A visible match beyond the page → truncated.
			extraVisible = true
			break
		}
		results = append(results, searchKeyResult{
			Key:             m.Key,
			ProjectID:       disp.projectID,
			ProjectName:     disp.projectName,
			ProjectSlug:     disp.projectSlug,
			EnvironmentID:   disp.environmentID,
			EnvironmentSlug: disp.environmentSlug,
			ConfigID:        m.ConfigID,
			ConfigName:      disp.configName,
		})
	}

	// `truncated` is derived ONLY from post-authz visible matches (a visible
	// match beyond the page). It must NOT reflect the raw pre-authz store count:
	// doing so would leak a coarse global-keyspace cardinality signal to a caller
	// with no grants (e.g. an unprivileged `q=e` returning truncated:true reveals
	// the system holds >= the store cap of matches they cannot read).
	writeJSON(w, http.StatusOK, searchKeysResponse{
		Results:   results,
		Truncated: extraVisible,
	})
}

// resolveConfigDisplay resolves a config's scope, checks SecretRead for the
// current principal, and gathers its display names. A config that errors during
// resolution OR is denied yields allowed=false (deny-by-default, no oracle).
func (s *Server) resolveConfigDisplay(r *http.Request, configID string) configDisplay {
	res, err := s.resolveScopeResource(r.Context(), "config", configID)
	if err != nil {
		return configDisplay{}
	}
	if err := s.can(r, authz.SecretRead, res); err != nil {
		return configDisplay{}
	}
	// Fetch display names. A lookup failure drops the config (fail-closed).
	cfg, err := store.NewConfigRepo(s.st).Get(r.Context(), configID)
	if err != nil {
		return configDisplay{}
	}
	env, err := store.NewEnvironmentRepo(s.st).Get(r.Context(), cfg.EnvironmentID)
	if err != nil {
		return configDisplay{}
	}
	proj, err := store.NewProjectRepo(s.st).Get(r.Context(), env.ProjectID)
	if err != nil {
		return configDisplay{}
	}
	return configDisplay{
		allowed:         true,
		projectID:       proj.ID,
		projectName:     proj.Name,
		projectSlug:     proj.Slug,
		environmentID:   env.ID,
		environmentSlug: env.Slug,
		configName:      cfg.Name,
	}
}
