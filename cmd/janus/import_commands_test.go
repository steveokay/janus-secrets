package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// --- Doppler source ---------------------------------------------------------

// dopplerServer serves an obviously-fake Doppler /v3/configs/config/secrets
// response so the fetcher has no live-network dependency.
func dopplerServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/configs/config/secrets", func(w http.ResponseWriter, r *http.Request) {
		// Doppler auth is Basic with the token as the username.
		u, _, _ := r.BasicAuth()
		if u == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"secrets":{
			"DATABASE_URL":{"raw":"postgres://fake","computed":"postgres://fake"},
			"API_KEY":{"raw":"dpl-fake-000","computed":"dpl-fake-000"}
		}}`))
	})
	return httptest.NewServer(mux)
}

func TestFetchDopplerMapsSecrets(t *testing.T) {
	ts := dopplerServer(t)
	defer ts.Close()

	got, err := fetchDoppler(context.Background(), dopplerConfig{
		token: "dp.st.fake", project: "acme", config: "dev", apiBase: ts.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.pairs["DATABASE_URL"] != "postgres://fake" || got.pairs["API_KEY"] != "dpl-fake-000" {
		t.Fatalf("unexpected mapping: %#v", got.pairs)
	}
	if len(got.keys()) != 2 {
		t.Fatalf("want 2 keys, got %v", got.keys())
	}
}

func TestFetchDopplerRequiresToken(t *testing.T) {
	if _, err := fetchDoppler(context.Background(), dopplerConfig{project: "p", config: "c"}); err == nil {
		t.Fatal("expected error without token")
	}
}

// --- Vault source -----------------------------------------------------------

func vaultServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/secret/data/myapp/prod", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		// KV v2 nests the map under data.data; a non-string leaf (port) too.
		_, _ = w.Write([]byte(`{"data":{"data":{
			"PASSWORD":"fake-vault-pw",
			"PORT":5432,
			"HOST":"db.fake"
		}}}`))
	})
	return httptest.NewServer(mux)
}

func TestFetchVaultMapsKVv2(t *testing.T) {
	ts := vaultServer(t)
	defer ts.Close()

	got, err := fetchVault(context.Background(), vaultConfig{
		addr: ts.URL, token: "hvs.fake", mount: "secret", path: "myapp/prod",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.pairs["PASSWORD"] != "fake-vault-pw" {
		t.Fatalf("string leaf wrong: %q", got.pairs["PASSWORD"])
	}
	if got.pairs["PORT"] != "5432" {
		t.Fatalf("numeric leaf should JSON-encode to 5432, got %q", got.pairs["PORT"])
	}
	if got.pairs["HOST"] != "db.fake" {
		t.Fatalf("host leaf wrong: %q", got.pairs["HOST"])
	}
}

func TestFetchVaultRejectsBadAddr(t *testing.T) {
	if _, err := fetchVault(context.Background(), vaultConfig{
		addr: "ftp://nope", token: "t", path: "p",
	}); err == nil {
		t.Fatal("expected rejection of non-http(s) addr")
	}
}

// --- AWS Secrets Manager source (fake SDK) ----------------------------------

type fakeSM struct {
	list map[string]string // secret name → SecretString
}

func (f *fakeSM) ListSecrets(_ context.Context, _ *secretsmanager.ListSecretsInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error) {
	out := &secretsmanager.ListSecretsOutput{}
	for name := range f.list {
		n := name
		out.SecretList = append(out.SecretList, smtypes.SecretListEntry{Name: &n})
	}
	return out, nil
}

func (f *fakeSM) GetSecretValue(_ context.Context, in *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	v, ok := f.list[aws.ToString(in.SecretId)]
	if !ok {
		return nil, &smtypes.ResourceNotFoundException{}
	}
	return &secretsmanager.GetSecretValueOutput{SecretString: aws.String(v)}, nil
}

func TestFetchAWSSMFansOutJSONAndScalars(t *testing.T) {
	fake := &fakeSM{list: map[string]string{
		"prod/myapp/db":     `{"DB_USER":"fake-user","DB_TOKEN":"fake-token"}`, // JSON → per-field
		"prod/myapp/apikey": "AKIA-FAKE-000",                                    // scalar → leaf key
		"other/skip":        "should-not-appear",                                // outside prefix
	}}
	got, err := fetchAWSSM(context.Background(), awsSMConfig{
		region: "us-east-1", accessKeyID: "AKIAFAKE", secretAccessKey: "fakesecret",
		prefix:    "prod/myapp/",
		newClient: func(_ context.Context, _ awsSMConfig) (awsSMAPI, error) { return fake, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.pairs["DB_USER"] != "fake-user" || got.pairs["DB_TOKEN"] != "fake-token" {
		t.Fatalf("JSON secret should fan out to fields: %#v", got.pairs)
	}
	if got.pairs["apikey"] != "AKIA-FAKE-000" {
		t.Fatalf("scalar secret should map to leaf key: %#v", got.pairs)
	}
	if _, ok := got.pairs["skip"]; ok {
		t.Fatalf("secret outside the prefix must not be imported: %#v", got.pairs)
	}
}

// --- runImport: dry-run value-free + real write via Janus client -------------

// captureConfigServer stands in for the Janus API: it resolves the target
// project/env/config and records the PUT /secrets body so tests can assert the
// exact SecretChange batch.
type captureConfigServer struct {
	ts      *httptest.Server
	lastPut map[string]any
}

func newCaptureConfigServer(t *testing.T) *captureConfigServer {
	t.Helper()
	cs := &captureConfigServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"p1","slug":"acme"}]}`))
	})
	mux.HandleFunc("/v1/projects/p1/environments", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"environments":[{"id":"e1","slug":"prod"}]}`))
	})
	mux.HandleFunc("/v1/projects/p1/environments/e1/configs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"configs":[{"id":"c1","name":"main"}]}`))
	})
	mux.HandleFunc("/v1/configs/c1/secrets", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&cs.lastPut)
		_, _ = w.Write([]byte(`{"version":7}`))
	})
	cs.ts = httptest.NewServer(mux)
	return cs
}

