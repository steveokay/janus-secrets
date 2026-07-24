package rotation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// validateTokenURL enforces an absolute http(s) URL for the OAuth token
// endpoint. This guards gosec G107 (the URL is operator-supplied config, not
// attacker-derived) and fails fast on a malformed value as invalid config.
func validateTokenURL(u string) error {
	parsed, err := url.Parse(u)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ErrInvalidConfig
	}
	return nil
}

// oauthTokenResponse is the subset of an RFC 6749 token response we parse. The
// access_token becomes the rotated secret value; expires_in is advisory only.
type oauthTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// oauthRotator is a GENERATING rotator: the external OAuth provider mints the
// new credential via the client-credentials grant and Janus stores the returned
// access_token as the secret value. It implements rotatorGenerator, not
// rotatorApplier — the token acquisition IS the external side effect, so there
// is no separate apply-a-locally-generated-value step.
type oauthRotator struct{ hc *http.Client }

// generate performs an RFC 6749 §4.4 client-credentials grant against the
// configured token_url and returns the access_token as the new secret value.
// The client_secret and the returned token are NEVER placed in an error string
// or logged; failures carry only a fixed category.
func (or oauthRotator) generate(ctx context.Context, cfg PolicyConfig, policyID, secretKey string) (string, error) {
	if err := validateTokenURL(cfg.OAuthTokenURL); err != nil {
		return "", err
	}
	if cfg.OAuthClientID == "" || cfg.OAuthClientSecret == "" {
		return "", ErrInvalidConfig
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", cfg.OAuthClientID)
	form.Set("client_secret", cfg.OAuthClientSecret)
	if cfg.OAuthScope != "" {
		form.Set("scope", cfg.OAuthScope)
	}
	if cfg.OAuthAudience != "" {
		form.Set("audience", cfg.OAuthAudience)
	}

	// #nosec G107 -- OAuthTokenURL is operator-supplied config, validated above
	// by validateTokenURL to be an absolute http(s) URL; it is not attacker-derived.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.OAuthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("%w: request build failed", ErrApplyFailed)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	hc := or.hc
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		// A transport error (e.g. url.Error) can echo the token_url with the
		// signed query; never surface it.
		return "", fmt.Errorf("%w: token request failed", ErrApplyFailed)
	}
	defer resp.Body.Close()

	// Cap the body so a hostile/misbehaving endpoint can't stream unbounded.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("%w: token response read failed", ErrApplyFailed)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The error body may echo the client_secret back; carry only the status.
		return "", fmt.Errorf("%w: token endpoint returned status %d", ErrApplyFailed, resp.StatusCode)
	}

	var tok oauthTokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("%w: token response parse failed", ErrApplyFailed)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("%w: token response missing access_token", ErrApplyFailed)
	}
	return tok.AccessToken, nil
}
