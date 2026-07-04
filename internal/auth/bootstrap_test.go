package auth

import (
	"context"
	"errors"
	"testing"
)

// TestCreateInitialAdminGuard covers the bootstrap guard: exactly one admin
// may be created at init. newTestService already exercised the create branch,
// so a second call must be rejected.
func TestCreateInitialAdminGuard(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	if _, _, err := svc.CreateInitialAdmin(ctx, "second@example.com"); !errors.Is(err, ErrValidation) {
		t.Fatalf("second admin should be rejected, got %v", err)
	}
}
