package secretsync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// ── fake providers ───────────────────────────────────────────────────────────

// plainProvider implements Provider but NOT Verifier — the historical shape of
// every provider before drift detection existed.
type plainProvider struct{}

func (plainProvider) Name() string { return "plain" }
func (plainProvider) Apply(context.Context, Creds, Addr, map[string]string, []string, bool) (ApplyResult, error) {
	return ApplyResult{}, nil
}

// fakeVerifier is a Provider + Verifier whose capability and remote state are
// injected by the test.
type fakeVerifier struct {
	plainProvider
	capability Capability
	state      RemoteState
	err        error
	sawKeys    []string
}

func (f *fakeVerifier) Capability() Capability { return f.capability }
func (f *fakeVerifier) Fetch(_ context.Context, _ Creds, _ Addr, keys []string) (RemoteState, error) {
	f.sawKeys = append([]string(nil), keys...)
	return f.state, f.err
}

// ── capability gate ──────────────────────────────────────────────────────────

func TestVerifierForCapabilityGate(t *testing.T) {
	tests := []struct {
		name string
		prov Provider
		want error
	}{
		{"provider without Verifier is unsupported", plainProvider{}, ErrVerifyUnsupported},
		{"CapNone is unsupported", &fakeVerifier{capability: CapNone}, ErrVerifyUnsupported},
		{"CapNamesOnly is verifiable", &fakeVerifier{capability: CapNamesOnly}, nil},
		{"CapValues is verifiable", &fakeVerifier{capability: CapValues}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := verifierFor(tc.prov)
			if !errors.Is(err, tc.want) {
				t.Fatalf("verifierFor: got %v, want %v", err, tc.want)
			}
			if tc.want == nil && v == nil {
				t.Fatal("verifierFor returned a nil Verifier for a verifiable provider")
			}
		})
	}
}

// TestShippedProviderCapabilities pins the documented capability matrix: the
// two write-only destinations must declare names-only, and must NEVER be able
// to claim a value comparison happened.
func TestShippedProviderCapabilities(t *testing.T) {
	svc := &Service{githubBaseURL: "https://api.github.com", hc: http.DefaultClient}
	want := map[string]Capability{
		ProviderGitHub:     CapNamesOnly,
		ProviderCloudflare: CapNamesOnly,
		ProviderK8s:        CapValues,
		ProviderGitLab:     CapValues,
		ProviderAWSSSM:     CapValues,
		ProviderAWSSecrets: CapValues,
		ProviderVercel:     CapValues,
		ProviderNetlify:    CapValues,
	}
	for name, capa := range want {
		if got := svc.ProviderCapability(name); got != capa {
			t.Errorf("%s capability = %q, want %q", name, got, capa)
		}
	}
	if got := svc.ProviderCapability("nope"); got != CapNone {
		t.Errorf("unknown provider capability = %q, want %q", got, CapNone)
	}
}

// ── drift computation (table-driven, no DB, no network) ──────────────────────

