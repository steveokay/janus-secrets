package api

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/auditship"
	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/dynamic"
	"github.com/steveokay/janus-secrets/internal/masterkeys"
	"github.com/steveokay/janus-secrets/internal/metrics"
	"github.com/steveokay/janus-secrets/internal/notification"
	"github.com/steveokay/janus-secrets/internal/projectkeys"
	"github.com/steveokay/janus-secrets/internal/promote"
	"github.com/steveokay/janus-secrets/internal/rotation"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/secretsync"
	"github.com/steveokay/janus-secrets/internal/store"
	"github.com/steveokay/janus-secrets/internal/transit"
	"golang.org/x/crypto/acme/autocert"
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
	// HTTP server hardening. Zero on any timeout field disables that timeout
	// (Go's default). cmd/janus applies production defaults; tests building
	// Config/BootConfig directly get zero.
	HTTPReadTimeout  time.Duration
	HTTPWriteTimeout time.Duration
	HTTPIdleTimeout  time.Duration
	HTTPMaxBodyBytes int64 // 0 = no limit (consumed by the body-limit middleware)
	// HTTPShutdownGrace bounds the graceful-drain window on ctx cancel for the
	// main server and any auxiliary listeners. Zero → New applies the 10s
	// default (New normalizes it so ListenAndServe always sees a positive value).
	HTTPShutdownGrace time.Duration
	// Scheduler tick intervals, surfaced by /v1/sys/status as each engine's
	// interval_seconds and enabled flag. Zero = disabled.
	RotationTick time.Duration
	SyncTick     time.Duration
	DynamicTick  time.Duration
	// MetricsToken, when non-empty, enables the /metrics endpoint gated by this
	// static bearer token. Empty → /metrics returns 404.
	MetricsToken string
	// BreakGlassMaxTTL is the ceiling a break-glass grant's requested TTL is
	// clamped to (JANUS_BREAKGLASS_MAX_TTL). Zero → New applies the 1h default.
	BreakGlassMaxTTL time.Duration
	// TLS configures the native HTTPS listener. Zero value → plain HTTP (TLS is
	// delegated to a reverse proxy, the historical default).
	TLS TLSConfig
}

// TLSConfig configures the optional native HTTPS listener. Two mutually
// exclusive modes: static certs (CertFile+KeyFile) or ACME/Let's Encrypt
// (ACMEDomains). Leaving all fields empty keeps Janus on plain HTTP so TLS can
// be terminated by a reverse proxy (the default).
type TLSConfig struct {
	// CertFile and KeyFile are paths to a PEM certificate/chain and its private
	// key. Both must be set together to serve HTTPS from static certs.
	CertFile string
	KeyFile  string
	// ACMEDomains are the hostnames autocert is whitelisted to obtain
	// certificates for. When non-empty (and static certs are unset), Janus
	// provisions certs via Let's Encrypt.
	ACMEDomains []string
	// ACMEEmail is the optional ACME account contact address.
	ACMEEmail string
	// ACMECache is the directory autocert caches issued certificates in.
	// Defaults to "./.janus-acme" when ACME is enabled and this is empty.
	ACMECache string
	// RedirectHTTP, when non-empty (e.g. ":80"), runs a redirect-only listener
	// on that address that 301s plain HTTP requests to their https:// URL. Only
	// consulted in the static-cert path (ACME runs its own :80 handler).
	RedirectHTTP string
}

// Enabled reports whether any TLS mode is configured.
func (t TLSConfig) Enabled() bool {
	return t.CertFile != "" || t.KeyFile != "" || len(t.ACMEDomains) > 0
}

// IsStaticCerts reports whether the static-cert mode is (fully) configured.
func (t TLSConfig) IsStaticCerts() bool {
	return t.CertFile != "" && t.KeyFile != ""
}

// IsACME reports whether ACME mode is configured.
func (t TLSConfig) IsACME() bool {
	return len(t.ACMEDomains) > 0
}

