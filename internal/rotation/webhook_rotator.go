package rotation

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
)

// hmacHex returns "sha256=<hex>" of body under key.
func hmacHex(key string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// signedPost POSTs body to url with an X-Janus-Signature HMAC header and
// returns an error unless the response is 2xx. The error carries only the
// status code — never the body or the signed payload.
func signedPost(ctx context.Context, hc *http.Client, url, hmacKey string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Janus-Signature", hmacHex(hmacKey, body))
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: request error", ErrApplyFailed)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: endpoint returned status %d", ErrApplyFailed, resp.StatusCode)
	}
	return nil
}

// webhookRotator pushes the new value to a configured endpoint.
type webhookRotator struct{ hc *http.Client }

func (wr webhookRotator) apply(ctx context.Context, cfg PolicyConfig, policyID, secretKey, newValue string) error {
	if cfg.URL == "" {
		return ErrInvalidConfig
	}
	body, _ := json.Marshal(map[string]any{
		"policy_id":  policyID,
		"secret_key": secretKey,
		"new_value":  newValue,
	})
	return signedPost(ctx, wr.hc, cfg.URL, cfg.HMACKey, body)
}