func importTestTarget(addr string) *importTarget {
	return &importTarget{
		address: addr, token: "janus_svc_fake",
		project: "acme", env: "prod", configNm: "main",
	}
}

func TestRunImportDryRunPrintsNamesNoValues(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")
	cs := newCaptureConfigServer(t)
	defer cs.ts.Close()

	fetched := fetchedSecrets{pairs: map[string]string{
		"API_KEY":      "SUPER-SECRET-VALUE-XYZ",
		"DATABASE_URL": "postgres://secret-dsn",
	}}
	tgt := importTestTarget(cs.ts.URL)
	tgt.dryRun = true // default, but be explicit

	out, stderr := runImportCapture(t, tgt, sourceDoppler, fetched)

	// Names appear; no value ever does; nothing was written.
	if !strings.Contains(stderr, "API_KEY") || !strings.Contains(stderr, "DATABASE_URL") {
		t.Fatalf("dry-run should list key names, got: %q", stderr)
	}
	for _, v := range fetched.pairs {
		if strings.Contains(stderr, v) || strings.Contains(out, v) {
			t.Fatalf("dry-run leaked a value")
		}
	}
	if cs.lastPut != nil {
		t.Fatalf("dry-run must not write to the Janus config")
	}
}

func TestRunImportConfirmWritesBatch(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")
	cs := newCaptureConfigServer(t)
	defer cs.ts.Close()

	fetched := fetchedSecrets{pairs: map[string]string{
		"API_KEY":      "SUPER-SECRET-VALUE-XYZ",
		"DATABASE_URL": "postgres://secret-dsn",
	}}
	tgt := importTestTarget(cs.ts.URL)
	tgt.confirm = true

	out, stderr := runImportCapture(t, tgt, sourceDoppler, fetched)

	if cs.lastPut == nil {
		t.Fatal("expected a PUT /secrets on --confirm")
	}
	changes, ok := cs.lastPut["changes"].([]any)
	if !ok || len(changes) != 2 {
		t.Fatalf("expected 2 batched changes, got: %#v", cs.lastPut["changes"])
	}
	// The batch carries the exact key→value mapping.
	seen := map[string]string{}
	for _, ch := range changes {
		m := ch.(map[string]any)
		seen[m["key"].(string)], _ = m["value"].(string)
	}
	if seen["API_KEY"] != "SUPER-SECRET-VALUE-XYZ" || seen["DATABASE_URL"] != "postgres://secret-dsn" {
		t.Fatalf("batch mapping wrong: %#v", seen)
	}
	// Success summary reports the count + version, never a value.
	if !strings.Contains(stderr, "v7") {
		t.Fatalf("expected success summary with version, got: %q", stderr)
	}
	for _, v := range fetched.pairs {
		if strings.Contains(stderr, v) || strings.Contains(out, v) {
			t.Fatalf("write path leaked a value")
		}
	}
}

func TestRunImportMissingTargetErrors(t *testing.T) {
	tgt := &importTarget{project: "acme"} // missing env/config
	err := runImportErr(t, tgt, sourceVault, fetchedSecrets{pairs: map[string]string{"K": "v"}})
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected a required-flags error, got: %v", err)
	}
}

// TestImportDopplerEndToEndDryRun exercises the full cobra command: flag parse,
// DOPPLER_TOKEN env pickup, fetch from the fake Doppler server, and a value-free
// dry-run. Neither the fetched values nor the source token may leak.
func TestImportDopplerEndToEndDryRun(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")

	src := dopplerServer(t)
	defer src.Close()

	const dopplerToken = "dp.st.OBVIOUSLYFAKE"
	t.Setenv("DOPPLER_TOKEN", dopplerToken)

	out, errOut, err := runCmd(t, newImportCmd(),
		"doppler",
		"--doppler-project", "acme", "--doppler-config", "dev", "--doppler-api", src.URL,
		"--project", "acme", "--env", "prod", "--config", "main",
	)
	if err != nil {
		t.Fatalf("import doppler dry-run: %v (stderr=%q)", err, errOut)
	}
	// Dry-run lists names.
	if !strings.Contains(errOut, "DATABASE_URL") || !strings.Contains(errOut, "API_KEY") {
		t.Fatalf("expected key names in dry-run, got: %q", errOut)
	}
	// No value and no source token may appear anywhere.
	for _, leak := range []string{"postgres://fake", "dpl-fake-000", dopplerToken} {
		if strings.Contains(out, leak) || strings.Contains(errOut, leak) {
			t.Fatalf("import leaked %q", leak)
		}
	}
}

// runImportCapture drives runImport with a cobra command whose stdout/stderr are
// captured, returning (stdout, stderr). It fails the test on error.
func runImportCapture(t *testing.T, tgt *importTarget, src importSource, f fetchedSecrets) (string, string) {
	t.Helper()
	var out, errb strings.Builder
	cmd := newImportCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	if err := runImport(cmd, tgt, src, f); err != nil {
		t.Fatalf("runImport: %v", err)
	}
	return out.String(), errb.String()
}

// runImportErr drives runImport and returns the error (for negative tests).
func runImportErr(t *testing.T, tgt *importTarget, src importSource, f fetchedSecrets) error {
	t.Helper()
	var out, errb strings.Builder
	cmd := newImportCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	return runImport(cmd, tgt, src, f)
}
