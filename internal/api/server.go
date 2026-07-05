package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// Config is the api server's static configuration.
type Config struct {
	// ListenAddr defaults to ":8200".
	ListenAddr string
	// SealType is the effective seal type ("shamir" or "awskms"): the stored
	// type when initialized, otherwise the operator-configured one.
	SealType string
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
	auth     *auth.Service // nil only in unit tests that exercise no auth path
	authz    *authz.Engine // nil only in unit-test servers that exercise no authz path
	st       *store.Store  // for scope-chain resolution + membership/user handlers
	logger   *slog.Logger
	router   chi.Router
	// initMu serializes POST /v1/sys/init: the unsealer's Init is
	// get-then-put, so unserialized concurrent inits could both report
	// success while only one share set matches the stored seal.
	initMu sync.Mutex
}

// New wires the router. logger nil defaults to slog.Default().
func New(cfg Config, kr *crypto.Keyring, u crypto.Unsealer,
	seals crypto.SealConfigStore, svc *secrets.Service, authSvc *auth.Service,
	authorizer *authz.Engine, st *store.Store, logger *slog.Logger) *Server {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8200"
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{cfg: cfg, keyring: kr, unsealer: u, seals: seals, service: svc,
		auth: authSvc, authz: authorizer, st: st, logger: logger}

	r := chi.NewRouter()
	r.Use(requestLogger(logger))
	r.Use(RequireUnsealed(kr))
	r.Route("/v1/sys", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Get("/seal-status", s.handleSealStatus)
		r.Post("/init", s.handleInit)
		r.Post("/unseal", s.handleUnseal)
		r.Post("/unseal/reset", s.handleUnsealReset)
		// Production always wires a non-nil auth service (Boot does), so seal is
		// authenticated. Unit-test servers pass nil and hit the route directly.
		if s.auth != nil && s.authz != nil {
			r.With(RequireAuth(s.auth), s.requireInstance(authz.SysSeal)).Post("/seal", s.handleSeal)
		} else {
			r.Post("/seal", s.handleSeal)
		}
	})
	if s.auth != nil {
		loginLimiter := newIPRateLimiter(10.0/60.0, 5) // 10/min sustained, burst 5
		r.Route("/v1/auth", func(r chi.Router) {
			r.With(loginLimiter.middleware).Post("/login", s.handleLogin)
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
			r.Put("/{uid}", func(w http.ResponseWriter, r *http.Request) { s.memberPut(w, r, s.instanceScope(), chi.URLParam(r, "uid")) })
			r.Delete("/{uid}", func(w http.ResponseWriter, r *http.Request) { s.memberDelete(w, r, s.instanceScope(), chi.URLParam(r, "uid")) })
		})
		r.Route("/v1/projects/{pid}/members", func(r chi.Router) {
			r.Use(RequireAuth(s.auth))
			r.Get("/", func(w http.ResponseWriter, r *http.Request) { s.membersList(w, r, s.projectScope(r)) })
			r.Put("/{uid}", func(w http.ResponseWriter, r *http.Request) { s.memberPut(w, r, s.projectScope(r), chi.URLParam(r, "uid")) })
			r.Delete("/{uid}", func(w http.ResponseWriter, r *http.Request) { s.memberDelete(w, r, s.projectScope(r), chi.URLParam(r, "uid")) })
		})
		r.Route("/v1/projects/{pid}/environments/{eid}/members", func(r chi.Router) {
			r.Use(RequireAuth(s.auth))
			r.Get("/", func(w http.ResponseWriter, r *http.Request) { s.membersList(w, r, s.envScope(r)) })
			r.Put("/{uid}", func(w http.ResponseWriter, r *http.Request) { s.memberPut(w, r, s.envScope(r), chi.URLParam(r, "uid")) })
			r.Delete("/{uid}", func(w http.ResponseWriter, r *http.Request) { s.memberDelete(w, r, s.envScope(r), chi.URLParam(r, "uid")) })
		})
	}
	s.router = r
	return s
}

// Handler exposes the router (used by tests and ListenAndServe).
func (s *Server) Handler() http.Handler { return s.router }

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
