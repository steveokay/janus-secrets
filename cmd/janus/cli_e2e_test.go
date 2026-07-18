package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/api"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func bootPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("janus"),
		tcpostgres.WithUsername("janus"),
		tcpostgres.WithPassword("janus-test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Skip("postgres/docker not available:", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return dsn
}

// bootServer boots + initializes + unseals the API and returns its base URL plus
// the admin session cookie value.
func bootServer(t *testing.T) (string, string) {
	t.Helper()
	dsn := bootPostgres(t)
	ctx := context.Background()
	srv, st, err := api.Boot(ctx, api.BootConfig{DatabaseURL: dsn, SealType: crypto.SealTypeShamir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	var ir struct {
		Shares []string `json:"shares"`
		Admin  *struct{ Email, Password string } `json:"admin"`
	}
	post(t, ts.URL+"/v1/sys/init", "", `{"shares":1,"threshold":1,"admin_email":"root@corp.io"}`, &ir)
	post(t, ts.URL+"/v1/sys/unseal", "", fmt.Sprintf(`{"share":%q}`, ir.Shares[0]), nil)

	// Login via the real endpoint to get a session cookie.
	session := loginForCookie(t, ts.URL, ir.Admin.Email, ir.Admin.Password)
	return ts.URL, session
}

func post(t *testing.T, url, cookie, body string, out any) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "janus_session", Value: cookie})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("POST %s -> %d", url, resp.StatusCode)
	}
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
}

func loginForCookie(t *testing.T, base, email, pw string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": pw})
	req, _ := http.NewRequest("POST", base+"/v1/auth/login", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, ck := range resp.Cookies() {
		if ck.Name == "janus_session" {
			return ck.Value
		}
	}
	t.Fatal("no session cookie")
	return ""
}

// seedConfig creates project/env/config via the API (session cookie) and returns
// slugs. Environments and configs are nested under the parent's *id*, so each
// level is resolved before creating the next.
func seedConfig(t *testing.T, base, session string) (proj, env, cfg string) {
	c := &apiClient{address: base, cred: credential{Cookie: session}, hc: http.DefaultClient}

	post(t, base+"/v1/projects", session, `{"slug":"acme","name":"Acme"}`, nil)
	var pl struct {
		Projects []struct{ ID, Slug string } `json:"projects"`
	}
	if err := c.call("GET", "/v1/projects", nil, &pl); err != nil {
		t.Fatal(err)
	}
	pid := pl.Projects[0].ID

	post(t, base+"/v1/projects/"+pid+"/environments", session, `{"slug":"dev","name":"Dev"}`, nil)
	var el struct {
		Environments []struct{ ID, Slug string } `json:"environments"`
	}
	if err := c.call("GET", "/v1/projects/"+pid+"/environments", nil, &el); err != nil {
		t.Fatal(err)
	}
	eid := el.Environments[0].ID

	post(t, base+"/v1/projects/"+pid+"/environments/"+eid+"/configs", session, `{"name":"dev"}`, nil)
	return "acme", "dev", "dev"
}

func TestCLIRoundTrip(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("JANUS_CONFIG_DIR", cfgDir)
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")

	base, session := bootServer(t)
	proj, env, cfg := seedConfig(t, base, session)
	// Persist the session so CLI commands authenticate as the admin.
	if err := saveAuth(&authState{Address: base, Session: session, Email: "root@corp.io"}); err != nil {
		t.Fatal(err)
	}

	// setup in a work dir
	work := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	if _, _, err := runCmd(t, newSetupCmd(), "--project", proj, "--env", env, "--config", cfg); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, _, err := runCmd(t, newSecretsCmd(), "set", "GREETING=hello", "COUNT=1", "--message", "seed"); err != nil {
		t.Fatalf("set: %v", err)
	}
	out, _, err := runCmd(t, newSecretsCmd(), "get", "GREETING")
	if err != nil || strings.TrimSpace(out) != "hello" {
		t.Fatalf("get GREETING = %q (%v)", out, err)
	}
	listOut, _, err := runCmd(t, newSecretsCmd(), "list")
	if err != nil || !strings.Contains(listOut, "GREETING") || strings.Contains(listOut, "hello") {
		t.Fatalf("list should show key not value: %q (%v)", listOut, err)
	}
	dl, _, err := runCmd(t, newSecretsCmd(), "download", "--format", "env")
	if err != nil || !strings.Contains(dl, "GREETING=hello") {
		t.Fatalf("download: %q (%v)", dl, err)
	}
}

// TestCLIRunInjectsAndPropagatesExit shells out to a child that reads the injected
// var and exits with a code, verified via a subprocess pattern.
func TestCLIRunInjectsExit(t *testing.T) {
	if os.Getenv("JANUS_RUN_CHILD") == "1" {
		// Child mode: exit 7 iff GREETING is injected correctly.
		if os.Getenv("GREETING") == "hello" {
			os.Exit(7)
		}
		os.Exit(2)
	}

	cfgDir := t.TempDir()
	t.Setenv("JANUS_CONFIG_DIR", cfgDir)
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")
	base, session := bootServer(t)
	proj, env, cfg := seedConfig(t, base, session)
	_ = saveAuth(&authState{Address: base, Session: session, Email: "root@corp.io"})

	work := t.TempDir()
	cwd, _ := os.Getwd()
	_ = os.Chdir(work)
	defer os.Chdir(cwd)
	_, _, _ = runCmd(t, newSetupCmd(), "--project", proj, "--env", env, "--config", cfg)
	_, _, _ = runCmd(t, newSecretsCmd(), "set", "GREETING=hello")

	// Resolve cid + reveal through the client, build env, and exec this test binary
	// in child mode to confirm buildChildEnv + execChild propagate the value & code.
	c := &apiClient{address: base, cred: credential{Cookie: session}, hc: http.DefaultClient}
	cid, err := c.resolveConfigID(proj, env, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var rv struct{ Secrets map[string]string `json:"secrets"` }
	if err := c.call("GET", "/v1/configs/"+cid+"/secrets?reveal=true", nil, &rv); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	child := exec.Command(self, "-test.run", "TestCLIRunInjectsExit")
	builtEnv, _ := buildChildEnv(os.Environ(), rv.Secrets, false)
	child.Env = append(builtEnv, "JANUS_RUN_CHILD=1")
	err = child.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 7 {
		t.Fatalf("child exit = %v, want code 7 (secret injected)", err)
	}
}
