package api

import (
	"fmt"
	"testing"
)

// TestTokenLastUsedAndUserLastLoginE2E verifies the new value-free timestamp
// fields surface through the REST API: GET /v1/tokens carries last_used_at
// (populated once the token authenticates) and GET /v1/users carries
// last_login_at (populated once the user logs in).
func TestTokenLastUsedAndUserLastLoginE2E(t *testing.T) {
	ts, email, password, configID := authStack(t)
	cookie := login(t, ts.URL, email, password)

	// After a password login, the admin's last_login_at is set.
	var users struct {
		Users []struct {
			ID          string  `json:"id"`
			Email       string  `json:"email"`
			LastLoginAt *string `json:"last_login_at"`
		} `json:"users"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/users", cookie, "", "", &users); code != 200 {
		t.Fatalf("list users: %d", code)
	}
	var adminLogin *string
	for _, u := range users.Users {
		if u.Email == email {
			adminLogin = u.LastLoginAt
		}
	}
	if adminLogin == nil || *adminLogin == "" {
		t.Fatalf("admin last_login_at should be set after login, got %v", adminLogin)
	}

	// Mint a token; before use, last_used_at is absent (omitempty → nil).
	var minted struct {
		Token string `json:"token"`
		ID    string `json:"id"`
	}
	body := fmt.Sprintf(`{"name":"ci","scope":{"kind":"config","id":%q},"access":"read"}`, configID)
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", cookie, "", body, &minted); code != 200 {
		t.Fatalf("mint: %d", code)
	}

	type tokRow struct {
		ID         string  `json:"id"`
		LastUsedAt *string `json:"last_used_at"`
	}
	listTokens := func() []tokRow {
		var out struct {
			Tokens []tokRow `json:"tokens"`
		}
		if code := doAuthed(t, "GET", ts.URL+"/v1/tokens", cookie, "", "", &out); code != 200 {
			t.Fatalf("list tokens: %d", code)
		}
		return out.Tokens
	}

	for _, tk := range listTokens() {
		if tk.ID == minted.ID && tk.LastUsedAt != nil {
			t.Fatalf("pre-use token should have nil last_used_at, got %v", *tk.LastUsedAt)
		}
	}

	// Authenticate with the token → last_used_at is stamped.
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", "", minted.Token, "", nil); code != 200 {
		t.Fatalf("token auth: %d", code)
	}
	found := false
	for _, tk := range listTokens() {
		if tk.ID == minted.ID {
			found = true
			if tk.LastUsedAt == nil || *tk.LastUsedAt == "" {
				t.Fatal("token last_used_at not set after authentication")
			}
		}
	}
	if !found {
		t.Fatalf("minted token %s not in list", minted.ID)
	}
}
