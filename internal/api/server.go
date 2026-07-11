package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/dynamic"
	"github.com/steveokay/janus-secrets/internal/rotation"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/secretsync"
	"github.com/steveokay/janus-secrets/internal/store"
	"github.com/steveokay/janus-secrets/internal/transit"
)

// Config is the api server's static configuration.
type Config struct {
	// ListenAddr defaults to ":8200".
	ListenAddr string
	// SealType is the effective seal type ("shamir" or "awskms"): the stored
	// type when initialized, otherwise the operator-configured one.
	SealType string
	// Version is the janus build version, stamped into backup headers.
	Version string
}

// Server is Janus's HTTP server. The keyring is the single source of truth
// for sealed-ness; svc is held for future secret routes and may be nil until
// those exist.
type Server struct {
	cfg      Config
	keyring  *crypto.Keyring
	unsealer crypto.Unsealer
	seals    crypto.SealConfigStore
	service  *secrets.Service
	transit  *transit.Service    // nil in unit-test servers that exercise no transit path
	rotation *rotation.Service   // nil in unit-test servers that exercise no rotation path
	sync     *secretsync.Service // nil in unit-test servers that exercise no sync path
	dynamic  *dynamic.Service    // nil in unit-test servers that exercise no dynamic path
	auth     *auth.Service       // nil only in unit tests that exercise no auth path
	authz    *authz.Engine       // nil only in unit-test servers that exercise no authz path
	st       *store.Store        // for scope-chain resolution + membership/user handlers
	audit    *audit.Recorder     // nil in unit-test servers; Boot always wires a real one
	logger   *slog.Logger
	router   chi.Router
	// initMu serializes POST /v1/sys/init: the unsealer's Init is
	// get-then-put, so unserialized concurrent inits could both report
	// success while only one share set matches the stored seal.
	initMu sync.Mutex
}

