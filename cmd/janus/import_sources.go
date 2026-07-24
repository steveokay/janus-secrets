package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// importSource identifies which external system to read from.
type importSource string

const (
	sourceDoppler importSource = "doppler"
	sourceVault   importSource = "vault"
	sourceAWSSM   importSource = "aws-sm"
)

// fetchedSecrets is the source-agnostic result of reading an external system:
// an ordered set of key→value pairs. Values live only in memory and are never
// logged or printed; only the keys are ever surfaced (dry-run, summaries).
type fetchedSecrets struct {
	pairs map[string]string
}

// keys returns the fetched key names sorted for stable, value-free output.
func (f fetchedSecrets) keys() []string {
	ks := make([]string, 0, len(f.pairs))
	for k := range f.pairs {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// importHTTP is the shared net/http client for the Doppler and Vault fetchers.
// A short timeout keeps a hung source from stalling the CLI.
var importHTTP = &http.Client{Timeout: 30 * time.Second}

// validateSourceURL parses an operator-supplied base URL (Vault addr) and
// rejects anything that is not an absolute http(s) URL. gosec G107 flags
// variable URLs in http.Get; validating here bounds the input.
func validateSourceURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("address must be http(s): %q", raw)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("address is missing a host: %q", raw)
	}
	return u, nil
}

// dopplerConfig carries the flags needed to read one Doppler config's secrets.
type dopplerConfig struct {
	token   string // Doppler service token (from flag/env; never logged)
	project string
	config  string
	apiBase string // override for tests; defaults to https://api.doppler.com
}

// fetchDoppler reads a Doppler config's secrets via the Doppler REST API
// (GET /v3/configs/config/secrets). It returns the RAW computed values keyed by
// the Doppler secret name. Errors are value-free (they never echo a value).
func fetchDoppler(ctx context.Context, dc dopplerConfig) (fetchedSecrets, error) {
	if dc.token == "" {
		return fetchedSecrets{}, fmt.Errorf("doppler: a service token is required (--token or DOPPLER_TOKEN)")
	}
	if dc.project == "" || dc.config == "" {
		return fetchedSecrets{}, fmt.Errorf("doppler: --doppler-project and --doppler-config are required")
	}
	base := dc.apiBase
	if base == "" {
		base = "https://api.doppler.com"
	}
	u, err := validateSourceURL(base)
	if err != nil {
		return fetchedSecrets{}, err
	}
	q := url.Values{}
	q.Set("project", dc.project)
	q.Set("config", dc.config)
	endpoint := u.String() + "/v3/configs/config/secrets?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fetchedSecrets{}, err
	}
	// Doppler accepts the service token as HTTP Basic username (no password).
	req.SetBasicAuth(dc.token, "")
	req.Header.Set("Accept", "application/json")

	resp, err := importHTTP.Do(req)
	if err != nil {
		return fetchedSecrets{}, fmt.Errorf("doppler: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fetchedSecrets{}, fmt.Errorf("doppler: API returned HTTP %d", resp.StatusCode)
	}

	// The response nests each secret under {"secrets": {"KEY": {"computed": "..."}}}.
	var body struct {
		Secrets map[string]struct {
			Computed string `json:"computed"`
			Raw      string `json:"raw"`
		} `json:"secrets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fetchedSecrets{}, fmt.Errorf("doppler: decoding response: %w", err)
	}
	pairs := make(map[string]string, len(body.Secrets))
	for k, v := range body.Secrets {
		val := v.Raw
		if val == "" {
			val = v.Computed
		}
		pairs[k] = val
	}
	return fetchedSecrets{pairs: pairs}, nil
}

// vaultConfig carries the flags needed to read a Vault KV v2 path.
type vaultConfig struct {
	addr  string // Vault base address, e.g. https://vault.example:8200
	token string // Vault token (from flag/env; never logged)
	mount string // KV v2 mount, default "secret"
	path  string // secret path under the mount, e.g. "myapp/prod"
}

// fetchVault reads a KV v2 secret via GET {addr}/v1/{mount}/data/{path}. The KV
// v2 payload nests the key/value map under data.data. Non-string leaf values
// are JSON-encoded so they survive as Janus string secrets.
func fetchVault(ctx context.Context, vc vaultConfig) (fetchedSecrets, error) {
	if vc.token == "" {
		return fetchedSecrets{}, fmt.Errorf("vault: a token is required (--vault-token or VAULT_TOKEN)")
	}
	if vc.addr == "" {
		return fetchedSecrets{}, fmt.Errorf("vault: --vault-addr is required (or VAULT_ADDR)")
	}
	if vc.path == "" {
		return fetchedSecrets{}, fmt.Errorf("vault: --vault-path is required")
	}
	mount := vc.mount
	if mount == "" {
		mount = "secret"
	}
	u, err := validateSourceURL(vc.addr)
	if err != nil {
		return fetchedSecrets{}, err
	}
	endpoint := u.String() + "/v1/" + url.PathEscape(strings.Trim(mount, "/")) + "/data/" + vaultEscapePath(vc.path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fetchedSecrets{}, err
	}
	req.Header.Set("X-Vault-Token", vc.token)
	req.Header.Set("Accept", "application/json")

	resp, err := importHTTP.Do(req)
	if err != nil {
		return fetchedSecrets{}, fmt.Errorf("vault: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fetchedSecrets{}, fmt.Errorf("vault: API returned HTTP %d", resp.StatusCode)
	}

	var body struct {
		Data struct {
			Data map[string]json.RawMessage `json:"data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fetchedSecrets{}, fmt.Errorf("vault: decoding response: %w", err)
	}
	pairs := make(map[string]string, len(body.Data.Data))
	for k, raw := range body.Data.Data {
		pairs[k] = jsonLeafString(raw)
	}
	return fetchedSecrets{pairs: pairs}, nil
}

