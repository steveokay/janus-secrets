package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

func TestGracefulShutdown(t *testing.T) {
	seals := &memSealStore{}
	srv := New(Config{ListenAddr: "127.0.0.1:0", SealType: crypto.SealTypeShamir},
		crypto.NewKeyring(), crypto.NewShamirUnsealer(seals, 0, 0), seals, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// ListenAddr :0 picks a free port; we only assert the lifecycle: serve,
	// cancel, clean return.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe(ctx) }()
	time.Sleep(100 * time.Millisecond) // let it start listening
	cancel()
	select {
	case err := <-done:
		if err != nil && err != http.ErrServerClosed {
			t.Fatalf("shutdown returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within 5s")
	}
}

func TestBuildHTTPServer_Timeouts(t *testing.T) {
	s := &Server{cfg: Config{
		ListenAddr:       ":9999",
		HTTPReadTimeout:  15 * time.Second,
		HTTPWriteTimeout: 0,
		HTTPIdleTimeout:  90 * time.Second,
	}}
	srv := s.buildHTTPServer()
	if srv.ReadTimeout != 15*time.Second || srv.IdleTimeout != 90*time.Second || srv.WriteTimeout != 0 {
		t.Fatalf("timeouts: read=%v write=%v idle=%v", srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout)
	}
	if srv.ReadHeaderTimeout != 10*time.Second {
		t.Fatalf("ReadHeaderTimeout should stay 10s, got %v", srv.ReadHeaderTimeout)
	}
	if srv.Addr != ":9999" {
		t.Fatalf("addr = %q", srv.Addr)
	}
}