// New wires the router. logger nil defaults to slog.Default().
func New(cfg Config, kr *crypto.Keyring, u crypto.Unsealer,
	seals crypto.SealConfigStore, svc *secrets.Service, tr *transit.Service, rot *rotation.Service, syncSvc *secretsync.Service, dyn *dynamic.Service, authSvc *auth.Service,
	authorizer *authz.Engine, st *store.Store, auditRec *audit.Recorder, logger *slog.Logger) *Server {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8200"
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{cfg: cfg, keyring: kr, unsealer: u, seals: seals, service: svc, transit: tr, rotation: rot,
		sync: syncSvc, dynamic: dyn, auth: authSvc, authz: authorizer, st: st, audit: auditRec, logger: logger}

	r := chi.NewRouter()
	r.Use(requestLogger(logger))
	r.Use(RequireUnsealed(kr))
	r.Route("/v1/sys", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Get("/live", s.handleLive)
		r.Get("/ready", s.handleReady)
		r.Get("/seal-status", s.handleSealStatus)
		r.Post("/init", s.handleInit)
		r.Post("/unseal", s.handleUnseal)
		r.Post("/unseal/reset", s.handleUnsealReset)
		// Pre-auth bootstrap like /init: only an EMPTY instance accepts it
		// (the handler gates on emptiness under initMu).
		r.Post("/restore", s.handleRestore)
		// Production always wires a non-nil auth service (Boot does), so seal is
		// authenticated. Unit-test servers pass nil and hit the route directly.
		if s.auth != nil && s.authz != nil {
			r.With(RequireAuth(s.auth), s.requireInstance(authz.SysSeal, "sys.seal", "")).Post("/seal", s.handleSeal)
			r.With(RequireAuth(s.auth), s.requireInstance(authz.SysBackup, "sys.backup", "")).Get("/backup", s.handleBackup)
			r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.config", "oidc")).Get("/oidc", s.handleOIDCConfigGet)
			r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.config", "oidc")).Put("/oidc", s.handleOIDCConfigPut)
			r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.config", "oidc")).Delete("/oidc", s.handleOIDCConfigDelete)
			r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Get("/oidc/federation", s.handleFederationConfigGet)
			r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Put("/oidc/federation", s.handleFederationConfigPut)
			r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Delete("/oidc/federation", s.handleFederationConfigDelete)
			r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Get("/oidc/federation/bindings", s.handleFederationBindingsList)
			r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Post("/oidc/federation/bindings", s.handleFederationBindingCreate)
			r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Delete("/oidc/federation/bindings/{id}", s.handleFederationBindingDelete)
		} else {
			r.Post("/seal", s.handleSeal)
			r.Get("/backup", s.handleBackup)
		}
	})
	if s.auth != nil {
		loginLimiter := newIPRateLimiter(10.0/60.0, 5) // 10/min sustained, burst 5
		r.Route("/v1/auth", func(r chi.Router) {
			r.With(loginLimiter.middleware).Post("/login", s.handleLogin)
			r.With(loginLimiter.middleware).Get("/oidc/status", s.handleOIDCStatus)
			r.With(loginLimiter.middleware).Get("/oidc/login", s.handleOIDCLogin)
			r.With(loginLimiter.middleware).Get("/oidc/callback", s.handleOIDCCallback)
			r.With(loginLimiter.middleware).Post("/oidc/federate", s.handleOIDCFederate)
			r.Group(func(r chi.Router) {
				r.Use(RequireAuth(s.auth))
				r.Post("/logout", s.handleLogout)
				r.Get("/me", s.handleMe)
				r.With(loginLimiter.middleware).Post("/password", s.handlePasswordChange)
			})
		})
	}
	if s.auth != nil && s.authz != nil {
		r.Route("/v1/tokens", func(r chi.Router) {
			r.Use(RequireAuth(s.auth))
			r.Post("/", s.handleTokenMint)
			r.Get("/", s.handleTokenList)
			r.Delete("/{id}", s.handleTokenRevoke)
		})
		r.Route("/v1/users", func(r chi.Router) {
			r.Use(RequireAuth(s.auth))
			r.Post("/", s.handleUserCreate)
			r.Get("/", s.handleUserList)
			r.Post("/{id}/disable", s.handleUserDisable)
		})
		r.Route("/v1/instance/members", func(r chi.Router) {
			r.Use(RequireAuth(s.auth))
			r.Get("/", func(w http.ResponseWriter, r *http.Request) { s.membersList(w, r, s.instanceScope()) })
			r.Put("/{uid}", func(w http.ResponseWriter, r *http.Request) {
				s.memberPut(w, r, s.instanceScope(), chi.URLParam(r, "uid"))
			})
			r.Delete("/{uid}", func(w http.ResponseWriter, r *http.Request) {
				s.memberDelete(w, r, s.instanceScope(), chi.URLParam(r, "uid"))
			})
		})
		r.Route("/v1/projects/{pid}/members", func(r chi.Router) {
			r.Use(RequireAuth(s.auth))
			r.Get("/", func(w http.ResponseWriter, r *http.Request) { s.membersList(w, r, s.projectScope(r)) })
			r.Put("/{uid}", func(w http.ResponseWriter, r *http.Request) {
				s.memberPut(w, r, s.projectScope(r), chi.URLParam(r, "uid"))
			})
			r.Delete("/{uid}", func(w http.ResponseWriter, r *http.Request) {
				s.memberDelete(w, r, s.projectScope(r), chi.URLParam(r, "uid"))
			})
		})
		r.Route("/v1/projects/{pid}/environments/{eid}/members", func(r chi.Router) {
			r.Use(RequireAuth(s.auth))
			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				spec, err := s.envScope(r)
				if err != nil {
					s.writeServiceError(w, err)
					return
				}
				s.membersList(w, r, spec)
			})
			r.Put("/{uid}", func(w http.ResponseWriter, r *http.Request) {
				spec, err := s.envScope(r)
				if err != nil {
					s.writeServiceError(w, err)
					return
				}
				s.memberPut(w, r, spec, chi.URLParam(r, "uid"))
			})
			r.Delete("/{uid}", func(w http.ResponseWriter, r *http.Request) {
				spec, err := s.envScope(r)
				if err != nil {
					s.writeServiceError(w, err)
					return
				}
				s.memberDelete(w, r, spec, chi.URLParam(r, "uid"))
			})
		})
		r.Group(func(r chi.Router) {
			r.Use(RequireAuth(s.auth))
			r.Post("/v1/projects", s.handleProjectCreate)
			r.Get("/v1/projects", s.handleProjectList)
			r.Get("/v1/projects/{pid}", s.handleProjectGet)
			r.Delete("/v1/projects/{pid}", s.handleProjectDelete)
			r.Post("/v1/projects/{pid}/restore", s.handleProjectRestore)
		})
		r.Group(func(r chi.Router) {
			r.Use(RequireAuth(s.auth))
			r.Post("/v1/projects/{pid}/environments", s.handleEnvCreate)
			r.Get("/v1/projects/{pid}/environments", s.handleEnvList)
			r.Get("/v1/projects/{pid}/environments/{eid}", s.handleEnvGet)
			r.Delete("/v1/projects/{pid}/environments/{eid}", s.handleEnvDelete)
			r.Post("/v1/projects/{pid}/environments/{eid}/restore", s.handleEnvRestore)
		})
		r.Group(func(r chi.Router) {
			r.Use(RequireAuth(s.auth))
			r.Post("/v1/projects/{pid}/environments/{eid}/configs", s.handleConfigCreate)
			r.Get("/v1/projects/{pid}/environments/{eid}/configs", s.handleConfigList)
			r.Get("/v1/configs/{cid}", s.handleConfigGet)
			r.Delete("/v1/configs/{cid}", s.handleConfigDelete)
			r.Post("/v1/configs/{cid}/restore", s.handleConfigRestore)
		})
		r.Group(func(r chi.Router) {
			r.Use(RequireAuth(s.auth))
			r.Get("/v1/configs/{cid}/secrets", s.handleSecretsList)
			r.Get("/v1/configs/{cid}/secrets/{key}", s.handleSecretGet)
			r.Get("/v1/configs/{cid}/secrets/{key}/history", s.handleKeyHistory)
		})
		r.Group(func(r chi.Router) {
			r.Use(RequireAuth(s.auth))
			r.Put("/v1/configs/{cid}/secrets", s.handleSecretsBatchWrite)
			r.Put("/v1/configs/{cid}/secrets/{key}", s.handleSecretPut)
			r.Delete("/v1/configs/{cid}/secrets/{key}", s.handleSecretDelete)
		})
		r.Group(func(r chi.Router) {
			r.Use(RequireAuth(s.auth))
			r.Get("/v1/configs/{cid}/versions", s.handleVersionList)
			r.Get("/v1/configs/{cid}/versions/diff", s.handleVersionDiff)
			r.Post("/v1/configs/{cid}/rollback", s.handleRollback)
		})
		if s.transit != nil {
			r.Group(func(r chi.Router) {
				r.Use(RequireAuth(s.auth))
				r.Post("/v1/transit/keys", s.handleTransitCreate)
				r.Get("/v1/transit/keys", s.handleTransitList)
				r.Get("/v1/transit/keys/{name}", s.handleTransitGet)
				r.Post("/v1/transit/keys/{name}/rotate", s.handleTransitRotate)
				r.Post("/v1/transit/keys/{name}/config", s.handleTransitConfig)
				r.Post("/v1/transit/keys/{name}/trim", s.handleTransitTrim)
				r.Delete("/v1/transit/keys/{name}", s.handleTransitDelete)
				r.Post("/v1/transit/encrypt/{name}", s.handleTransitEncrypt)
				r.Post("/v1/transit/decrypt/{name}", s.handleTransitDecrypt)
				r.Post("/v1/transit/sign/{name}", s.handleTransitSign)
				r.Post("/v1/transit/verify/{name}", s.handleTransitVerify)
				r.Post("/v1/transit/rewrap/{name}", s.handleTransitRewrap)
				r.Post("/v1/transit/datakey/plaintext/{name}", s.handleTransitDatakeyPlaintext)
				r.Post("/v1/transit/datakey/wrapped/{name}", s.handleTransitDatakeyWrapped)
			})
		}
		if s.rotation != nil {
			r.Group(func(r chi.Router) {
				r.Use(RequireAuth(s.auth))
				r.Post("/v1/rotation/policies", s.handleRotationCreate)
				r.Get("/v1/rotation/policies", s.handleRotationList)
				r.Get("/v1/rotation/policies/{id}", s.handleRotationGet)
				r.Patch("/v1/rotation/policies/{id}", s.handleRotationUpdate)
				r.Delete("/v1/rotation/policies/{id}", s.handleRotationDelete)
				r.Post("/v1/rotation/policies/{id}/rotate", s.handleRotationRotateNow)
			})
		}
		if s.sync != nil {
			r.Group(func(r chi.Router) {
				r.Use(RequireAuth(s.auth))
				r.Post("/v1/sync/targets", s.handleSyncCreate)
				r.Get("/v1/sync/targets", s.handleSyncList)
				r.Get("/v1/sync/targets/{id}", s.handleSyncGet)
				r.Patch("/v1/sync/targets/{id}", s.handleSyncUpdate)
				r.Delete("/v1/sync/targets/{id}", s.handleSyncDelete)
				r.Post("/v1/sync/targets/{id}/sync", s.handleSyncNow)
			})
		}
		if s.dynamic != nil {
			r.Group(func(r chi.Router) {
				r.Use(RequireAuth(s.auth))
				r.Post("/v1/dynamic/roles", s.handleDynamicRoleCreate)
				r.Get("/v1/dynamic/roles", s.handleDynamicRoleList)
				r.Get("/v1/dynamic/roles/{id}", s.handleDynamicRoleGet)
				r.Patch("/v1/dynamic/roles/{id}", s.handleDynamicRoleUpdate)
				r.Delete("/v1/dynamic/roles/{id}", s.handleDynamicRoleDelete)
				r.Post("/v1/dynamic/roles/{id}/creds", s.handleDynamicIssue)
				r.Get("/v1/dynamic/leases", s.handleDynamicLeaseList)
				r.Post("/v1/dynamic/leases/{id}/renew", s.handleDynamicLeaseRenew)
				r.Post("/v1/dynamic/leases/{id}/revoke", s.handleDynamicLeaseRevoke)
			})
		}
		if s.audit != nil {
			r.Route("/v1/audit", func(r chi.Router) {
				r.Use(RequireAuth(s.auth))
				r.Get("/verify", s.handleAuditVerify)
				r.Get("/export", s.handleAuditExport)
				r.Get("/events", s.handleAuditEvents)
			})
		}
		r.Group(func(r chi.Router) {
			r.Use(RequireAuth(s.auth))
			r.Get("/v1/metrics/reads-24h", s.handleMetricsReads)
			r.Get("/v1/projects/{pid}/metrics/reads-24h", s.handleProjectMetricsReads)
		})
	}
	s.router = r
	return s
}

// Handler exposes the router (used by tests and ListenAndServe).
func (s *Server) Handler() http.Handler { return s.router }

// MountUI installs h as the router's fallback for any route the /v1 API does not
// match — i.e. the embedded SPA and its assets. Call after New, before serving.
// nil is a no-op (unit-test servers with no UI keep chi's default 404).
func (s *Server) MountUI(h http.Handler) {
	if h == nil {
		return
	}
	s.router.NotFound(h.ServeHTTP)
}

// ListenAndServe serves until ctx is canceled, then drains for up to 10s.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
