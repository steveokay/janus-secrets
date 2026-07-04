package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
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
	logger   *slog.Logger
	router   chi.Router
	// initMu serializes POST /v1/sys/init: the unsealer's Init is
	// get-then-put, so unserialized concurrent inits could both report
	// success while only one share set matches the stored seal.
	initMu sync.Mutex
}

// New wires the router. logger nil defaults to slog.Default().
func New(cfg Config, kr *crypto.Keyring, u crypto.Unsealer,
	seals crypto.SealConfigStore, svc *secrets.Service, logger *slog.Logger) *Server {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8200"
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{cfg: cfg, keyring: kr, unsealer: u, seals: seals, service: svc, logger: logger}

	r := chi.NewRouter()
	r.Use(requestLogger(logger))
	r.Use(RequireUnsealed(kr))
	r.Route("/v1/sys", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Get("/seal-status", s.handleSealStatus)
		r.Post("/init", s.handleInit)
		r.Post("/unseal", s.handleUnseal)
		r.Post("/unseal/reset", s.handleUnsealReset)
		r.Post("/seal", s.handleSeal)
	})
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
