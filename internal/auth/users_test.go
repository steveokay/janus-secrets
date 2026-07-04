package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCreateAndListUsers(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	id, pw, err := svc.CreateUser(ctx, "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if id == "" || len(pw) < 16 {
		t.Fatalf("id=%q pw=%q", id, pw)
	}
	// The generated password authenticates.
	if _, err := svc.Login(ctx, "member@example.com", []byte(pw)); err != nil {
		t.Fatalf("login new user: %v", err)
	}

	users, err := svc.ListUsers(ctx)
	if err != nil || len(users) < 2 { // bootstrap admin + the new member
		t.Fatalf("list: %d (err %v)", len(users), err)
	}

	// Disable → login fails.
	if err := svc.DisableUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, "member@example.com", []byte(pw)); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled login: %v", err)
	}
}

func TestCreateInitialAdminReturnsID(t *testing.T) {
	// newTestService already calls CreateInitialAdmin; assert it now yields a
	// non-empty id via a fresh reset.
	svc, email, _ := newTestService(t)
	ctx := context.Background()
	u, err := svc.userByEmailForTest(ctx, email)
	if err != nil || u == "" {
		t.Fatalf("admin id: %q (err %v)", u, err)
	}
	if !strings.Contains(email, "@") {
		t.Fatalf("email %q", email)
	}
}
