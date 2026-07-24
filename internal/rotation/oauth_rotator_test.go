package rotation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// obviously-fake, low-entropy fixtures (a secret scanner flags high-entropy
// strings; keep these clearly non-real).
const (
	fakeOAuthClientID     = "test-client-id"
	fakeOAuthClientSecret = "test-client-secret-xxxx"
	fakeOAuthToken        = "test-access-token-yyyy"
)

func TestOAuthGenerateReturnsAccessToken(t *testing.T) {
	var gotForm string
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		gotForm = r.PostForm.Encode()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"` + fakeOAuthToken + `","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	rot := oauthRotator{hc: srv.Client()}
	cfg := PolicyConfig{
		OAuthTokenURL:     srv.URL,
		OAuthClientID:     fakeOAuthClientID,
		OAuthClientSecret: fakeOAuthClientSecret,
		OAuthScope:        "read write",
		OAuthAudience:     "test-api",
	}
	val, err := rot.generate(context.Background(), cfg, "pol", "TOKEN")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if val != fakeOAuthToken {
		t.Fatalf("value = %q, want %q", val, fakeOAuthToken)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want form-urlencoded", gotContentType)
	}
	// Assert the request was a proper client-credentials form body.
	for _, want := range []string{
		"grant_type=client_credentials",
		"client_id=" + fakeOAuthClientID,
		"client_secret=" + fakeOAuthClientSecret,
		"scope=read+write",
		"audience=test-api",
	} {
		if !strings.Contains(gotForm, want) {
			t.Errorf("form body %q missing %q", gotForm, want)
		}
	}
}

func TestOAuthGenerateOmitsOptionalParams(t *testing.T) {
	var gotForm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm.Encode()
		_, _ = w.Write([]byte(`{"access_token":"` + fakeOAuthToken + `"}`))
	}))
	defer srv.Close()

	rot := oauthRotator{hc: srv.Client()}
	cfg := PolicyConfig{OAuthTokenURL: srv.URL, OAuthClientID: fakeOAuthClientID, OAuthClientSecret: fakeOAuthClientSecret}
	if _, err := rot.generate(context.Background(), cfg, "pol", "TOKEN"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if strings.Contains(gotForm, "scope=") || strings.Contains(gotForm, "audience=") {
		t.Errorf("optional params must be omitted when empty, got %q", gotForm)
	}
}

func TestOAuthGenerateRejectsBadConfig(t *testing.T) {
	rot := oauthRotator{hc: http.DefaultClient}
	cases := []struct {
		name string
		cfg  PolicyConfig
	}{
		{"no url", PolicyConfig{OAuthClientID: "a", OAuthClientSecret: "b"}},
		{"bad scheme", PolicyConfig{OAuthTokenURL: "ftp://x/y", OAuthClientID: "a", OAuthClientSecret: "b"}},
		{"no host", PolicyConfig{OAuthTokenURL: "https://", OAuthClientID: "a", OAuthClientSecret: "b"}},
		{"no client id", PolicyConfig{OAuthTokenURL: "https://x/t", OAuthClientSecret: "b"}},
		{"no client secret", PolicyConfig{OAuthTokenURL: "https://x/t", OAuthClientID: "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := rot.generate(context.Background(), tc.cfg, "p", "K"); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestOAuthGenerateNon2xxSanitized(t *testing.T) {
	// The endpoint echoes the client_secret back in an error body; it must NOT
	// leak into the returned error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client","secret_echo":"` + fakeOAuthClientSecret + `"}`))
	}))
	defer srv.Close()

	rot := oauthRotator{hc: srv.Client()}
	cfg := PolicyConfig{OAuthTokenURL: srv.URL, OAuthClientID: fakeOAuthClientID, OAuthClientSecret: fakeOAuthClientSecret}
	_, err := rot.generate(context.Background(), cfg, "p", "K")
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("err = %v, want ErrApplyFailed", err)
	}
	if strings.Contains(err.Error(), fakeOAuthClientSecret) {
		t.Fatalf("error leaked client_secret: %v", err)
	}
}

func TestOAuthGenerateMissingTokenIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"token_type":"Bearer"}`)) // no access_token
	}))
	defer srv.Close()

	rot := oauthRotator{hc: srv.Client()}
	cfg := PolicyConfig{OAuthTokenURL: srv.URL, OAuthClientID: fakeOAuthClientID, OAuthClientSecret: fakeOAuthClientSecret}
	if _, err := rot.generate(context.Background(), cfg, "p", "K"); !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("err = %v, want ErrApplyFailed", err)
	}
}

// oauthRotator must be a generating rotator, never an applier.
func TestOAuthImplementsGeneratorNotApplier(t *testing.T) {
	var rot any = oauthRotator{}
	if _, ok := rot.(rotatorGenerator); !ok {
		t.Fatal("oauthRotator must implement rotatorGenerator")
	}
	if _, ok := rot.(rotatorApplier); ok {
		t.Fatal("oauthRotator must NOT implement rotatorApplier")
	}
}
