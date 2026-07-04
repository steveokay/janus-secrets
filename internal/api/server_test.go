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
		crypto.NewKeyring(), crypto.NewShamirUnsealer(seals, 0, 0), seals, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

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
