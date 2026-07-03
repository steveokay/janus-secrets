package store

import (
	"context"
	"testing"
)

func TestOpenAndPing(t *testing.T) {
	s := requireStore(t)
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
