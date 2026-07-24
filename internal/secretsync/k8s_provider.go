package secretsync

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/steveokay/janus-secrets/internal/nethard"
)

// k8sTimeout bounds a k8s API request end-to-end. The provider previously had
// NO timeout and NO redirect control; both are added here.
const k8sTimeout = 20 * time.Second

type k8sProvider struct {
	policy nethard.Policy
	// newClient builds an HTTP client that trusts caPEM (overridable in tests).
	newClient func(caPEM string) (*http.Client, error)
}

func (k8sProvider) Name() string { return ProviderK8s }

// defaultK8sClient returns a client that verifies the API server against caPEM,
// dials through the SSRF guard (blocks link-local/IMDS; loopback + RFC1918
// allowed for in-cluster/self-hosted API servers), bounds redirects, and has a
// request timeout.
func defaultK8sClient(policy nethard.Policy, caPEM string) (*http.Client, error) {
	pool := x509.NewCertPool()
	if caPEM != "" {
		if !pool.AppendCertsFromPEM([]byte(caPEM)) {
			return nil, ErrInvalidConfig
		}
	}
	hc := nethard.SafeHTTPClient(k8sTimeout, policy)
	// Preserve the custom CA verification on the guarded transport.
	if tr, ok := hc.Transport.(*http.Transport); ok {
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return hc, nil
}

func (p k8sProvider) client(caPEM string) (*http.Client, error) {
	if p.newClient != nil {
		return p.newClient(caPEM)
	}
	return defaultK8sClient(p.policy, caPEM)
}

func (p k8sProvider) Apply(ctx context.Context, creds Creds, addr Addr, desired map[string]string,
	managedKeys []string, prune bool) (ApplyResult, error) {
	if creds.APIURL == "" || creds.Token == "" || addr.Namespace == "" || addr.SecretName == "" {
		return ApplyResult{}, ErrInvalidConfig
	}
	hc, err := p.client(creds.CACert)
	if err != nil {
		return ApplyResult{}, err
	}

	data := make(map[string]string, len(desired))
	applied := make([]string, 0, len(desired))
	for k, v := range desired {
		data[k] = base64.StdEncoding.EncodeToString([]byte(v))
		applied = append(applied, k)
	}
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]any{"name": addr.SecretName, "namespace": addr.Namespace},
		"type":       "Opaque",
		"data":       data,
	}
	body, _ := json.Marshal(obj)

	url := fmt.Sprintf("%s/api/v1/namespaces/%s/secrets/%s", creds.APIURL, addr.Namespace, addr.SecretName)
	contentType := "application/apply-patch+yaml"
	if prune {
		url += "?fieldManager=janus&force=true"
	} else {
		contentType = "application/merge-patch+json"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return ApplyResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+creds.Token)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("%w: request error", ErrApplyFailed)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ApplyResult{}, fmt.Errorf("%w: k8s status %d", ErrApplyFailed, resp.StatusCode)
	}
	_ = managedKeys // SSA handles prune server-side; managedKeys unused for k8s
	return ApplyResult{Applied: applied, Skipped: map[string]string{}}, nil
}
