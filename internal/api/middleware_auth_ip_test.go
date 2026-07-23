package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/steveokay/janus-secrets/internal/auth"
)

func TestClientIP(t *testing.T) {
	cases := []struct{ remote, want string }{
		{"10.0.0.1:12345", "10.0.0.1"},                 // IPv4 host:port → host
		{"[2001:db8::1]:443", "2001:db8::1"},           // IPv6 bracketed host:port → host
		{"192.168.1.5", "192.168.1.5"},                 // no port → returned verbatim
		{"", ""},                                       // empty → empty
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if got := clientIP(r); got != c.want {
			t.Errorf("clientIP(%q) = %q, want %q", c.remote, got, c.want)
		}
	}
}

func TestIPAllowed(t *testing.T) {
	cases := []struct {
		name  string
		ip    string
		allow []string
		want  bool
	}{
		{"empty allowlist allows any", "203.0.113.9", nil, true},
		{"empty slice allows any", "203.0.113.9", []string{}, true},
		{"v4 in cidr", "10.1.2.3", []string{"10.1.0.0/16"}, true},
		{"v4 outside cidr", "10.9.2.3", []string{"10.1.0.0/16"}, false},
		{"v4 exact /32", "192.0.2.7", []string{"192.0.2.7/32"}, true},
		{"v6 in cidr", "2001:db8::5", []string{"2001:db8::/32"}, true},
		{"v6 outside cidr", "2001:dead::5", []string{"2001:db8::/32"}, false},
		{"multiple cidrs any match", "172.16.0.1", []string{"10.0.0.0/8", "172.16.0.0/12"}, true},
		{"unparseable ip denied when allowlist set", "not-an-ip", []string{"10.0.0.0/8"}, false},
		{"bad cidr skipped, no other match", "10.0.0.1", []string{"garbage"}, false},
	}
	for _, c := range cases {
		if got := ipAllowed(c.ip, c.allow); got != c.want {
			t.Errorf("%s: ipAllowed(%q,%v) = %v, want %v", c.name, c.ip, c.allow, got, c.want)
		}
	}
}

// recordingHook captures recordTokenIP calls for assertion.
type recordingHook struct {
	calls []struct{ tokenID, ip string }
}

func (h *recordingHook) recordTokenIP(_ *http.Request, tokenID, ip string) {
	h.calls = append(h.calls, struct{ tokenID, ip string }{tokenID, ip})
}

func tokenReq(remote string) *http.Request {
	req := httptest.NewRequest("GET", "/v1/secrets", nil)
	req.Header.Set("Authorization", "Bearer janus_svc_x")
	req.RemoteAddr = remote
	return req
}

func TestRequireAuth_TokenIPAllowlist_Allow(t *testing.T) {
	svcPrincipal := auth.Principal{Kind: auth.KindServiceToken, ID: "tok1", Name: "ci"}
	hook := &recordingHook{}
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RequireAuth(&fakeVerifier{
		principal: svcPrincipal,
		scope:     &auth.TokenScope{Kind: "config", ID: "c1", Access: "read", IPAllowlist: []string{"10.0.0.0/8"}},
	}, hook)(probe)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, tokenReq("10.1.2.3:5555"))
	if rec.Code != http.StatusOK {
		t.Fatalf("in-allowlist: %d, want 200", rec.Code)
	}
	// New-IP hook fired with stripped host.
	if len(hook.calls) != 1 || hook.calls[0].tokenID != "tok1" || hook.calls[0].ip != "10.1.2.3" {
		t.Fatalf("hook calls = %+v", hook.calls)
	}
}

func TestRequireAuth_TokenIPAllowlist_Deny(t *testing.T) {
	svcPrincipal := auth.Principal{Kind: auth.KindServiceToken, ID: "tok1", Name: "ci"}
	hook := &recordingHook{}
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run for a denied IP")
	})
	h := RequireAuth(&fakeVerifier{
		principal: svcPrincipal,
		scope:     &auth.TokenScope{Kind: "config", ID: "c1", Access: "read", IPAllowlist: []string{"10.0.0.0/8"}},
	}, hook)(probe)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, tokenReq("203.0.113.9:5555"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("outside-allowlist: %d, want 403", rec.Code)
	}
	// A denied request must NOT record a new-IP sighting.
	if len(hook.calls) != 0 {
		t.Fatalf("denied request recorded a sighting: %+v", hook.calls)
	}
}

func TestRequireAuth_EmptyAllowlist_AllowsAny(t *testing.T) {
	svcPrincipal := auth.Principal{Kind: auth.KindServiceToken, ID: "tok1", Name: "ci"}
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RequireAuth(&fakeVerifier{
		principal: svcPrincipal,
		scope:     &auth.TokenScope{Kind: "config", ID: "c1", Access: "read"}, // no allowlist
	})(probe)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, tokenReq("203.0.113.9:5555"))
	if rec.Code != http.StatusOK {
		t.Fatalf("empty allowlist should allow: %d", rec.Code)
	}
}

// The IP check applies to TOKENS only: a session (cookie) principal with no
// token scope is never subject to allowlist enforcement.
func TestRequireAuth_SessionNotSubjectToIPCheck(t *testing.T) {
	userPrincipal := auth.Principal{Kind: auth.KindUser, ID: "u1", Name: "a@b.c"}
	hook := &recordingHook{}
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	// scope is nil for session auth; even if a stray allowlist existed it would
	// not apply because scope==nil and Kind!=service_token.
	h := RequireAuth(&fakeVerifier{principal: userPrincipal}, hook)(probe)

	req := httptest.NewRequest("GET", "/v1/projects", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "cookievalue"})
	req.RemoteAddr = "203.0.113.9:5555"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("session auth: %d, want 200", rec.Code)
	}
	if len(hook.calls) != 0 {
		t.Fatalf("session auth recorded a token IP sighting: %+v", hook.calls)
	}
}
