package secretsync

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"golang.org/x/crypto/nacl/box"
)

// ghSecretNameRe is GitHub's Actions-secret name rule: letters/digits/underscore,
// not starting with a digit. (GitHub also reserves the GITHUB_ prefix.)
var ghSecretNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validGitHubSecretName(k string) bool {
	return ghSecretNameRe.MatchString(k) && len(k) <= 100 && !hasPrefixFold(k, "GITHUB_")
}

// hasPrefixFold reports whether s begins with p, case-insensitively (ASCII).
func hasPrefixFold(s, p string) bool {
	if len(s) < len(p) {
		return false
	}
	for i := 0; i < len(p); i++ {
		a, b := s[i], p[i]
		if 'a' <= a && a <= 'z' {
			a -= 32
		}
		if 'a' <= b && b <= 'z' {
			b -= 32
		}
		if a != b {
			return false
		}
	}
	return true
}

type githubProvider struct {
	hc      *http.Client
	baseURL string // "https://api.github.com" in prod; overridden by tests
}

func (githubProvider) Name() string { return ProviderGitHub }

// secretsPath returns the repo- or environment-scoped secrets base path.
func (g githubProvider) secretsPath(a Addr) string {
	if a.Environment != "" {
		return fmt.Sprintf("/repos/%s/%s/environments/%s/secrets", a.Owner, a.Repo, a.Environment)
	}
	return fmt.Sprintf("/repos/%s/%s/actions/secrets", a.Owner, a.Repo)
}

type ghPublicKey struct {
	KeyID string `json:"key_id"`
	Key   string `json:"key"` // base64 32-byte NaCl public key
}

func (g githubProvider) Apply(ctx context.Context, creds Creds, addr Addr, desired map[string]string,
	managedKeys []string, prune bool) (ApplyResult, error) {
	if creds.PAT == "" || addr.Owner == "" || addr.Repo == "" {
		return ApplyResult{}, ErrInvalidConfig
	}
	base := g.baseURL + g.secretsPath(addr)

	pk, err := g.publicKey(ctx, creds.PAT, base)
	if err != nil {
		return ApplyResult{}, err
	}
	recipient, err := decodeKey(pk.Key)
	if err != nil {
		return ApplyResult{}, ErrApplyFailed
	}

	res := ApplyResult{Skipped: map[string]string{}}
	for name, val := range desired {
		if !validGitHubSecretName(name) {
			res.Skipped[name] = "invalid github secret name"
			continue
		}
		enc, err := sealBox(recipient, []byte(val))
		if err != nil {
			return res, ErrApplyFailed
		}
		if err := g.putSecret(ctx, creds.PAT, base, name, enc, pk.KeyID); err != nil {
			return res, err
		}
		res.Applied = append(res.Applied, name)
	}

	if prune {
		desiredSet := map[string]bool{}
		for _, k := range res.Applied {
			desiredSet[k] = true
		}
		for _, k := range managedKeys {
			if !desiredSet[k] && validGitHubSecretName(k) {
				if err := g.deleteSecret(ctx, creds.PAT, base, k); err != nil {
					return res, err
				}
			}
		}
	}
	return res, nil
}

// sealBox encrypts value as a libsodium sealed box under recipient (GitHub's format).
func sealBox(recipient *[32]byte, value []byte) (string, error) {
	sealed, err := box.SealAnonymous(nil, value, recipient, rand.Reader)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func decodeKey(b64 string) (*[32]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("bad key")
	}
	var k [32]byte
	copy(k[:], raw)
	return &k, nil
}

func (g githubProvider) publicKey(ctx context.Context, pat, base string) (ghPublicKey, error) {
	var pk ghPublicKey
	if err := g.doJSON(ctx, http.MethodGet, pat, base+"/public-key", nil, &pk); err != nil {
		return ghPublicKey{}, err
	}
	return pk, nil
}

func (g githubProvider) putSecret(ctx context.Context, pat, base, name, encVal, keyID string) error {
	body, _ := json.Marshal(map[string]string{"encrypted_value": encVal, "key_id": keyID})
	return g.doJSON(ctx, http.MethodPut, pat, base+"/"+name, body, nil)
}

func (g githubProvider) deleteSecret(ctx context.Context, pat, base, name string) error {
	return g.doJSON(ctx, http.MethodDelete, pat, base+"/"+name, nil, nil)
}

// doJSON performs an authenticated GitHub API call. Errors carry only the status
// code — never the PAT, body, or secret value.
func (g githubProvider) doJSON(ctx context.Context, method, pat, url string, body []byte, out any) error {
	var r *bytes.Reader
	if body != nil {
		r = bytes.NewReader(body)
	} else {
		r = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: request error", ErrApplyFailed)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: github status %d", ErrApplyFailed, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