func TestComputeDrift(t *testing.T) {
	svc, _ := newKeyedService(t)

	tests := []struct {
		name           string
		capability     Capability
		prune          bool
		desired        map[string]string
		remote         RemoteState
		wantStatus     string
		wantMissing    []string
		wantModified   []string
		wantExtra      []string
		wantUnreadable []string
		wantCompared   bool
	}{
		{
			name:       "values provider clean",
			capability: CapValues,
			desired:    map[string]string{"A": "1", "B": "2"},
			remote: RemoteState{Names: []string{"A", "B"},
				Values: map[string]string{"A": "1", "B": "2"}},
			wantStatus:   VerifyClean,
			wantCompared: true,
		},
		{
			name:       "values provider detects modified value",
			capability: CapValues,
			desired:    map[string]string{"A": "1", "B": "2"},
			remote: RemoteState{Names: []string{"A", "B"},
				Values: map[string]string{"A": "1", "B": "TAMPERED"}},
			wantStatus:   VerifyDrift,
			wantModified: []string{"B"},
			wantCompared: true,
		},
		{
			name:        "values provider detects missing key",
			capability:  CapValues,
			desired:     map[string]string{"A": "1", "B": "2"},
			remote:      RemoteState{Names: []string{"A"}, Values: map[string]string{"A": "1"}},
			wantStatus:  VerifyDrift,
			wantMissing: []string{"B"},

			wantCompared: true,
		},
		{
			name:       "present but value not returned is unreadable, not drift",
			capability: CapValues,
			desired:    map[string]string{"A": "1"},
			remote:     RemoteState{Names: []string{"A"}, Values: map[string]string{}},
			wantStatus: VerifyClean,

			wantUnreadable: []string{"A"},
			wantCompared:   true,
		},
		{
			name:       "extra key is drift when the target prunes",
			capability: CapValues,
			prune:      true,
			desired:    map[string]string{"A": "1"},
			remote: RemoteState{Names: []string{"A", "STRAY"},
				Values: map[string]string{"A": "1", "STRAY": "x"}},
			wantStatus:   VerifyDrift,
			wantExtra:    []string{"STRAY"},
			wantCompared: true,
		},
		{
			name:       "extra key is advisory only when the target does not prune",
			capability: CapValues,
			prune:      false,
			desired:    map[string]string{"A": "1"},
			remote: RemoteState{Names: []string{"A", "STRAY"},
				Values: map[string]string{"A": "1", "STRAY": "x"}},
			wantStatus:   VerifyClean,
			wantExtra:    []string{"STRAY"},
			wantCompared: true,
		},
		{
			name:       "names-only provider never compares values",
			capability: CapNamesOnly,
			desired:    map[string]string{"A": "1", "B": "2"},
			remote:     RemoteState{Names: []string{"A", "B"}},
			wantStatus: VerifyClean,
			// values_compared MUST be false: a clean names-only pass is not a
			// verified one.
			wantCompared: false,
		},
		{
			name:        "names-only provider still detects missing and extra",
			capability:  CapNamesOnly,
			prune:       true,
			desired:     map[string]string{"A": "1", "B": "2"},
			remote:      RemoteState{Names: []string{"A", "STRAY"}},
			wantStatus:  VerifyDrift,
			wantMissing: []string{"B"},
			wantExtra:   []string{"STRAY"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := svc.computeDrift(tc.desired, tc.remote, tc.capability, tc.prune)
			if err != nil {
				t.Fatalf("computeDrift: %v", err)
			}
			if d.Status() != tc.wantStatus {
				t.Errorf("status = %q, want %q (%+v)", d.Status(), tc.wantStatus, d)
			}
			if d.ValuesCompared != tc.wantCompared {
				t.Errorf("ValuesCompared = %t, want %t", d.ValuesCompared, tc.wantCompared)
			}
			assertKeys(t, "missing", d.Missing, tc.wantMissing)
			assertKeys(t, "modified", d.Modified, tc.wantModified)
			assertKeys(t, "extra", d.Extra, tc.wantExtra)
			assertKeys(t, "unreadable", d.Unreadable, tc.wantUnreadable)
			if d.Checked != len(tc.desired) {
				t.Errorf("Checked = %d, want %d", d.Checked, len(tc.desired))
			}
		})
	}
}

func assertKeys(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v", label, got, want)
		return
	}
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	for i := range g {
		if g[i] != w[i] {
			t.Errorf("%s = %v, want %v", label, got, want)
			return
		}
	}
}

// TestComputeDriftSealed asserts the comparison refuses to proceed without the
// master key (the HMAC subkey is unavailable while sealed).
func TestComputeDriftSealed(t *testing.T) {
	svc, _ := newKeyedService(t)
	svc.kr.(interface{ Seal() }).Seal()
	_, err := svc.computeDrift(
		map[string]string{"A": "1"},
		RemoteState{Names: []string{"A"}, Values: map[string]string{"A": "1"}},
		CapValues, false)
	if !errors.Is(err, ErrSealed) {
		t.Fatalf("computeDrift while sealed: got %v, want ErrSealed", err)
	}
}

// TestZeroValuesWipesMap asserts the remote plaintext map is emptied.
func TestZeroValuesWipesMap(t *testing.T) {
	m := map[string]string{"A": "s3cret"}
	zeroValues(m)
	if len(m) != 0 {
		t.Fatalf("zeroValues left %d entries", len(m))
	}
}