// Validate checks the TLS configuration for the mutually exclusive and
// paired-field constraints, returning a clear startup error on misconfig.
func (t TLSConfig) Validate() error {
	if !t.Enabled() {
		return nil
	}
	// Static certs require both halves.
	if (t.CertFile != "") != (t.KeyFile != "") {
		return errors.New("TLS static certs require both JANUS_TLS_CERT and JANUS_TLS_KEY (only one was set)")
	}
	// Static certs and ACME are mutually exclusive.
	if t.IsStaticCerts() && t.IsACME() {
		return errors.New("TLS static certs (JANUS_TLS_CERT/JANUS_TLS_KEY) and ACME (JANUS_TLS_ACME_DOMAINS) are mutually exclusive")
	}
	return nil
}

// acmeCacheDir returns the effective autocert cache directory.
func (t TLSConfig) acmeCacheDir() string {
	if t.ACMECache != "" {
		return t.ACMECache
	}
	return "./.janus-acme"
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
	// projectKeys drives owner-only project-KEK rotate/rewrap/status. Constructed
	// in New from the keyring + store (both always present in production); nil in
	// unit-test servers built without a real store.
	projectKeys *projectkeys.Service
	// masterKeys drives owner-only master-key rotation + the Shamir rekey
	// ceremony. Wired in Boot next to projectKeys; nil in unit-test servers
	// built without a real store.
	masterKeys *masterkeys.Service
	// promote drives the promotion pipeline (env ids) + config locked-keys (key
	// names). Value-free. nil in unit-test servers built without a real store /
	// secrets service.
	promote *promote.Service
	// notification manages alerting channels + the delivery dispatcher.
	// Constructed in New from the keyring + store; nil in unit-test servers built
	// without a real store.
	notification *notification.Service
	// auditShip streams the audit log to an external SIEM (webhook/syslog). Wired
	// in Boot only when JANUS_AUDIT_SHIP_MODE is a real destination; nil otherwise
	// (and in unit-test servers). Read by /v1/sys/status for a value-free snapshot.
	auditShip *auditship.Service
	auth         *auth.Service   // nil only in unit tests that exercise no auth path
	authz        *authz.Engine   // nil only in unit-test servers that exercise no authz path
	st           *store.Store    // for scope-chain resolution + membership/user handlers
	// breakGlass persists time-boxed emergency role elevations. Wired in New from
	// the store; nil in unit-test servers built without a real store (the
	// /break-glass routes are then not mounted).
	breakGlass       *store.BreakGlassRepo
	breakGlassMaxTTL time.Duration
	audit        *audit.Recorder // nil in unit-test servers; Boot always wires a real one
	logger       *slog.Logger
	router       chi.Router
	// metrics is the janus_ Prometheus metric set + HTTP instrumentation. Always
	// constructed in New (even when nil-token, so instrumentation still records).
	metrics *AppMetrics
	// metricsToken gates GET /metrics; empty → the endpoint 404s.
	metricsToken string
	// ticks is the shared scheduler last-tick tracker feeding both
	// janus_scheduler_last_tick_seconds and /v1/sys/status.
	ticks *metrics.TickTracker
	// startTime is the process/server start, for uptime + janus_start_time_seconds.
	startTime time.Time
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
	if cfg.HTTPShutdownGrace <= 0 {
		cfg.HTTPShutdownGrace = 10 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{cfg: cfg, keyring: kr, unsealer: u, seals: seals, service: svc, transit: tr, rotation: rot,
		sync: syncSvc, dynamic: dyn, auth: authSvc, authz: authorizer, st: st, audit: auditRec, logger: logger}
	// Metrics + scheduler tick tracking. The tick tracker is shared with the
	// three schedulers (wired via SetTickHook in Boot) and read by
	// /v1/sys/status. AppMetrics registers scrape-time collectors that degrade
	// gracefully when kr/st are nil (unit-test servers).
	s.startTime = time.Now()
	s.metricsToken = cfg.MetricsToken
	s.ticks = metrics.NewTickTracker()
	s.metrics = NewAppMetrics(kr, st, s.ticks, s.startTime)
	// Project-KEK rotation service: available whenever a real keyring and store
	// are wired (production and full e2e). Unit-test servers built with a nil
	// store leave it nil and simply don't mount the /kek routes.
	if kr != nil && st != nil {
		s.projectKeys = projectkeys.New(kr, store.NewProjectRepo(st), store.NewProjectKEKVersionRepo(st), store.NewSecretRepo(st))
	}
	// Master-key rotation + Shamir rekey ceremony (owner-only). Available whenever
	// a real keyring, unsealer, and store are wired (production and full e2e). The
	// unsealer satisfies masterkeys.Unsealer via Reseal. Unit-test servers built
	// with a nil store/unsealer leave it nil and don't mount the /master-key routes.
	if kr != nil && st != nil && u != nil {
		s.masterKeys = masterkeys.NewService(kr, u, store.NewMasterKeyRepo(st), seals)
	}
	// Promotion service: available whenever a real keyring, store, and secrets
	// service are wired (production and full e2e). Used by the pipeline +
	// locked-keys routes (and the next task's preview/apply routes).
	if kr != nil && st != nil && svc != nil {
		s.promote = promote.New(svc, st)
	}
	// Notification service: alerting channels + the audit-tailing delivery
	// dispatcher. Available whenever a real keyring and store are wired; nil in
	// unit-test servers built without a real store.
	if kr != nil && st != nil {
		s.notification = notification.New(kr, st, store.NewAuditRepo(st), logger)
	}
	// Break-glass grants: available whenever a real store is wired. The overlay
	// on the authz engine is wired in Boot; these routes let operators activate,
	// list, and revoke grants.
	if st != nil {
		s.breakGlass = store.NewBreakGlassRepo(st)
	}
	s.breakGlassMaxTTL = cfg.BreakGlassMaxTTL
	if s.breakGlassMaxTTL <= 0 {
		s.breakGlassMaxTTL = time.Hour
	}

	r := chi.NewRouter()
	r.Use(requestLogger(logger))
	r.Use(s.instrument)
	r.Use(RequireUnsealed(kr))
	if cfg.HTTPMaxBodyBytes > 0 {
		r.Use(bodyLimit(cfg.HTTPMaxBodyBytes))
	}
	if st != nil && authSvc != nil {
		r.Use(idempotencyMiddleware(idemRepoAdapter{repo: store.NewIdempotencyRepo(st)}, authSvc))
	}
	// GET /metrics at ROOT (outside /v1). metricsAuth 404s when no token is
	// configured; otherwise requires the static bearer token. Registered after
	// all r.Use middlewares (chi requires middleware before routes); excluded
	// from instrumentation inside instrument.
	r.With(s.metricsAuth).Get("/metrics", s.handleMetrics)
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
			r.With(RequireAuth(s.auth, s), s.requireInstance(authz.SysSeal, "sys.seal", "")).Post("/seal", s.handleSeal)
			r.With(RequireAuth(s.auth, s), s.requireInstance(authz.SysBackup, "sys.backup", "")).Get("/backup", s.handleBackup)
			r.With(RequireAuth(s.auth, s), s.requireInstance(authz.OIDCManage, "oidc.config", "oidc")).Get("/oidc", s.handleOIDCConfigGet)
			r.With(RequireAuth(s.auth, s), s.requireInstance(authz.OIDCManage, "oidc.config", "oidc")).Put("/oidc", s.handleOIDCConfigPut)
			r.With(RequireAuth(s.auth, s), s.requireInstance(authz.OIDCManage, "oidc.config", "oidc")).Delete("/oidc", s.handleOIDCConfigDelete)
			r.With(RequireAuth(s.auth, s), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Get("/oidc/federation", s.handleFederationConfigGet)
			r.With(RequireAuth(s.auth, s), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Put("/oidc/federation", s.handleFederationConfigPut)
			r.With(RequireAuth(s.auth, s), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Delete("/oidc/federation", s.handleFederationConfigDelete)
			r.With(RequireAuth(s.auth, s), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Get("/oidc/federation/bindings", s.handleFederationBindingsList)
			r.With(RequireAuth(s.auth, s), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Post("/oidc/federation/bindings", s.handleFederationBindingCreate)
			r.With(RequireAuth(s.auth, s), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Delete("/oidc/federation/bindings/{id}", s.handleFederationBindingDelete)
			// Master-key rotation + Shamir rekey ceremony (owner-only). Owner-only
			// is enforced in-handler via s.authorize/s.can (authz.SysMasterKey), so
			// only RequireAuth is applied here. Mounted when the service is wired.
			if s.masterKeys != nil {
				r.With(RequireAuth(s.auth, s)).Get("/master-key", s.handleMasterKeyStatus)
				r.With(RequireAuth(s.auth, s)).Post("/master-key/rotate", s.handleMasterKeyRotate)
				r.With(RequireAuth(s.auth, s)).Post("/master-key/rekey/init", s.handleMasterKeyRekeyInit)
				r.With(RequireAuth(s.auth, s)).Post("/master-key/rekey/submit", s.handleMasterKeyRekeySubmit)
				r.With(RequireAuth(s.auth, s)).Delete("/master-key/rekey", s.handleMasterKeyRekeyCancel)
			}
			r.With(RequireAuth(s.auth, s)).Get("/version", s.handleVersion)
			// Admin health snapshot. Instance AuditRead is enforced in-handler
			// via s.authorize, so only RequireAuth is applied here.
			r.With(RequireAuth(s.auth, s)).Get("/status", s.handleSysStatus)
		} else {
			r.Post("/seal", s.handleSeal)
			r.Get("/backup", s.handleBackup)
			r.Get("/version", s.handleVersion)
			// /status is intentionally NOT mounted in the auth-less branch: it
			// is authz-gated in-handler via s.authorize, which needs s.authz.
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
				r.Use(RequireAuth(s.auth, s))
				r.Post("/logout", s.handleLogout)
				r.Get("/me", s.handleMe)
				r.With(loginLimiter.middleware).Post("/password", s.handlePasswordChange)
				r.Get("/sessions", s.handleSessionList)
				r.Delete("/sessions", s.handleSessionRevokeOthers)
				r.Delete("/sessions/{id}", s.handleSessionRevoke)
				r.Get("/totp", s.handleTOTPStatus)
				r.With(loginLimiter.middleware).Post("/totp/enroll", s.handleTOTPEnroll)
				r.With(loginLimiter.middleware).Post("/totp/confirm", s.handleTOTPConfirm)
				r.With(loginLimiter.middleware).Post("/totp/disable", s.handleTOTPDisable)
				r.With(loginLimiter.middleware).Post("/totp/recovery-codes", s.handleTOTPRecoveryCodes)
			})
		})
	}
	if s.auth != nil && s.authz != nil {
		r.Route("/v1/tokens", func(r chi.Router) {
			r.Use(RequireAuth(s.auth, s))
			r.Post("/", s.handleTokenMint)
			r.Get("/", s.handleTokenList)
			r.Get("/new-ips", s.handleTokenNewIPs)
			r.Patch("/{id}", s.handleTokenUpdate)
			r.Delete("/{id}", s.handleTokenRevoke)
		})
		r.Route("/v1/users", func(r chi.Router) {
			r.Use(RequireAuth(s.auth, s))
			r.Post("/", s.handleUserCreate)
			r.Get("/", s.handleUserList)
			r.Post("/{id}/disable", s.handleUserDisable)
			r.Post("/{id}/unlock", s.handleUserUnlock)
		})
		r.Route("/v1/trash", func(r chi.Router) {
			r.Use(RequireAuth(s.auth, s))
			r.Get("/", s.handleTrashList)
		})
		r.Route("/v1/instance/members", func(r chi.Router) {
			r.Use(RequireAuth(s.auth, s))
			r.Get("/", func(w http.ResponseWriter, r *http.Request) { s.membersList(w, r, s.instanceScope()) })
			r.Put("/{uid}", func(w http.ResponseWriter, r *http.Request) {
				s.memberPut(w, r, s.instanceScope(), chi.URLParam(r, "uid"))
			})
			r.Delete("/{uid}", func(w http.ResponseWriter, r *http.Request) {
				s.memberDelete(w, r, s.instanceScope(), chi.URLParam(r, "uid"))
			})
		})
		r.Route("/v1/projects/{pid}/members", func(r chi.Router) {
			r.Use(RequireAuth(s.auth, s))
			r.Get("/", func(w http.ResponseWriter, r *http.Request) { s.membersList(w, r, s.projectScope(r)) })
			r.Put("/{uid}", func(w http.ResponseWriter, r *http.Request) {
				s.memberPut(w, r, s.projectScope(r), chi.URLParam(r, "uid"))
			})
			r.Delete("/{uid}", func(w http.ResponseWriter, r *http.Request) {
				s.memberDelete(w, r, s.projectScope(r), chi.URLParam(r, "uid"))
			})
		})
		r.Route("/v1/projects/{pid}/environments/{eid}/members", func(r chi.Router) {
			r.Use(RequireAuth(s.auth, s))
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
			r.Use(RequireAuth(s.auth, s))
			r.Post("/v1/projects", s.handleProjectCreate)
			r.Get("/v1/projects", s.handleProjectList)
			r.Get("/v1/projects/{pid}", s.handleProjectGet)
			r.Patch("/v1/projects/{pid}", s.handleProjectRename)
			r.Delete("/v1/projects/{pid}", s.handleProjectDelete)
			r.Post("/v1/projects/{pid}/restore", s.handleProjectRestore)
		})
		if s.projectKeys != nil {
			r.Group(func(r chi.Router) {
				r.Use(RequireAuth(s.auth, s))
				r.Post("/v1/projects/{pid}/kek/rotate", s.handleKEKRotate)
				r.Post("/v1/projects/{pid}/kek/rewrap", s.handleKEKRewrap)
				r.Get("/v1/projects/{pid}/kek", s.handleKEKStatus)
			})
		}
		if s.promote != nil {
			r.Group(func(r chi.Router) {
				r.Use(RequireAuth(s.auth, s))
				r.Get("/v1/projects/{pid}/pipeline", s.handlePipelineGet)
				r.Put("/v1/projects/{pid}/pipeline", s.handlePipelinePut)
				r.Get("/v1/configs/{cid}/locked-keys", s.handleLockedKeysList)
				r.Post("/v1/configs/{cid}/locked-keys", s.handleLockedKeyCreate)
				r.Delete("/v1/configs/{cid}/locked-keys/{key}", s.handleLockedKeyDelete)
				r.Get("/v1/promote/preview", s.handlePromotePreview)
				r.Post("/v1/promote", s.handlePromoteApply)
				r.Post("/v1/promote/requests", s.handlePromoteRequestCreate)
				r.Get("/v1/promote/requests", s.handlePromoteRequestList)
				r.Get("/v1/promote/requests/{id}", s.handlePromoteRequestGet)
				r.Post("/v1/promote/requests/{id}/approve", s.handlePromoteRequestApprove)
				r.Post("/v1/promote/requests/{id}/reject", s.handlePromoteRequestReject)
				r.Post("/v1/promote/requests/{id}/cancel", s.handlePromoteRequestCancel)
			})
		}
		r.Group(func(r chi.Router) {
			r.Use(RequireAuth(s.auth, s))
			r.Post("/v1/projects/{pid}/environments", s.handleEnvCreate)
			r.Get("/v1/projects/{pid}/environments", s.handleEnvList)
			r.Get("/v1/projects/{pid}/environments/{eid}", s.handleEnvGet)
			r.Patch("/v1/projects/{pid}/environments/{eid}", s.handleEnvRename)
			r.Delete("/v1/projects/{pid}/environments/{eid}", s.handleEnvDelete)
			r.Post("/v1/projects/{pid}/environments/{eid}/restore", s.handleEnvRestore)
			r.Post("/v1/projects/{pid}/environments/{eid}/clone", s.handleEnvClone)
		})
		r.Group(func(r chi.Router) {
			r.Use(RequireAuth(s.auth, s))
			r.Post("/v1/projects/{pid}/environments/{eid}/configs", s.handleConfigCreate)
			r.Get("/v1/projects/{pid}/environments/{eid}/configs", s.handleConfigList)
			r.Get("/v1/configs/{cid}", s.handleConfigGet)
			r.Delete("/v1/configs/{cid}", s.handleConfigDelete)
			r.Post("/v1/configs/{cid}/restore", s.handleConfigRestore)
		})
		r.Group(func(r chi.Router) {
			r.Use(RequireAuth(s.auth, s))
			r.Get("/v1/configs/{cid}/secrets", s.handleSecretsList)
			r.Get("/v1/configs/{cid}/secrets/{key}", s.handleSecretGet)
			r.Get("/v1/configs/{cid}/secrets/{key}/history", s.handleKeyHistory)
			r.Get("/v1/configs/{cid}/read-insights", s.handleReadInsights)
			r.Get("/v1/configs/{cid}/compare", s.handleConfigCompare)
		})
		r.Group(func(r chi.Router) {
			r.Use(RequireAuth(s.auth, s))
			r.Put("/v1/configs/{cid}/secrets", s.handleSecretsBatchWrite)
			r.Put("/v1/configs/{cid}/secrets/{key}", s.handleSecretPut)
			r.Delete("/v1/configs/{cid}/secrets/{key}", s.handleSecretDelete)
		})
		r.Group(func(r chi.Router) {
			// Advisory secret max-age / expiry policy (value-free metadata).
			r.Use(RequireAuth(s.auth, s))
			r.Get("/v1/configs/{cid}/max-age", s.handleMaxAgeList)
			r.Put("/v1/configs/{cid}/max-age", s.handleConfigMaxAgePut)
			r.Put("/v1/configs/{cid}/secrets/{key}/max-age", s.handleKeyMaxAgePut)
		})
		r.Group(func(r chi.Router) {
			// Per-key secret annotations (owner + note; value-free metadata).
			r.Use(RequireAuth(s.auth, s))
			r.Put("/v1/configs/{cid}/secrets/{key}/annotation", s.handleKeyAnnotationPut)
		})
		r.Group(func(r chi.Router) {
			r.Use(RequireAuth(s.auth, s))
			r.Get("/v1/configs/{cid}/versions", s.handleVersionList)
			r.Get("/v1/configs/{cid}/versions/diff", s.handleVersionDiff)
			r.Post("/v1/configs/{cid}/rollback", s.handleRollback)
		})
		if s.transit != nil {
			r.Group(func(r chi.Router) {
				r.Use(RequireAuth(s.auth, s))
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
				r.Use(RequireAuth(s.auth, s))
				r.Post("/v1/rotation/policies", s.handleRotationCreate)
				r.Get("/v1/rotation/policies", s.handleRotationList)
				r.Get("/v1/rotation/policies/{id}", s.handleRotationGet)
				r.Get("/v1/rotation/policies/{id}/runs", s.handleRotationRuns)
				r.Patch("/v1/rotation/policies/{id}", s.handleRotationUpdate)
				r.Delete("/v1/rotation/policies/{id}", s.handleRotationDelete)
				r.Post("/v1/rotation/policies/{id}/rotate", s.handleRotationRotateNow)
			})
		}
		if s.sync != nil {
			r.Group(func(r chi.Router) {
				r.Use(RequireAuth(s.auth, s))
				r.Post("/v1/sync/targets", s.handleSyncCreate)
				r.Get("/v1/sync/targets", s.handleSyncList)
				r.Get("/v1/sync/targets/{id}", s.handleSyncGet)
				r.Get("/v1/sync/targets/{id}/runs", s.handleSyncRuns)
				r.Patch("/v1/sync/targets/{id}", s.handleSyncUpdate)
				r.Delete("/v1/sync/targets/{id}", s.handleSyncDelete)
				r.Post("/v1/sync/targets/{id}/sync", s.handleSyncNow)
			})
		}
		if s.dynamic != nil {
			r.Group(func(r chi.Router) {
				r.Use(RequireAuth(s.auth, s))
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
				r.Use(RequireAuth(s.auth, s))
				r.Get("/verify", s.handleAuditVerify)
				r.Get("/export", s.handleAuditExport)
				r.Get("/events", s.handleAuditEvents)
				r.Get("/histogram", s.handleAuditHistogram)
			})
		}
		if s.notification != nil {
			r.Group(func(r chi.Router) {
				r.Use(RequireAuth(s.auth, s))
				r.Post("/v1/notifications/channels", s.handleNotificationCreate)
				r.Get("/v1/notifications/channels", s.handleNotificationList)
				r.Get("/v1/notifications/channels/{id}", s.handleNotificationGet)
				r.Patch("/v1/notifications/channels/{id}", s.handleNotificationUpdate)
				r.Delete("/v1/notifications/channels/{id}", s.handleNotificationDelete)
				r.Post("/v1/notifications/channels/{id}/test", s.handleNotificationTest)
				r.Get("/v1/notifications/channels/{id}/deliveries", s.handleNotificationDeliveries)
			})
		}
		r.Group(func(r chi.Router) {
			r.Use(RequireAuth(s.auth, s))
			r.Get("/v1/metrics/reads-24h", s.handleMetricsReads)
			r.Get("/v1/projects/{pid}/metrics/reads-24h", s.handleProjectMetricsReads)
		})
		// Break-glass: guarded self-service emergency role elevation. Any
		// authenticated user may reach these; the activation guard (must already
		// hold a role on the scope, target must be strictly higher) is enforced
		// in-handler. Mounted when the repo is wired (real store).
		if s.breakGlass != nil {
			r.Group(func(r chi.Router) {
				r.Use(RequireAuth(s.auth))
				r.Post("/v1/break-glass", s.handleBreakGlassActivate)
				r.Get("/v1/break-glass", s.handleBreakGlassList)
				r.Delete("/v1/break-glass/{id}", s.handleBreakGlassRevoke)
			})
		}
		// Global key-name search. Any authenticated principal; results are
		// authz-filtered per config (deny-by-default) inside the handler.
		r.Route("/v1/search", func(r chi.Router) {
			r.Use(RequireAuth(s.auth, s))
			r.Get("/keys", s.handleSearchKeys)
		})
	}
	s.router = r
	return s
}

// SetAuditShip attaches the audit-shipping service so /v1/sys/status can surface
// its value-free status. Wired in Boot only when a real destination is
// configured; a nil argument leaves the status block absent.
func (s *Server) SetAuditShip(a *auditship.Service) { s.auditShip = a }

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

// buildHTTPServer constructs the http.Server from s.cfg. ReadHeaderTimeout
// stays a fixed 10s (slowloris guard); the read/write/idle timeouts flow from
// config so they're operator-tunable (WriteTimeout defaults to 0 upstream so
// large streaming audit exports aren't truncated).
func (s *Server) buildHTTPServer() *http.Server {
	return &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       s.cfg.HTTPReadTimeout,
		WriteTimeout:      s.cfg.HTTPWriteTimeout,
		IdleTimeout:       s.cfg.HTTPIdleTimeout,
	}
}

// ListenAndServe serves until ctx is canceled, then drains for up to
// s.cfg.HTTPShutdownGrace (default 10s, normalized in New).
// It serves plain HTTP by default, or native HTTPS when s.cfg.TLS is configured
// (static certs or ACME/Let's Encrypt). Any auxiliary listeners it starts (the
// ACME HTTP-01 :80 handler, or an optional static-cert HTTP→HTTPS redirect
// server) are shut down alongside the main server on ctx cancel.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := s.cfg.TLS.Validate(); err != nil {
		return err
	}

	srv := s.buildHTTPServer()

	// aux collects secondary servers (ACME :80, redirect) so they drain on
	// shutdown together with the main server.
	var aux []*http.Server

	serve := func() error { return srv.ListenAndServe() } // plain HTTP default

	switch {
	case s.cfg.TLS.IsACME():
		mgr := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(s.cfg.TLS.ACMEDomains...),
			Cache:      autocert.DirCache(s.cfg.TLS.acmeCacheDir()),
			Email:      s.cfg.TLS.ACMEEmail,
		}
		tlsCfg := mgr.TLSConfig()
		tlsCfg.MinVersion = tls.VersionTLS12
		srv.TLSConfig = tlsCfg
		// ACME HTTP-01 challenges + redirect run on :80.
		challenge := &http.Server{
			Addr:              ":80",
			Handler:           mgr.HTTPHandler(nil),
			ReadHeaderTimeout: 10 * time.Second,
		}
		aux = append(aux, challenge)
		go func() {
			if err := challenge.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.logger.Error("acme http-01 listener stopped", "err", err)
			}
		}()
		s.logger.Info("serving https", "addr", s.cfg.ListenAddr, "mode", "acme",
			"domains", strings.Join(s.cfg.TLS.ACMEDomains, ","))
		serve = func() error { return srv.ListenAndServeTLS("", "") }

	case s.cfg.TLS.IsStaticCerts():
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		if s.cfg.TLS.RedirectHTTP != "" {
			redirect := &http.Server{
				Addr:              s.cfg.TLS.RedirectHTTP,
				Handler:           http.HandlerFunc(redirectToHTTPS),
				ReadHeaderTimeout: 10 * time.Second,
			}
			aux = append(aux, redirect)
			go func() {
				if err := redirect.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					s.logger.Error("http→https redirect listener stopped", "err", err)
				}
			}()
		}
		s.logger.Info("serving https", "addr", s.cfg.ListenAddr, "mode", "static-cert",
			"redirect_http", s.cfg.TLS.RedirectHTTP)
		serve = func() error { return srv.ListenAndServeTLS(s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile) }

	default:
		s.logger.Info("serving http", "addr", s.cfg.ListenAddr)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- serve() }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.HTTPShutdownGrace)
		defer cancel()
		for _, a := range aux {
			// Best-effort drain of auxiliary listeners; errors are non-fatal.
			_ = a.Shutdown(shutdownCtx)
		}
		return srv.Shutdown(shutdownCtx)
	}
}

// redirectToHTTPS 301-redirects a plain HTTP request to its https:// equivalent,
// preserving host (minus any :port), path, and query.
func redirectToHTTPS(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if h, _, err := splitHostPort(host); err == nil && h != "" {
		host = h
	}
	target := url.URL{Scheme: "https", Host: host, Path: r.URL.Path, RawQuery: r.URL.RawQuery}
	http.Redirect(w, r, target.String(), http.StatusMovedPermanently)
}

// splitHostPort strips a trailing :port from a Host header, tolerating a bare
// host (no port). It wraps net.SplitHostPort with a fallback so IPv6 and
// port-less hosts both work.
func splitHostPort(hostport string) (host, port string, err error) {
	if !strings.Contains(hostport, ":") {
		return hostport, "", nil
	}
	return net.SplitHostPort(hostport)
}
