package secretsync

import "context"

const (
	ProviderGitHub = "github"
	ProviderK8s    = "k8s"
)

// Creds is the decrypted provider credential blob (never logged/persisted clear).
type Creds struct {
	PAT    string `json:"pat,omitempty"`     // github
	APIURL string `json:"api_url,omitempty"` // k8s
	CACert string `json:"ca_cert,omitempty"` // k8s
	Token  string `json:"token,omitempty"`   // k8s
}

// Addr is the non-secret destination coordinates (stored as jsonb).
type Addr struct {
	Owner       string `json:"owner,omitempty"`       // github
	Repo        string `json:"repo,omitempty"`        // github
	Environment string `json:"environment,omitempty"` // github (optional)
	Namespace   string `json:"namespace,omitempty"`   // k8s
	SecretName  string `json:"secret_name,omitempty"` // k8s
}

// ApplyResult reports what a provider did.
type ApplyResult struct {
	Applied []string          // keys written to the target
	Skipped map[string]string // key -> value-free reason
}

// Provider applies a desired key/value map to one external destination.
// managedKeys is the set pushed on the previous successful sync (drives prune).
type Provider interface {
	Apply(ctx context.Context, creds Creds, addr Addr, desired map[string]string,
		managedKeys []string, prune bool) (ApplyResult, error)
	Name() string
}