// TestFetchReceivesManagedKeys asserts the engine passes the managed key set to
// Fetch so a per-key provider can bound its fan-out.
func TestFetchReceivesManagedKeys(t *testing.T) {
	f := &fakeVerifier{capability: CapValues, state: RemoteState{Values: map[string]string{}}}
	v, err := verifierFor(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Fetch(context.Background(), Creds{}, Addr{}, []string{"B", "A"}); err != nil {
		t.Fatal(err)
	}
	if len(f.sawKeys) != 2 {
		t.Fatalf("Fetch saw keys %v, want 2", f.sawKeys)
	}
}

// ── end-to-end against fake destinations (needs Docker/Postgres) ─────────────

// newKeyedService returns a Service whose keyring is unsealed. It shares the
// DB-backed harness so computeDrift (which needs the HMAC subkey) works.
func newKeyedService(t *testing.T) (*Service, *secrets.Service) {
	t.Helper()
	return newTestService(t)
}

// fakeGitLab serves the CI/CD variables API for one project: a GET list (used
// by verification) over an injectable variable table.
type fakeGitLab struct {
	srv  *httptest.Server
	vars map[string]string
}

func newFakeGitLab(t *testing.T, vars map[string]string) *fakeGitLab {
	t.Helper()
	f := &fakeGitLab{vars: vars}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		page := r.URL.Query().Get("page")
		out := []glVarRead{}
		if page == "" || page == "1" {
			keys := make([]string, 0, len(f.vars))
			for k := range f.vars {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				out = append(out, glVarRead{Key: k, Value: f.vars[k], EnvironmentScope: "*"})
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// seedTarget creates a sync target for cfg with the given provider/addr and the
// given desired secrets already stored.
func seedTarget(t *testing.T, svc *Service, sec *secrets.Service, proj *store.Project, cfg *store.Config,
	provider string, addr string, prune bool, kv map[string]string) *store.SyncTarget {
	t.Helper()
	ctx := context.Background()
	changes := make([]secrets.SecretChange, 0, len(kv))
	for k, v := range kv {
		changes = append(changes, secrets.SecretChange{Key: k, Value: []byte(v)})
	}
	if _, err := sec.SetSecrets(ctx, cfg.ID, changes, "seed", "user:tester"); err != nil {
		t.Fatalf("SetSecrets: %v", err)
	}
	id, err := testStore.NewID(ctx)
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	ct, nonce, wrapped, kekVer, err := svc.sealCreds(proj, id, Creds{Token: "glpat-x", PAT: "ghp_x"})
	if err != nil {
		t.Fatalf("sealCreds: %v", err)
	}
	tgt, err := svc.repo.Create(ctx, &store.SyncTarget{
		ID: id, ProjectID: proj.ID, ConfigID: cfg.ID, Provider: provider,
		Prune: prune, IntervalSeconds: 3600, NextSyncAt: time.Now().UTC(),
		CredsCT: ct, CredsNonce: nonce, CredsWrappedDEK: wrapped, CredsDEKKEKVersion: kekVer,
		Addr: []byte(addr), CreatedBy: "user:tester",
	})
	if err != nil {
		t.Fatalf("repo.Create: %v", err)
	}
	return tgt
}

// TestVerifyValuesProviderDetectsDrift drives the full Verify path (resolve →
// fetch → HMAC compare → persist → audit) against a fake GitLab destination.
func TestVerifyValuesProviderDetectsDrift(t *testing.T) {
	svc, sec := newTestService(t)
	proj, cfg := mkChain(t, sec, "sync-verify-values")

	gl := newFakeGitLab(t, map[string]string{"API_KEY": "s3cret", "DB_URL": "postgres://x"})
	addr := `{"gitlab_url":"` + gl.srv.URL + `","project":"42"}`
	tgt := seedTarget(t, svc, sec, proj, cfg, ProviderGitLab, addr, true,
		map[string]string{"API_KEY": "s3cret", "DB_URL": "postgres://x"})

	ctx := context.Background()
	rep, err := svc.Verify(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("Verify (clean): %v", err)
	}
	if rep.Status != VerifyClean {
		t.Fatalf("status = %q, want clean (%+v)", rep.Status, rep)
	}
	if !rep.ValuesCompared || rep.Capability != CapValues {
		t.Fatalf("values-capable provider reported %+v", rep)
	}

	// Someone edits the variable at GitLab → value drift.
	gl.vars["API_KEY"] = "changed-out-of-band"
	rep, err = svc.Verify(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("Verify (drift): %v", err)
	}
	if rep.Status != VerifyDrift || rep.ModifiedCount != 1 || rep.Modified[0] != "API_KEY" {
		t.Fatalf("drift report = %+v, want modified [API_KEY]", rep)
	}

	// Someone adds an unmanaged variable → extra (target prunes → drift).
	gl.vars["API_KEY"] = "s3cret"
	gl.vars["STRAY"] = "whatever"
	rep, err = svc.Verify(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("Verify (extra): %v", err)
	}
	if rep.Status != VerifyDrift || rep.ExtraCount != 1 || rep.Extra[0] != "STRAY" {
		t.Fatalf("extra report = %+v, want extra [STRAY]", rep)
	}

	// History persisted, newest first, and value-free.
	runs, err := svc.ListVerifyRuns(ctx, tgt.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListVerifyRuns: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("recorded %d verify runs, want 3", len(runs))
	}
	if runs[0].Status != VerifyDrift || runs[2].Status != VerifyClean {
		t.Fatalf("run statuses = %q…%q", runs[0].Status, runs[2].Status)
	}
	if !runs[0].ValuesCompared || runs[0].Capability != string(CapValues) {
		t.Fatalf("run row = %+v", runs[0])
	}

	// Last-result summary lands on the target's verify state.
	st, err := svc.GetVerifyState(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("GetVerifyState: %v", err)
	}
	if st.LastStatus == nil || *st.LastStatus != VerifyDrift || st.LastDriftCount != 1 {
		t.Fatalf("verify state = %+v", st)
	}
	if st.NextVerifyAt.Before(time.Now().UTC()) {
		t.Fatalf("next_verify_at not advanced: %v", st.NextVerifyAt)
	}
}

// fakeGitHubNames serves the Actions-secrets LIST endpoint — names only, never
// values, exactly like the real API.
func fakeGitHubNames(t *testing.T, names *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/secrets") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("page") != "1" {
			_ = json.NewEncoder(w).Encode(ghSecretsPage{})
			return
		}
		var out ghSecretsPage
		for _, n := range *names {
			out.Secrets = append(out.Secrets, struct {
				Name string `json:"name"`
			}{Name: n})
		}
		out.TotalCount = len(out.Secrets)
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestVerifyNamesOnlyProviderReportsMissingAndExtra asserts a write-only
// destination reports what it can (missing / extra) and NEVER claims a value
// comparison — even when every managed key is present.
func TestVerifyNamesOnlyProviderReportsMissingAndExtra(t *testing.T) {
	svc, sec := newTestService(t)
	proj, cfg := mkChain(t, sec, "sync-verify-namesonly")

	names := []string{"API_KEY", "DB_URL"}
	gh := fakeGitHubNames(t, &names)
	svc.githubBaseURL = gh.URL

	tgt := seedTarget(t, svc, sec, proj, cfg, ProviderGitHub, `{"owner":"o","repo":"r"}`, true,
		map[string]string{"API_KEY": "s3cret", "DB_URL": "postgres://x"})

	ctx := context.Background()
	rep, err := svc.Verify(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.Status != VerifyClean {
		t.Fatalf("status = %q, want clean (%+v)", rep.Status, rep)
	}
	if rep.Capability != CapNamesOnly {
		t.Fatalf("capability = %q, want %q", rep.Capability, CapNamesOnly)
	}
	// THE contract: a clean names-only pass must not read as "values verified".
	if rep.ValuesCompared {
		t.Fatal("names-only provider reported values_compared=true")
	}
	if rep.ModifiedCount != 0 {
		t.Fatalf("names-only provider reported %d modified keys", rep.ModifiedCount)
	}

	// Someone deletes a managed secret and adds an unmanaged one.
	names = []string{"API_KEY", "SOMEONE_ELSES"}
	rep, err = svc.Verify(ctx, tgt.ID)
	if err != nil {
		t.Fatalf("Verify (drift): %v", err)
	}
	if rep.Status != VerifyDrift {
		t.Fatalf("status = %q, want drift (%+v)", rep.Status, rep)
	}
	if rep.MissingCount != 1 || rep.Missing[0] != "DB_URL" {
		t.Fatalf("missing = %v, want [DB_URL]", rep.Missing)
	}
	if rep.ExtraCount != 1 || rep.Extra[0] != "SOMEONE_ELSES" {
		t.Fatalf("extra = %v, want [SOMEONE_ELSES]", rep.Extra)
	}
	if rep.ValuesCompared {
		t.Fatal("names-only provider reported values_compared=true on the drift pass")
	}
}

// TestVerifySealedIsNotAFailure asserts a sealed server records nothing.
func TestVerifySealedIsNotAFailure(t *testing.T) {
	svc, sec := newTestService(t)
	proj, cfg := mkChain(t, sec, "sync-verify-sealed")
	gl := newFakeGitLab(t, map[string]string{})
	tgt := seedTarget(t, svc, sec, proj, cfg, ProviderGitLab,
		`{"gitlab_url":"`+gl.srv.URL+`","project":"42"}`, true, map[string]string{"A": "1"})

	svc.kr.(interface{ Seal() }).Seal()
	ctx := context.Background()
	if _, err := svc.Verify(ctx, tgt.ID); !errors.Is(err, ErrSealed) {
		t.Fatalf("Verify while sealed: got %v, want ErrSealed", err)
	}
	runs, err := svc.ListVerifyRuns(ctx, tgt.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListVerifyRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("sealed verify recorded %d runs, want 0", len(runs))
	}
}

// TestRunVerifyDuePersistsAndReschedules asserts the scheduler claims a target
// with no verify-state row (lazy defaults → due now), records a run, and pushes
// next_verify_at out so the next pass does not re-claim it.
func TestRunVerifyDuePersistsAndReschedules(t *testing.T) {
	svc, sec := newTestService(t)
	proj, cfg := mkChain(t, sec, "sync-verify-sched")
	gl := newFakeGitLab(t, map[string]string{"A": "1"})
	tgt := seedTarget(t, svc, sec, proj, cfg, ProviderGitLab,
		`{"gitlab_url":"`+gl.srv.URL+`","project":"42"}`, true, map[string]string{"A": "1"})

	ctx := context.Background()
	due, err := svc.repo.ClaimVerifyDueIDs(ctx, time.Now().UTC(), 50)
	if err != nil {
		t.Fatalf("ClaimVerifyDueIDs: %v", err)
	}
	if !containsID(due, tgt.ID) {
		t.Fatalf("target with no verify state was not due: %v", due)
	}

	svc.RunVerifyDue(ctx)

	runs, err := svc.ListVerifyRuns(ctx, tgt.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListVerifyRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != VerifyClean {
		t.Fatalf("scheduler recorded %+v", runs)
	}

	due, err = svc.repo.ClaimVerifyDueIDs(ctx, time.Now().UTC(), 50)
	if err != nil {
		t.Fatalf("ClaimVerifyDueIDs (2): %v", err)
	}
	if containsID(due, tgt.ID) {
		t.Fatal("target re-claimed immediately after a pass (next_verify_at not advanced)")
	}

	// Disabling verification takes the target out of the due set entirely.
	no := false
	if err := svc.SetVerifySchedule(ctx, tgt.ID, &no, nil); err != nil {
		t.Fatalf("SetVerifySchedule: %v", err)
	}
	due, err = svc.repo.ClaimVerifyDueIDs(ctx, time.Now().UTC().Add(24*time.Hour), 50)
	if err != nil {
		t.Fatalf("ClaimVerifyDueIDs (3): %v", err)
	}
	if containsID(due, tgt.ID) {
		t.Fatal("disabled target still claimed by the verify scheduler")
	}
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestSetVerifyScheduleRejectsBadInterval pins the deny-by-default validation.
func TestSetVerifyScheduleRejectsBadInterval(t *testing.T) {
	svc, sec := newTestService(t)
	proj, cfg := mkChain(t, sec, "sync-verify-badinterval")
	gl := newFakeGitLab(t, map[string]string{})
	tgt := seedTarget(t, svc, sec, proj, cfg, ProviderGitLab,
		`{"gitlab_url":"`+gl.srv.URL+`","project":"42"}`, true, map[string]string{"A": "1"})

	bad := int64(0)
	if err := svc.SetVerifySchedule(context.Background(), tgt.ID, nil, &bad); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("interval 0: got %v, want ErrInvalidConfig", err)
	}
	if err := svc.SetVerifySchedule(context.Background(), "00000000-0000-0000-0000-000000000000", nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown target: got %v, want ErrNotFound", err)
	}
}
