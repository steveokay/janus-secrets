package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestValidateCIDRs(t *testing.T) {
	ok := []struct {
		in   []string
		want []string
	}{
		{nil, []string{}},
		{[]string{}, []string{}},
		{[]string{"10.0.0.0/8"}, []string{"10.0.0.0/8"}},
		{[]string{" 192.0.2.0/24 "}, []string{"192.0.2.0/24"}},          // trimmed
		{[]string{"192.0.2.7/24"}, []string{"192.0.2.0/24"}},            // canonicalized to network
		{[]string{"2001:db8::/32"}, []string{"2001:db8::/32"}},          // IPv6
		{[]string{"10.0.0.0/8", "2001:db8::/32"}, []string{"10.0.0.0/8", "2001:db8::/32"}},
	}
	for _, c := range ok {
		got, err := ValidateCIDRs(c.in)
		if err != nil {
			t.Fatalf("ValidateCIDRs(%v) err=%v", c.in, err)
		}
		if len(got) != len(c.want) {
			t.Fatalf("ValidateCIDRs(%v) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("ValidateCIDRs(%v)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
	bad := [][]string{
		{"not-a-cidr"},
		{"10.0.0.0"},   // missing prefix length
		{"10.0.0.0/33"}, // out of range
		{""},            // empty entry
		{"10.0.0.0/8", "garbage"},
	}
	for _, c := range bad {
		if _, err := ValidateCIDRs(c); !errors.Is(err, ErrValidation) {
			t.Fatalf("ValidateCIDRs(%v) err=%v, want ErrValidation", c, err)
		}
	}
}

func TestMintWithIPAllowlist(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()
	cookie, _ := svc.Login(ctx, email, []byte(password), "")
	admin, _ := svc.VerifySession(ctx, cookie)
	_, configID := mkScope(t)

	// Mint with a valid allowlist; verify it round-trips through mint + verify.
	raw, meta, err := svc.MintServiceToken(ctx, admin, "ci", "config", configID, "read", nil,
		[]string{"10.0.0.0/8", "192.0.2.7/24"})
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.IPAllowlist) != 2 || meta.IPAllowlist[0] != "10.0.0.0/8" || meta.IPAllowlist[1] != "192.0.2.0/24" {
		t.Fatalf("meta.IPAllowlist = %v", meta.IPAllowlist)
	}
	_, scope, err := svc.VerifyServiceToken(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(scope.IPAllowlist) != 2 || scope.IPAllowlist[0] != "10.0.0.0/8" {
		t.Fatalf("scope.IPAllowlist = %v", scope.IPAllowlist)
	}

	// A bad CIDR is rejected at mint.
	if _, _, err := svc.MintServiceToken(ctx, admin, "bad", "config", configID, "read", nil,
		[]string{"not-a-cidr"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("mint with bad CIDR: %v", err)
	}

	// SetTokenIPAllowlist replaces, then clears.
	if err := svc.SetTokenIPAllowlist(ctx, meta.ID, []string{"172.16.0.0/12"}); err != nil {
		t.Fatal(err)
	}
	_, scope, _ = svc.VerifyServiceToken(ctx, raw)
	if len(scope.IPAllowlist) != 1 || scope.IPAllowlist[0] != "172.16.0.0/12" {
		t.Fatalf("after set: %v", scope.IPAllowlist)
	}
	if err := svc.SetTokenIPAllowlist(ctx, meta.ID, nil); err != nil {
		t.Fatal(err)
	}
	_, scope, _ = svc.VerifyServiceToken(ctx, raw)
	if len(scope.IPAllowlist) != 0 {
		t.Fatalf("after clear: %v", scope.IPAllowlist)
	}
	// Set on a missing token → ErrNotFound.
	if err := svc.SetTokenIPAllowlist(ctx, "00000000-0000-0000-0000-000000000000", []string{"10.0.0.0/8"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set missing: %v", err)
	}
	// Set with a bad CIDR → ErrValidation.
	if err := svc.SetTokenIPAllowlist(ctx, meta.ID, []string{"garbage"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("set bad cidr: %v", err)
	}
}

func TestNoteTokenIP(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()
	cookie, _ := svc.Login(ctx, email, []byte(password), "")
	admin, _ := svc.VerifySession(ctx, cookie)
	_, configID := mkScope(t)
	_, meta, err := svc.MintServiceToken(ctx, admin, "ci", "config", configID, "read", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// First sighting of an IP → new.
	isNew, err := svc.NoteTokenIP(ctx, meta.ID, "203.0.113.9")
	if err != nil || !isNew {
		t.Fatalf("first note: isNew=%v err=%v", isNew, err)
	}
	// Repeat sighting → not new (throttled by the PK).
	isNew, err = svc.NoteTokenIP(ctx, meta.ID, "203.0.113.9")
	if err != nil || isNew {
		t.Fatalf("repeat note: isNew=%v err=%v", isNew, err)
	}
	// A different IP → new again.
	isNew, err = svc.NoteTokenIP(ctx, meta.ID, "198.51.100.4")
	if err != nil || !isNew {
		t.Fatalf("second ip: isNew=%v err=%v", isNew, err)
	}

	// The recent-new-IP aggregate counts both first-sightings.
	n, err := svc.CountRecentNewIPs(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("CountRecentNewIPs = %d, want 2", n)
	}
	// A zero window sees nothing recent.
	n, err = svc.CountRecentNewIPs(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("CountRecentNewIPs(0) = %d, want 0", n)
	}
}