// vaultEscapePath percent-escapes each path segment while preserving the slashes
// that Vault uses to nest KV paths.
func vaultEscapePath(p string) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	for i, seg := range parts {
		parts[i] = url.PathEscape(seg)
	}
	return strings.Join(parts, "/")
}

// jsonLeafString renders a KV v2 leaf: a JSON string decodes to its text;
// anything else (number/bool/object) keeps its JSON form so no data is lost.
func jsonLeafString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// awsSMAPI is the minimal Secrets Manager surface the importer needs. The real
// *secretsmanager.Client satisfies it; tests inject a fake so no live AWS call
// is made.
type awsSMAPI interface {
	ListSecrets(ctx context.Context, in *secretsmanager.ListSecretsInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error)
	GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// awsSMConfig carries the flags needed to read AWS Secrets Manager secrets.
type awsSMConfig struct {
	region          string
	accessKeyID     string
	secretAccessKey string
	sessionToken    string
	prefix          string // name prefix to list under, e.g. "prod/myapp/"
	// newClient builds the SM client; overridable in tests. When nil, a static-
	// credential client is built (never ambient/instance-profile creds).
	newClient func(ctx context.Context, ac awsSMConfig) (awsSMAPI, error)
}

// defaultAWSSMClient builds a Secrets Manager client from STATIC credentials
// only, so the importer never silently borrows the host's AWS identity.
func defaultAWSSMClient(ctx context.Context, ac awsSMConfig) (awsSMAPI, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(ac.region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			ac.accessKeyID, ac.secretAccessKey, ac.sessionToken)),
	)
	if err != nil {
		return nil, fmt.Errorf("aws-sm: invalid AWS config")
	}
	return secretsmanager.NewFromConfig(cfg), nil
}

// fetchAWSSM lists every Secrets Manager secret whose name starts with the
// prefix, fetches each value, and maps it into Janus keys:
//   - a secret whose value is a JSON object becomes one Janus key per JSON field;
//   - any other secret value becomes a single Janus key named after the secret's
//     trailing path segment (prefix stripped).
func fetchAWSSM(ctx context.Context, ac awsSMConfig) (fetchedSecrets, error) {
	if ac.region == "" {
		return fetchedSecrets{}, fmt.Errorf("aws-sm: --aws-region is required")
	}
	if ac.accessKeyID == "" || ac.secretAccessKey == "" {
		return fetchedSecrets{}, fmt.Errorf("aws-sm: static credentials are required (--aws-access-key-id / --aws-secret-access-key)")
	}
	if ac.prefix == "" {
		return fetchedSecrets{}, fmt.Errorf("aws-sm: --aws-prefix is required")
	}
	build := ac.newClient
	if build == nil {
		build = defaultAWSSMClient
	}
	cl, err := build(ctx, ac)
	if err != nil {
		return fetchedSecrets{}, err
	}

	names, err := listSMNames(ctx, cl, ac.prefix)
	if err != nil {
		return fetchedSecrets{}, err
	}

	pairs := map[string]string{}
	for _, name := range names {
		out, gerr := cl.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
			SecretId: aws.String(name),
		})
		if gerr != nil {
			return fetchedSecrets{}, fmt.Errorf("aws-sm: reading a secret failed")
		}
		if out.SecretString == nil {
			// Binary secrets are not importable as string secrets; skip.
			continue
		}
		mergeSMSecret(pairs, ac.prefix, name, *out.SecretString)
	}
	return fetchedSecrets{pairs: pairs}, nil
}

// listSMNames pages ListSecrets, keeping only names under the prefix.
func listSMNames(ctx context.Context, cl awsSMAPI, prefix string) ([]string, error) {
	var names []string
	var nextToken *string
	for {
		out, err := cl.ListSecrets(ctx, &secretsmanager.ListSecretsInput{NextToken: nextToken})
		if err != nil {
			return nil, fmt.Errorf("aws-sm: listing secrets failed")
		}
		for _, s := range out.SecretList {
			if s.Name == nil {
				continue
			}
			if strings.HasPrefix(*s.Name, prefix) {
				names = append(names, *s.Name)
			}
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	sort.Strings(names)
	return names, nil
}

// mergeSMSecret maps one Secrets Manager secret into Janus key(s): a JSON object
// value fans out to one key per field; any other value becomes a single key
// named after the secret's last path segment (with the prefix stripped).
func mergeSMSecret(dst map[string]string, prefix, name, value string) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &obj); err == nil && obj != nil {
		for k, raw := range obj {
			dst[k] = jsonLeafString(raw)
		}
		return
	}
	dst[smLeafKey(prefix, name)] = value
}

// smLeafKey derives a Janus key from a Secrets Manager secret name by stripping
// the prefix and keeping the trailing path segment.
func smLeafKey(prefix, name string) string {
	trimmed := strings.TrimPrefix(name, prefix)
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		trimmed = name
	}
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	return trimmed
}
