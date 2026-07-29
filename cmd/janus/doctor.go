package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
	"github.com/steveokay/janus-secrets/internal/api"
	"github.com/steveokay/janus-secrets/internal/auditship"
	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/nethard"
	"github.com/steveokay/janus-secrets/internal/store"
	"github.com/steveokay/janus-secrets/internal/version"
	migrations "github.com/steveokay/janus-secrets/migrations"
)

// ---------------------------------------------------------------------------
// Result model
// ---------------------------------------------------------------------------

// doctorStatus is a single check's verdict.
type doctorStatus string

const (
	statusPass doctorStatus = "PASS"
	statusWarn doctorStatus = "WARN"
	statusFail doctorStatus = "FAIL"
	statusSkip doctorStatus = "SKIP"
)

// doctorCheck is one named diagnostic and its outcome. Every field is
// operator-facing text: it is scrubbed of known secret material before it is
// printed or serialized (see scrubber).
type doctorCheck struct {
	Name    string       `json:"name"`
	Status  doctorStatus `json:"status"`
	Summary string       `json:"summary"`
	// Detail carries supporting observations — what doctor actually saw.
	Detail []string `json:"detail,omitempty"`
	// Fix is the concrete remedy, present whenever Status is WARN or FAIL.
	Fix string `json:"fix,omitempty"`
}

// doctorReport is the whole run, and the --json document.
type doctorReport struct {
	Version string        `json:"version"`
	Checks  []doctorCheck `json:"checks"`
	Summary struct {
		Pass int `json:"pass"`
		Warn int `json:"warn"`
		Fail int `json:"fail"`
		Skip int `json:"skip"`
	} `json:"summary"`
	// OK is the exit verdict: false means the process exits non-zero. Any FAIL
	// clears it; a WARN clears it too only under --strict.
	OK bool `json:"ok"`
}

func (r *doctorReport) add(c doctorCheck) {
	r.Checks = append(r.Checks, c)
}

// pass/warn/fail/skip are small constructors that keep the check bodies terse.
func dPass(name, summary string, detail ...string) doctorCheck {
	return doctorCheck{Name: name, Status: statusPass, Summary: summary, Detail: detail}
}

func dWarn(name, summary, fix string, detail ...string) doctorCheck {
	return doctorCheck{Name: name, Status: statusWarn, Summary: summary, Fix: fix, Detail: detail}
}

func dFail(name, summary, fix string, detail ...string) doctorCheck {
	return doctorCheck{Name: name, Status: statusFail, Summary: summary, Fix: fix, Detail: detail}
}

func dSkip(name, summary string, detail ...string) doctorCheck {
	return doctorCheck{Name: name, Status: statusSkip, Summary: summary, Detail: detail}
}

// ---------------------------------------------------------------------------
// Command
// ---------------------------------------------------------------------------

type doctorOpts struct {
	asJSON  bool
	strict  bool
	offline bool
	address string
	timeout time.Duration
}

func newDoctorCmd() *cobra.Command {
	var o doctorOpts
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose this instance's configuration and environment",
		Long: `Run a series of preflight checks over the JANUS_* environment, the
database, and (when reachable) the running server, and print a PASS/WARN/FAIL
line per check with the concrete fix for anything that is wrong.

doctor exists for configuration that is INTERNALLY VALID but wrong relative to
how the deployment actually works — a passkey origin naming the wrong port, a
seal type that disagrees with the stored one, a typo'd variable the server
silently ignores. Boot-time validation cannot catch those.

It needs no running server: with only JANUS_DATABASE_URL set it inspects the
configuration and the schema. It reports more when a server answers.

Exit status is non-zero if any check FAILs (add --strict to fail on WARN too),
so it is usable as a CI gate or a container healthcheck. No secret value is ever
printed: DSN passwords, tokens, and key material are redacted.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rep := runDoctor(cmd.Context(), o)
			if err := writeDoctorReport(cmd.OutOrStdout(), rep, o.asJSON); err != nil {
				return err
			}
			if !rep.OK {
				return fmt.Errorf("doctor: %d check(s) failed, %d warning(s)",
					rep.Summary.Fail, rep.Summary.Warn)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "emit the report as JSON")
	cmd.Flags().BoolVar(&o.strict, "strict", false, "treat warnings as failures (non-zero exit)")
	cmd.Flags().BoolVar(&o.offline, "offline", false, "skip every network probe and the database connection")
	cmd.Flags().StringVar(&o.address, "address", "", "base URL of the running server to probe (default: derived from JANUS_LISTEN_ADDR)")
	cmd.Flags().DurationVar(&o.timeout, "timeout", 5*time.Second, "per-probe timeout for the database and HTTP checks")
	return cmd
}

// ---------------------------------------------------------------------------
// Runner
// ---------------------------------------------------------------------------

// runDoctor executes every check in a fixed order and returns the report. It
// never returns an error: a check that cannot run reports SKIP or FAIL for
// itself, so one broken probe never hides the rest of the diagnosis.
func runDoctor(ctx context.Context, o doctorOpts) *doctorReport {
	if ctx == nil {
		ctx = context.Background()
	}
	if o.timeout <= 0 {
		o.timeout = 5 * time.Second
	}
	scrub := newScrubber()
	rep := &doctorReport{Version: version.String()}

	// --- configuration only (no I/O) ---------------------------------------
	rep.add(checkUnknownEnv())
	rep.add(checkConfigParse())

	dsn := os.Getenv("JANUS_DATABASE_URL")
	rep.add(checkDatabaseDSN(dsn))
	rep.add(checkDatabaseSSLMode(dsn))
	rep.add(checkDatabasePool())

	// --- database ----------------------------------------------------------
	var st *store.Store
	if o.offline {
		rep.add(dSkip("db.connect", "skipped (--offline)"))
		rep.add(dSkip("db.migrations", "skipped (--offline)"))
	} else if dsn == "" {
		rep.add(dSkip("db.connect", "skipped: JANUS_DATABASE_URL is not set"))
		rep.add(dSkip("db.migrations", "skipped: JANUS_DATABASE_URL is not set"))
	} else {
		var c doctorCheck
		st, c = checkDatabaseConnect(ctx, dsn, o.timeout, scrub)
		rep.add(c)
		if st != nil {
			defer st.Close()
			rep.add(checkSchemaVersion(ctx, st, o.timeout, scrub))
		} else {
			rep.add(dSkip("db.migrations", "skipped: no database connection"))
		}
	}

	// --- seal --------------------------------------------------------------
	rep.add(checkSeal(ctx, st, o.timeout, scrub))

	// --- passkeys (the motivating case) ------------------------------------
	waCfg := buildWebAuthnConfig()
	tlsCfg, tlsErr := buildTLSConfig()
	listen := os.Getenv("JANUS_LISTEN_ADDR")
	rep.add(checkWebAuthnConfig(waCfg))
	rep.add(checkWebAuthnOrigins(ctx, waCfg, listen, o))

	// --- transport ---------------------------------------------------------
	rep.add(checkTLS(tlsCfg, tlsErr, listen, scrub))
	rep.add(checkOutbound())

	// --- observability + optional subsystems -------------------------------
	rep.add(checkMetrics())
	rep.add(checkLogging())
	rep.add(checkOIDCGroupMaxAge())
	rep.add(checkHTTPLimits())
	rep.add(checkAuditShipping())
	rep.add(checkBackupSchedule())

	// --- running server (optional) -----------------------------------------
	rep.add(checkServerReachable(ctx, o, listen, tlsCfg, scrub))

	finalizeReport(rep, o.strict, scrub)
	return rep
}

// finalizeReport scrubs every operator-facing string, tallies the statuses and
// computes the exit verdict.
func finalizeReport(rep *doctorReport, strict bool, scrub *scrubber) {
	for i := range rep.Checks {
		c := &rep.Checks[i]
		c.Summary = scrub.clean(c.Summary)
		c.Fix = scrub.clean(c.Fix)
		for j := range c.Detail {
			c.Detail[j] = scrub.clean(c.Detail[j])
		}
		switch c.Status {
		case statusPass:
			rep.Summary.Pass++
		case statusWarn:
			rep.Summary.Warn++
		case statusFail:
			rep.Summary.Fail++
		case statusSkip:
			rep.Summary.Skip++
		}
	}
	rep.OK = rep.Summary.Fail == 0 && (!strict || rep.Summary.Warn == 0)
}

// writeDoctorReport renders the report as text or JSON.
func writeDoctorReport(w io.Writer, rep *doctorReport, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	if _, err := fmt.Fprintf(w, "janus doctor — %s\n\n", rep.Version); err != nil {
		return err
	}
	width := 0
	for _, c := range rep.Checks {
		if len(c.Name) > width {
			width = len(c.Name)
		}
	}
	for _, c := range rep.Checks {
		if _, err := fmt.Fprintf(w, "  %-4s  %-*s  %s\n", c.Status, width, c.Name, c.Summary); err != nil {
			return err
		}
		for _, d := range c.Detail {
			if _, err := fmt.Fprintf(w, "        %-*s    %s\n", width, "", d); err != nil {
				return err
			}
		}
		if c.Fix != "" {
			if _, err := fmt.Fprintf(w, "        %-*s    fix: %s\n", width, "", c.Fix); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintf(w, "\n%d passed, %d warning(s), %d failed, %d skipped\n",
		rep.Summary.Pass, rep.Summary.Warn, rep.Summary.Fail, rep.Summary.Skip)
	return err
}

// ---------------------------------------------------------------------------
// Redaction
// ---------------------------------------------------------------------------

// scrubber replaces every known secret value with a placeholder in any string
// doctor is about to emit. The checks are written not to include secret
// material in the first place; this is the belt-and-braces second line, because
// third-party error strings (pgx, net/http, x509) can echo back a URL that
// carries a password.
type scrubber struct{ secrets []string }

// newScrubber collects the secret-bearing values from the process environment.
// Only values are collected — never names — and each is matched literally.
func newScrubber() *scrubber {
	s := &scrubber{}
	if u, err := url.Parse(os.Getenv("JANUS_DATABASE_URL")); err == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok {
			s.addSecret(pw)
			// pgx and net/url both surface the percent-encoded spelling.
			s.addSecret(url.QueryEscape(pw))
		}
	}
	for _, name := range []string{
		"JANUS_METRICS_TOKEN",
		"JANUS_AUDIT_SHIP_WEBHOOK_HMAC_KEY",
		"JANUS_BACKUP_S3_SECRET_ACCESS_KEY",
		"JANUS_BACKUP_S3_ACCESS_KEY_ID",
		"JANUS_TOKEN",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
		"http_proxy", "https_proxy", "all_proxy",
	} {
		s.addSecret(os.Getenv(name))
	}
	return s
}

// addSecret registers a value for redaction. Values shorter than 4 characters
// are ignored: redacting them would corrupt unrelated text without protecting
// anything meaningful.
func (s *scrubber) addSecret(v string) {
	if len(strings.TrimSpace(v)) < 4 {
		return
	}
	s.secrets = append(s.secrets, v)
}

func (s *scrubber) clean(in string) string {
	if in == "" {
		return in
	}
	for _, sec := range s.secrets {
		in = strings.ReplaceAll(in, sec, "[redacted]")
	}
	return in
}

// redactDSN renders a database URL safe to print: the password is replaced and
// the query string is reduced to sslmode (the only part doctor reports on). A
// DSN that does not parse as a URL is reported by shape only.
func redactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return "(unparseable connection string)"
	}
	out := u.Scheme + "://"
	if u.User != nil {
		out += u.User.Username()
		if _, ok := u.User.Password(); ok {
			out += ":[redacted]"
		}
		out += "@"
	}
	out += u.Host + u.Path
	if m := dsnParam(dsn, "sslmode"); m != "" {
		out += "?sslmode=" + m
	}
	return out
}

// dsnParam returns a single query parameter from a URL-shaped DSN.
func dsnParam(dsn, key string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	return u.Query().Get(key)
}

// ---------------------------------------------------------------------------
// Check: unknown JANUS_* variables
// ---------------------------------------------------------------------------

// knownEnvVars is the complete set of JANUS_* variables any part of Janus
// reads, plus the two used by the repo's own tooling. A variable outside this
// set is silently ignored by the server, which is exactly how a typo like
// JANUS_WEBAUTHN_ORIGIN (singular) costs an afternoon.
//
// TestDoctorEnvAllowlistCoversSource walks the source tree and fails if any
// os.Getenv("JANUS_…") literal is missing here, so the list cannot drift.
var knownEnvVars = map[string]bool{
	// server: core
	"JANUS_DATABASE_URL":   true,
	"JANUS_LISTEN_ADDR":    true,
	"JANUS_SEAL_TYPE":      true,
	"JANUS_SHUTDOWN_GRACE": true,
	// server: schedulers
	"JANUS_ROTATION_TICK":    true,
	"JANUS_SYNC_TICK":        true,
	"JANUS_SYNC_VERIFY_TICK": true,
	"JANUS_DYNAMIC_TICK":     true,
	"JANUS_NOTIFY_TICK":      true,
	// server: sessions + lockout
	"JANUS_SESSION_IDLE_TIMEOUT": true,
	"JANUS_LOCKOUT_ENABLED":      true,
	"JANUS_LOCKOUT_THRESHOLD":    true,
	"JANUS_LOCKOUT_BASE":         true,
	"JANUS_LOCKOUT_MAX":          true,
	"JANUS_BREAKGLASS_MAX_TTL":   true,
	// server: HTTP hardening
	"JANUS_HTTP_READ_TIMEOUT":   true,
	"JANUS_HTTP_WRITE_TIMEOUT":  true,
	"JANUS_HTTP_IDLE_TIMEOUT":   true,
	"JANUS_HTTP_MAX_BODY_BYTES": true,
	// server: db pool
	"JANUS_DB_MAX_CONNS":          true,
	"JANUS_DB_MIN_CONNS":          true,
	"JANUS_DB_MAX_CONN_LIFETIME":  true,
	"JANUS_DB_MAX_CONN_IDLE_TIME": true,
	// server: retention
	"JANUS_AUDIT_RETAIN_MIN_DAYS":      true,
	"JANUS_AUDIT_RETAIN_MIN_EVENTS":    true,
	"JANUS_SECRET_RETAIN_MIN_VERSIONS": true,
	"JANUS_SECRET_RETAIN_MIN_DAYS":     true,
	"JANUS_UNUSED_SECRET_DAYS":         true,
	"JANUS_OIDC_GROUP_MAX_AGE":         true,
	// server: audit shipping
	"JANUS_AUDIT_SHIP_MODE":             true,
	"JANUS_AUDIT_SHIP_TICK":             true,
	"JANUS_AUDIT_SHIP_WEBHOOK_URL":      true,
	"JANUS_AUDIT_SHIP_WEBHOOK_HMAC_KEY": true,
	"JANUS_AUDIT_SHIP_SYSLOG_NETWORK":   true,
	"JANUS_AUDIT_SHIP_SYSLOG_ADDR":      true,
	// server: scheduled backups
	"JANUS_BACKUP_TICK":                 true,
	"JANUS_BACKUP_RETENTION":            true,
	"JANUS_BACKUP_S3_BUCKET":            true,
	"JANUS_BACKUP_S3_PREFIX":            true,
	"JANUS_BACKUP_S3_REGION":            true,
	"JANUS_BACKUP_S3_ENDPOINT":          true,
	"JANUS_BACKUP_S3_ACCESS_KEY_ID":     true,
	"JANUS_BACKUP_S3_SECRET_ACCESS_KEY": true,
	// server: KMS auto-unseal
	"JANUS_AWS_KMS_KEY_ARN":    true,
	"JANUS_GCP_KMS_KEY":        true,
	"JANUS_AZURE_KEYVAULT_URL": true,
	"JANUS_AZURE_KEY_NAME":     true,
	"JANUS_AZURE_KEY_VERSION":  true,
	// server: passkeys
	"JANUS_WEBAUTHN_RP_ID":   true,
	"JANUS_WEBAUTHN_ORIGINS": true,
	"JANUS_WEBAUTHN_RP_NAME": true,
	// server: TLS
	"JANUS_TLS_CERT":          true,
	"JANUS_TLS_KEY":           true,
	"JANUS_TLS_ACME_DOMAINS":  true,
	"JANUS_TLS_ACME_EMAIL":    true,
	"JANUS_TLS_ACME_CACHE":    true,
	"JANUS_TLS_REDIRECT_HTTP": true,
	// server: egress + observability
	"JANUS_OUTBOUND_BLOCK_PRIVATE": true,
	"JANUS_OUTBOUND_ALLOW":         true,
	"JANUS_OUTBOUND_ALLOW_PROXY":   true,
	"JANUS_OUTBOUND_POLICY_LOCKED": true,
	"JANUS_METRICS_TOKEN":          true,
	"JANUS_LOG_LEVEL":              true,
	"JANUS_LOG_FORMAT":             true,
	// client / CLI
	"JANUS_ADDR":       true,
	"JANUS_TOKEN":      true,
	"JANUS_CONFIG_DIR": true,
	"JANUS_PROJECT":    true,
	"JANUS_ENV":        true,
	"JANUS_CONFIG":     true,
	"JANUS_RUN_CHILD":  true,
	// repo tooling (scripts/dev-unseal.sh, web/ e2e harness)
	"JANUS_BIN":          true,
	"JANUS_E2E_BASE_URL": true,
}

func checkUnknownEnv() doctorCheck {
	const name = "env.unknown"
	var unknown []string
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		k := kv[:i]
		if !strings.HasPrefix(k, "JANUS_") || knownEnvVars[k] {
			continue
		}
		unknown = append(unknown, k)
	}
	if len(unknown) == 0 {
		return dPass(name, "no unrecognised JANUS_* variables")
	}
	sort.Strings(unknown)
	detail := make([]string, 0, len(unknown))
	for _, k := range unknown {
		if near := nearestKnownEnv(k); near != "" {
			detail = append(detail, fmt.Sprintf("%s — did you mean %s?", k, near))
			continue
		}
		detail = append(detail, k)
	}
	return dWarn(name,
		fmt.Sprintf("%d unrecognised JANUS_* variable(s) — the server ignores these silently", len(unknown)),
		"correct the spelling or unset the variable; a misspelled variable never takes effect and never errors",
		detail...)
}

// nearestKnownEnv returns the closest known variable name within a small edit
// distance, so a one-character slip is named outright rather than left as a
// puzzle. Returns "" when nothing is close enough to be a confident suggestion.
func nearestKnownEnv(k string) string {
	best, bestDist := "", 1<<30
	// Allow one edit per 8 characters, min 1, max 3 — enough for a missing
	// plural or a transposition, not enough to suggest an unrelated variable.
	budget := len(k) / 8
	if budget < 1 {
		budget = 1
	}
	if budget > 3 {
		budget = 3
	}
	for known := range knownEnvVars {
		d := editDistance(k, known)
		if d < bestDist {
			best, bestDist = known, d
		}
	}
	if bestDist > budget {
		return ""
	}
	return best
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// ---------------------------------------------------------------------------
// Check: whole-environment parse (would `janus server` start?)
// ---------------------------------------------------------------------------

func checkConfigParse() doctorCheck {
	const name = "config.parse"
	// Discard the advisory warnings: the individual checks below report them.
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := buildBootConfig(quiet); err != nil {
		if errors.Is(err, errNoDatabaseURL) {
			return dSkip(name, "not evaluated: the database URL is missing (see db.dsn)")
		}
		return dFail(name, "the server would refuse to start: "+err.Error(),
			"correct the variable named in the message; `janus server` applies exactly this parse at boot")
	}
	return dPass(name, "the JANUS_* environment parses; `janus server` would accept it")
}

// ---------------------------------------------------------------------------
// Check: database
// ---------------------------------------------------------------------------

func checkDatabaseDSN(dsn string) doctorCheck {
	const name = "db.dsn"
	if dsn == "" {
		return dFail(name, "JANUS_DATABASE_URL is not set",
			"set JANUS_DATABASE_URL=postgres://user:password@host:5432/janus?sslmode=require")
	}
	// Parse it exactly as the store does, so doctor agrees with `janus server`.
	if _, err := pgxpool.ParseConfig(dsn); err != nil {
		return dFail(name, "JANUS_DATABASE_URL does not parse: "+err.Error(),
			"use a URL-shaped DSN: postgres://user:password@host:5432/janus?sslmode=require")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return dWarn(name, "JANUS_DATABASE_URL parses but is not a postgres:// URL",
			"golang-migrate needs a URL-shaped DSN; use postgres://user:password@host:5432/janus",
			"parsed as: "+redactDSN(dsn))
	}
	return dPass(name, redactDSN(dsn))
}

func checkDatabaseSSLMode(dsn string) doctorCheck {
	const name = "db.sslmode"
	if dsn == "" {
		return dSkip(name, "skipped: JANUS_DATABASE_URL is not set")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return dSkip(name, "skipped: the connection string does not parse as a URL")
	}
	host := u.Hostname()
	local := hostIsLoopback(host)
	mode := u.Query().Get("sslmode")
	switch mode {
	case "verify-full", "verify-ca", "require":
		return dPass(name, fmt.Sprintf("sslmode=%s", mode))
	case "disable":
		if local {
			return dPass(name, "sslmode=disable to a loopback database (no network exposure)")
		}
		return dWarn(name,
			fmt.Sprintf("sslmode=disable to a non-local database (%s) — credentials and ciphertext cross the network in the clear", host),
			"add ?sslmode=require to JANUS_DATABASE_URL (sslmode=verify-full with a CA bundle is better)")
	case "":
		if local {
			return dPass(name, "sslmode unset (libpq default \"prefer\") to a loopback database")
		}
		return dWarn(name,
			fmt.Sprintf("sslmode is unset for a non-local database (%s); the default \"prefer\" silently falls back to plaintext", host),
			"set ?sslmode=require (or verify-full) explicitly in JANUS_DATABASE_URL")
	default:
		return dWarn(name, fmt.Sprintf("unrecognised sslmode=%q", mode),
			"use one of: disable, allow, prefer, require, verify-ca, verify-full")
	}
}

func checkDatabasePool() doctorCheck {
	const name = "db.pool"
	pc, err := parsePoolConfig()
	if err != nil {
		return dFail(name, err.Error(), "correct the JANUS_DB_* value named in the message")
	}
	if pc == (store.PoolConfig{}) {
		return dPass(name, "JANUS_DB_* unset — pgx defaults apply")
	}
	var detail []string
	if pc.MaxConns > 0 {
		detail = append(detail, fmt.Sprintf("max_conns=%d", pc.MaxConns))
	}
	if pc.MinConns > 0 {
		detail = append(detail, fmt.Sprintf("min_conns=%d", pc.MinConns))
	}
	if pc.MaxConnLifetime > 0 {
		detail = append(detail, "max_conn_lifetime="+pc.MaxConnLifetime.String())
	}
	if pc.MaxConnIdleTime > 0 {
		detail = append(detail, "max_conn_idle_time="+pc.MaxConnIdleTime.String())
	}
	joined := strings.Join(detail, ", ")
	if pc.MaxConns > 0 && pc.MinConns > pc.MaxConns {
		return dFail(name,
			fmt.Sprintf("JANUS_DB_MIN_CONNS (%d) exceeds JANUS_DB_MAX_CONNS (%d); pgx refuses this pool", pc.MinConns, pc.MaxConns),
			"lower JANUS_DB_MIN_CONNS below JANUS_DB_MAX_CONNS", joined)
	}
	if pc.MaxConnLifetime > 0 && pc.MaxConnIdleTime >= pc.MaxConnLifetime {
		return dWarn(name,
			"JANUS_DB_MAX_CONN_IDLE_TIME is not below JANUS_DB_MAX_CONN_LIFETIME, so idle connections are never reaped early",
			"set JANUS_DB_MAX_CONN_IDLE_TIME well below JANUS_DB_MAX_CONN_LIFETIME (e.g. 30m vs 1h)", joined)
	}
	return dPass(name, "pool tuning is coherent", joined)
}

func checkDatabaseConnect(ctx context.Context, dsn string, timeout time.Duration, scrub *scrubber) (*store.Store, doctorCheck) {
	const name = "db.connect"
	pc, _ := parsePoolConfig() // a bad value is already reported by db.pool
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	st, err := store.OpenWithConfig(cctx, dsn, pc)
	if err != nil {
		return nil, dFail(name, "cannot reach the database: "+scrub.clean(err.Error()),
			"check the host, port, credentials and that Postgres accepts connections from this host",
			"target: "+redactDSN(dsn))
	}
	return st, dPass(name, "connected to "+redactDSN(dsn))
}

func checkSchemaVersion(ctx context.Context, st *store.Store, timeout time.Duration, scrub *scrubber) doctorCheck {
	const name = "db.migrations"
	want, err := embeddedSchemaVersion()
	if err != nil {
		return dFail(name, "cannot read the embedded migration set: "+err.Error(),
			"this is a build problem — reinstall the janus binary")
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	got, err := st.SchemaVersion(cctx)
	if err != nil {
		if strings.Contains(err.Error(), "dirty") {
			return dFail(name, "the schema_migrations table is DIRTY — a migration failed part-way",
				"restore from a backup, or resolve the failed migration manually and clear the dirty flag; never back up or restore over a dirty schema")
		}
		// No schema_migrations table (42P01) or no row in it: the database is
		// reachable but has never been migrated. That is the expected state of a
		// fresh Postgres, not a fault — the server migrates on its first boot.
		if errors.Is(err, store.ErrNotFound) ||
			strings.Contains(err.Error(), "42P01") ||
			strings.Contains(err.Error(), "schema_migrations\" does not exist") {
			return dWarn(name,
				fmt.Sprintf("the database has no schema yet (this binary embeds version %d)", want),
				"start the server once — `janus server` applies migrations at boot — or run `janus migrate`")
		}
		return dFail(name, "cannot read the schema version: "+scrub.clean(err.Error()),
			"confirm the connection user can read the schema_migrations table")
	}
	switch {
	case got == want:
		return dPass(name, fmt.Sprintf("schema is current (version %d)", got))
	case got < want:
		return dWarn(name,
			fmt.Sprintf("schema is at version %d, this binary embeds %d — %d migration(s) pending", got, want, want-got),
			"start the server (it migrates at boot) or run `janus migrate` before serving traffic")
	default:
		return dFail(name,
			fmt.Sprintf("schema is at version %d but this binary only embeds %d — the database was migrated by a NEWER janus", got, want),
			"run the newer janus binary against this database; downgrading the schema is not supported")
	}
}

// embeddedSchemaVersion is the highest migration number compiled into this
// binary — the version a successful boot migrates the database to.
func embeddedSchemaVersion() (int64, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return 0, err
	}
	var maxVer int64
	for _, e := range entries {
		nm := e.Name()
		if !strings.HasSuffix(nm, ".up.sql") {
			continue
		}
		i := strings.IndexByte(nm, '_')
		if i <= 0 {
			continue
		}
		n, err := strconv.ParseInt(nm[:i], 10, 64)
		if err != nil {
			continue
		}
		if n > maxVer {
			maxVer = n
		}
	}
	if maxVer == 0 {
		return 0, errors.New("no migrations found in the embedded set")
	}
	return maxVer, nil
}

// ---------------------------------------------------------------------------
// Check: seal
// ---------------------------------------------------------------------------

// sealProviderVars names the environment each cloud-KMS seal type needs.
var sealProviderVars = map[string][]string{
	crypto.SealTypeAWSKMS: {"JANUS_AWS_KMS_KEY_ARN"},
	crypto.SealTypeGCPKMS: {"JANUS_GCP_KMS_KEY"},
	crypto.SealTypeAzureKV: {
		"JANUS_AZURE_KEYVAULT_URL",
		"JANUS_AZURE_KEY_NAME",
	},
}

func knownSealType(t string) bool {
	switch t {
	case crypto.SealTypeShamir, crypto.SealTypeAWSKMS, crypto.SealTypeGCPKMS, crypto.SealTypeAzureKV:
		return true
	}
	return false
}

func checkSeal(ctx context.Context, st *store.Store, timeout time.Duration, scrub *scrubber) doctorCheck {
	const name = "seal.type"
	envType := strings.TrimSpace(os.Getenv("JANUS_SEAL_TYPE"))
	if envType != "" && !knownSealType(envType) {
		return dFail(name, fmt.Sprintf("JANUS_SEAL_TYPE=%q is not a known seal type", envType),
			"use one of: shamir, awskms, gcpkms, azurekv")
	}

	var detail []string
	storedType := ""
	initialized := false
	if st == nil {
		detail = append(detail, "stored seal type not verified (no database connection)")
	} else {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cfg, err := store.NewSealConfigStore(st).Get(cctx)
		switch {
		case errors.Is(err, crypto.ErrNoSealConfig):
			detail = append(detail, "the seal is not initialized yet")
		case err != nil:
			detail = append(detail, "could not read the stored seal config: "+scrub.clean(err.Error()))
		default:
			initialized = true
			storedType = cfg.Type
		}
	}

	// The stored type is authoritative after init; a disagreeing env var is a
	// fatal boot error, and a very common one after a seal-type migration.
	if initialized && envType != "" && envType != storedType {
		return dFail(name,
			fmt.Sprintf("seal type mismatch: JANUS_SEAL_TYPE=%q but the stored seal is %q — the server refuses to boot", envType, storedType),
			fmt.Sprintf("set JANUS_SEAL_TYPE=%s to match the stored seal, or unset it (the stored type is authoritative)", storedType))
	}
	if !initialized && envType == "" && st != nil {
		return dFail(name, "the seal is not initialized and JANUS_SEAL_TYPE is not set",
			"set JANUS_SEAL_TYPE (shamir for Shamir shares, or awskms/gcpkms/azurekv for auto-unseal) before running `janus init`")
	}

	effective := envType
	if initialized {
		effective = storedType
		if envType == "" {
			detail = append(detail, "JANUS_SEAL_TYPE is unset; the stored type is used")
		}
	}
	if effective == "" {
		return dWarn(name, "the effective seal type is unknown",
			"set JANUS_SEAL_TYPE, or run doctor with database access so the stored type can be read", detail...)
	}

	var missing []string
	for _, v := range sealProviderVars[effective] {
		if strings.TrimSpace(os.Getenv(v)) == "" {
			missing = append(missing, v)
		}
	}
	if len(missing) > 0 {
		return dFail(name,
			fmt.Sprintf("seal type %q is missing required configuration: %s", effective, strings.Join(missing, ", ")),
			"set "+strings.Join(missing, " and ")+"; auto-unseal cannot build its KMS client without it",
			detail...)
	}
	if effective == crypto.SealTypeShamir {
		detail = append(detail, "shamir: the server boots sealed and needs `janus unseal` (threshold shares) after every restart")
	}
	return dPass(name, fmt.Sprintf("seal type %q", effective), detail...)
}

// ---------------------------------------------------------------------------
// Check: WebAuthn / passkeys
// ---------------------------------------------------------------------------

func checkWebAuthnConfig(cfg auth.WebAuthnConfig) doctorCheck {
	const name = "webauthn.config"
	if cfg.RPID == "" && len(cfg.Origins) == 0 {
		return dPass(name, "passkeys are disabled (JANUS_WEBAUTHN_RP_ID / _ORIGINS unset)")
	}
	if err := cfg.Validate(); err != nil {
		return dFail(name, err.Error(),
			"JANUS_WEBAUTHN_RP_ID must be a bare lower-case host and every JANUS_WEBAUTHN_ORIGINS entry must be scheme://host[:port] whose host is that RP ID or a subdomain of it")
	}
	return dPass(name,
		fmt.Sprintf("rp_id=%s, origins=%s", cfg.RPID, strings.Join(cfg.Origins, " ")))
}

// checkWebAuthnOrigins is the check this command was built for.
//
// JANUS_WEBAUTHN_ORIGINS is internally valid the moment each origin's host
// matches the RP ID — which is all boot-time validation can assert. It is still
// wrong if the port or scheme does not describe how a browser actually reaches
// this server, and the resulting failure looks like a product bug: the ceremony
// simply fails, with no server-side error, because the browser refuses to hand
// the assertion to an origin it did not come from.
//
// The verdict comes from the one relationship that is decidable without
// guessing: a LOOPBACK origin names a port on this very host, so unless that
// port is the port this server binds, the browser is not talking to this
// process. (A probe alone is not enough — the incident host also ran an
// unrelated Janus on the misconfigured port, and "a Janus answers" would have
// passed it. The port comparison is the evidence; the probe only says which
// flavour of wrong it is.)
//
// Deliberate exceptions, so the check does not cry wolf:
//   - Inside a container the comparison is meaningless: the published host port
//     is invisible from within the namespace. Loopback origins are SKIPped.
//   - Ports 80 and 443 are where a local reverse proxy terminates, so a
//     loopback origin on one of them is reported as a note, not a problem.
//   - A non-loopback origin is only judged on DNS: a public hostname routinely
//     fails to connect from the server itself (no hairpin NAT, split horizon),
//     but a name that does not resolve at all is nearly always a typo.
func checkWebAuthnOrigins(ctx context.Context, cfg auth.WebAuthnConfig, listenAddr string, o doctorOpts) doctorCheck {
	const name = "webauthn.origins"
	if err := cfg.Validate(); err != nil {
		return dSkip(name, "skipped: the passkey configuration is invalid (see webauthn.config)")
	}
	if !cfg.Enabled() {
		return dSkip(name, "skipped: passkeys are disabled")
	}

	listenPort := listenPortOf(listenAddr)
	inContainer := containerDetector()
	hc := nethard.SafeHTTPClient(o.timeout, nethard.Static(nethard.Policy{}))

	var problems, notes []string
	skipped := 0

	for _, origin := range cfg.Origins {
		u, err := url.Parse(origin)
		if err != nil {
			continue // webauthn.config already failed on this
		}
		host := strings.ToLower(u.Hostname())
		port := originPort(u)

		if !hostIsLoopback(host) {
			// Only a DNS answer is meaningful here.
			if o.offline {
				skipped++
				notes = append(notes, origin+" — not verified (--offline)")
				continue
			}
			switch res := probeJanus(ctx, hc, origin, o.timeout); res.outcome {
			case probeDNSFailure:
				problems = append(problems, fmt.Sprintf(
					"%s — the hostname does not resolve from this host (%s)", origin, res.detail))
			case probeJanusOK:
				notes = append(notes, origin+" — a Janus instance answers here")
			case probeTLSUnverified:
				notes = append(notes, fmt.Sprintf(
					"%s — a TLS server answers here; its certificate is not trusted by this host (%s)", origin, res.detail))
			default:
				notes = append(notes, origin+
					" — resolves, but nothing answered from this host; expected when the origin is only reachable through your proxy or load balancer")
			}
			continue
		}

		// --- loopback origin: decidable from the listen address ---------------
		if inContainer {
			skipped++
			notes = append(notes, origin+
				" — not verified: this process is containerised, where the published host port is invisible")
			continue
		}
		if listenPort == "" {
			skipped++
			notes = append(notes, origin+" — not verified: JANUS_LISTEN_ADDR does not parse as host:port")
			continue
		}
		if port == listenPort {
			notes = append(notes, fmt.Sprintf("%s — matches the listen port (%s)", origin, listenPort))
			continue
		}
		if port == "80" || port == "443" {
			notes = append(notes, fmt.Sprintf(
				"%s — port %s differs from the listen port (%s); assumed to be a local reverse proxy in front of this server",
				origin, port, listenPort))
			continue
		}

		p := fmt.Sprintf("%s names port %s, but this server listens on port %s — a browser loading the UI from that origin is NOT reaching this instance",
			origin, port, listenPort)
		if !o.offline {
			switch res := probeJanus(ctx, hc, origin, o.timeout); res.outcome {
			case probeJanusOK:
				p += "; a DIFFERENT Janus instance is answering on that port"
			case probeNotJanus:
				p += fmt.Sprintf("; something else is bound to that port (%s)", res.detail)
			case probeUnreachable:
				p += "; nothing is listening on that port at all"
			}
		}
		problems = append(problems, p)
	}

	detail := append(problems, notes...)
	if len(problems) > 0 {
		return dWarn(name,
			fmt.Sprintf("%d configured passkey origin(s) do not describe how this server is reached", len(problems)),
			"set JANUS_WEBAUTHN_ORIGINS to the exact scheme://host:port a browser uses to load the Janus UI "+
				"(and JANUS_LISTEN_ADDR to the port it really serves on) — the browser refuses the assertion when they disagree, "+
				"so the ceremony fails with no server-side error and looks like an application bug",
			detail...)
	}
	if skipped > 0 && len(notes) == skipped {
		return dSkip(name, "origins could not be verified from here", detail...)
	}
	return dPass(name, "every configured passkey origin matches how this server is reached", detail...)
}

// ---------------------------------------------------------------------------
// Check: TLS
// ---------------------------------------------------------------------------

func checkTLS(cfg api.TLSConfig, cfgErr error, listenAddr string, scrub *scrubber) doctorCheck {
	const name = "tls"
	if cfgErr != nil {
		return dFail(name, cfgErr.Error(),
			"set JANUS_TLS_CERT and JANUS_TLS_KEY together for static certificates, or JANUS_TLS_ACME_DOMAINS for ACME — never both")
	}

	var detail []string
	// Variables that only take effect in a mode that is not active are a silent
	// no-op today; say so rather than let an operator believe they are in force.
	var ignored []string
	if !cfg.IsACME() {
		if cfg.ACMEEmail != "" {
			ignored = append(ignored, "JANUS_TLS_ACME_EMAIL")
		}
		if cfg.ACMECache != "" {
			ignored = append(ignored, "JANUS_TLS_ACME_CACHE")
		}
	}
	if cfg.RedirectHTTP != "" && !cfg.IsStaticCerts() {
		ignored = append(ignored, "JANUS_TLS_REDIRECT_HTTP")
	}

	switch {
	case cfg.IsACME():
		detail = append(detail, "mode: ACME (Let's Encrypt) for "+strings.Join(cfg.ACMEDomains, ", "))
		detail = append(detail, "ACME needs inbound :80 for the HTTP-01 challenge and a public DNS record per domain")
	case cfg.IsStaticCerts():
		detail = append(detail, "mode: static certificates")
		c, err := loadLeafCert(cfg.CertFile)
		if err != nil {
			return dFail(name, "the configured certificate cannot be used: "+scrub.clean(err.Error()),
				"point JANUS_TLS_CERT at a readable PEM certificate chain and JANUS_TLS_KEY at its private key",
				detail...)
		}
		if err := readableFile(cfg.KeyFile); err != nil {
			return dFail(name, "the configured private key cannot be read: "+scrub.clean(err.Error()),
				"make JANUS_TLS_KEY readable by the janus process (mode 0600, owned by the service user)",
				detail...)
		}
		detail = append(detail, fmt.Sprintf("certificate subject %q, valid %s → %s",
			c.Subject.CommonName,
			c.NotBefore.UTC().Format(time.RFC3339),
			c.NotAfter.UTC().Format(time.RFC3339)))
		now := time.Now()
		if now.After(c.NotAfter) {
			return dFail(name, fmt.Sprintf("the TLS certificate EXPIRED on %s", c.NotAfter.UTC().Format(time.RFC3339)),
				"renew the certificate at JANUS_TLS_CERT and restart; browsers and CLIs reject the instance until you do",
				detail...)
		}
		if now.Before(c.NotBefore) {
			return dFail(name, fmt.Sprintf("the TLS certificate is not valid until %s", c.NotBefore.UTC().Format(time.RFC3339)),
				"check the host clock, or install the certificate that is currently valid", detail...)
		}
		if left := time.Until(c.NotAfter); left < 21*24*time.Hour {
			return dWarn(name, fmt.Sprintf("the TLS certificate expires in %d day(s)", int(left.Hours()/24)),
				"renew it now; there is no automatic renewal in static-certificate mode", detail...)
		}
		// Cheap hostname check against the passkey RP ID, the one hostname we
		// know a browser will use.
		if rp := strings.TrimSpace(os.Getenv("JANUS_WEBAUTHN_RP_ID")); rp != "" {
			if err := c.VerifyHostname(rp); err != nil {
				return dWarn(name,
					fmt.Sprintf("the TLS certificate does not cover %q (the configured passkey RP ID)", rp),
					fmt.Sprintf("reissue the certificate with %s in its subject alternative names, or correct JANUS_WEBAUTHN_RP_ID", rp),
					detail...)
			}
			detail = append(detail, "certificate covers the passkey RP ID "+rp)
		}
	default:
		detail = append(detail,
			"mode: plain HTTP on "+effectiveListenAddr(listenAddr)+" — TLS is expected to terminate at a reverse proxy or ingress (the shipped default)")
		detail = append(detail,
			"if nothing terminates TLS in front of this port, sessions and service tokens travel in the clear")
	}

	if len(ignored) > 0 {
		return dWarn(name,
			fmt.Sprintf("%s set but ignored in the active TLS mode", strings.Join(ignored, ", ")),
			"remove the variable, or switch to the mode that consumes it", detail...)
	}
	return dPass(name, tlsMode(cfg), detail...)
}

// loadLeafCert reads the first certificate from a PEM file.
func loadLeafCert(p string) (*x509.Certificate, error) {
	b, err := os.ReadFile(p) // #nosec G304 -- operator-supplied certificate path from JANUS_TLS_CERT
	if err != nil {
		return nil, err
	}
	for len(b) > 0 {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			break
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		return x509.ParseCertificate(blk.Bytes)
	}
	return nil, fmt.Errorf("no CERTIFICATE block in %s", path.Base(p))
}

// readableFile confirms a path exists and can be opened for reading.
func readableFile(p string) error {
	f, err := os.Open(p) // #nosec G304 -- operator-supplied key path from JANUS_TLS_KEY
	if err != nil {
		return err
	}
	return f.Close()
}

// ---------------------------------------------------------------------------
// Check: outbound / SSRF policy
// ---------------------------------------------------------------------------

func checkOutbound() doctorCheck {
	const name = "outbound.ssrf"

	// A malformed allowlist is fatal at boot, so report it as a failure here
	// rather than describing a policy the server would refuse to start with.
	if err := nethard.ValidateEnv(); err != nil {
		return dFail(name, err.Error(),
			"fix the entry and restart; entries are IP addresses or CIDR prefixes, comma-separated")
	}

	p := nethard.PolicyFromEnv()
	detail := []string{
		"link-local / cloud-metadata ranges (169.254.0.0/16, fe80::/10, fd00:ec2::254) are always blocked at connect time",
	}
	if p.BlockPrivate {
		detail = append(detail, "loopback + RFC1918 + ULA are also blocked (JANUS_OUTBOUND_BLOCK_PRIVATE)")
	} else {
		detail = append(detail, "loopback + RFC1918 + ULA are allowed (default; self-hosted deployments dial internal targets)")
	}
	// doctor is a preflight over the ENVIRONMENT and does not open the database,
	// so it cannot see a stored override. Say so rather than reporting the
	// environment as though it were necessarily what the server will dial under.
	if nethard.PolicyLocked() {
		detail = append(detail, "the policy is pinned to the environment ("+nethard.EnvPolicyLocked+"); it cannot be changed in the app")
	} else {
		detail = append(detail, "a stored policy (Settings → Outbound policy) supersedes these values; check GET /v1/sys/outbound-policy on a running server")
	}
	if allow := nethard.DescribeAllow(p.Allow); len(allow) > 0 {
		detail = append(detail, "exempt from the private-space block ("+nethard.EnvAllow+"): "+strings.Join(allow, ", "))
		// An allowlist without the tightening it exempts from does nothing. Worth
		// a warning: the operator wrote it expecting an effect.
		if !p.BlockPrivate {
			return dWarn(name,
				nethard.EnvAllow+" is set but "+nethard.EnvBlockPrivate+" is not enabled, so the allowlist has no effect",
				"either enable "+nethard.EnvBlockPrivate+" (the allowlist then exempts these destinations) or drop "+nethard.EnvAllow,
				detail...)
		}
	}
	if !p.AllowProxy {
		return dPass(name, "outbound integration calls use the connect-time resolved-IP guard", detail...)
	}
	proxies := nethard.ProxyEnvVarsSet()
	if len(proxies) == 0 {
		return dWarn(name,
			"JANUS_OUTBOUND_ALLOW_PROXY is on, which degrades the SSRF guard to a URL-time host check whenever a proxy is configured",
			"unset JANUS_OUTBOUND_ALLOW_PROXY unless a proxy is the deployment's only egress path", detail...)
	}
	detail = append(detail, "proxy variables in effect: "+strings.Join(proxies, ", "))
	return dWarn(name,
		"outbound calls go through a proxy: the resolved-IP guard cannot see the real destination, so metadata/link-local blocking now applies only to literal-IP targets",
		"unset JANUS_OUTBOUND_ALLOW_PROXY to restore the full guard, or enforce destination allowlisting on the proxy itself",
		detail...)
}

// ---------------------------------------------------------------------------
// Check: observability
// ---------------------------------------------------------------------------

func checkMetrics() doctorCheck {
	const name = "metrics"
	tok := os.Getenv("JANUS_METRICS_TOKEN")
	if tok == "" {
		return dPass(name, "/metrics is disabled (returns 404); set JANUS_METRICS_TOKEN to enable it")
	}
	if len(tok) < 24 {
		return dWarn(name,
			fmt.Sprintf("/metrics is enabled but JANUS_METRICS_TOKEN is only %d characters", len(tok)),
			"use a high-entropy token, e.g. JANUS_METRICS_TOKEN=$(openssl rand -hex 32); it is the only thing gating the endpoint")
	}
	return dPass(name, "/metrics is enabled and bearer-token gated")
}

func checkLogging() doctorCheck {
	const name = "logging"
	level, format, problems := parseLogEnv()
	if len(problems) > 0 {
		return dWarn(name, strings.Join(problems, "; "),
			"set JANUS_LOG_LEVEL to debug|info|warn|error and JANUS_LOG_FORMAT to text|json")
	}
	if level == slog.LevelDebug {
		return dWarn(name, "log level is debug",
			"use info in production; debug output is verbose and increases the surface for accidental disclosure in third-party libraries",
			"format: "+format)
	}
	return dPass(name, fmt.Sprintf("level=%s format=%s", strings.ToLower(level.String()), format))
}

func checkHTTPLimits() doctorCheck {
	const name = "http.limits"
	var problems, detail []string

	if v := os.Getenv("JANUS_HTTP_WRITE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			problems = append(problems, fmt.Sprintf(
				"JANUS_HTTP_WRITE_TIMEOUT=%s truncates long streaming responses (audit export, backup download)", v))
		}
	}
	if v := os.Getenv("JANUS_HTTP_MAX_BODY_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n == 0 {
			problems = append(problems, "JANUS_HTTP_MAX_BODY_BYTES=0 removes the request body cap entirely")
		}
	}
	for _, v := range []struct{ name, val string }{
		{"JANUS_HTTP_READ_TIMEOUT", os.Getenv("JANUS_HTTP_READ_TIMEOUT")},
		{"JANUS_HTTP_IDLE_TIMEOUT", os.Getenv("JANUS_HTTP_IDLE_TIMEOUT")},
	} {
		if v.val == "" {
			continue
		}
		if d, err := time.ParseDuration(v.val); err == nil && d == 0 {
			problems = append(problems, v.name+"=0 disables that timeout, leaving the listener open to slow-request exhaustion")
		} else {
			detail = append(detail, v.name+"="+v.val)
		}
	}
	if len(problems) > 0 {
		return dWarn(name, strings.Join(problems, "; "),
			"leave the JANUS_HTTP_* knobs unset unless you have a measured reason; the defaults (read 30s, idle 120s, no write timeout, 10 MiB body cap) are the tested combination",
			detail...)
	}
	return dPass(name, "HTTP timeouts and body cap are at safe values", detail...)
}

// ---------------------------------------------------------------------------
// Check: audit shipping + scheduled backups
// ---------------------------------------------------------------------------

// auditShipVars are every JANUS_AUDIT_SHIP_* variable other than the mode
// switch — used to detect a half-configured destination.
var auditShipVars = []string{
	"JANUS_AUDIT_SHIP_WEBHOOK_URL",
	"JANUS_AUDIT_SHIP_WEBHOOK_HMAC_KEY",
	"JANUS_AUDIT_SHIP_SYSLOG_NETWORK",
	"JANUS_AUDIT_SHIP_SYSLOG_ADDR",
	"JANUS_AUDIT_SHIP_TICK",
}

func checkAuditShipping() doctorCheck {
	const name = "audit.shipping"
	cfg, err := auditship.ConfigFromEnv()
	if err != nil {
		return dFail(name, err.Error(),
			"correct the JANUS_AUDIT_SHIP_* variable named in the message; a bad destination fails the boot rather than dropping the audit stream")
	}
	if !cfg.Enabled() {
		var set []string
		for _, v := range auditShipVars {
			if strings.TrimSpace(os.Getenv(v)) != "" {
				set = append(set, v)
			}
		}
		if len(set) > 0 {
			return dWarn(name,
				"audit shipping is configured but switched off: JANUS_AUDIT_SHIP_MODE is unset/off, so nothing ships",
				"set JANUS_AUDIT_SHIP_MODE=webhook or =syslog to activate the destination, or unset the other variables",
				"set but inert: "+strings.Join(set, ", "))
		}
		return dPass(name, "audit shipping is off (JANUS_AUDIT_SHIP_MODE unset)")
	}

	var detail []string
	if cfg.Mode == auditship.ModeWebhook {
		detail = append(detail, "mode: webhook")
		if cfg.WebhookHMACKey == "" {
			return dWarn(name, "webhook audit shipping has no JANUS_AUDIT_SHIP_WEBHOOK_HMAC_KEY",
				"set a shared secret so the receiver can authenticate the shipped batches", detail...)
		}
		detail = append(detail, "batches are HMAC-signed")
	} else {
		detail = append(detail, fmt.Sprintf("mode: syslog over %s to %s", cfg.SyslogNetwork, cfg.SyslogAddr))
	}
	if v := os.Getenv("JANUS_AUDIT_SHIP_TICK"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d == 0 {
			return dWarn(name, "a destination is configured but JANUS_AUDIT_SHIP_TICK=0 stops the scheduler",
				"remove JANUS_AUDIT_SHIP_TICK (default 30s) or set a positive duration", detail...)
		}
	}
	return dPass(name, "audit shipping is active", detail...)
}

func checkBackupSchedule() doctorCheck {
	const name = "backup.schedule"
	cfg, err := parseBackupSchedule(version.Version)
	if err != nil {
		return dFail(name, err.Error(),
			"complete the JANUS_BACKUP_S3_* configuration, or unset JANUS_BACKUP_TICK to leave scheduled backups off")
	}
	if !cfg.Enabled() {
		if cfg.S3.Bucket != "" {
			return dWarn(name,
				"a backup bucket is configured but JANUS_BACKUP_TICK is unset/zero, so scheduled backups never run",
				"set JANUS_BACKUP_TICK (e.g. 6h) to enable the scheduler, or unset JANUS_BACKUP_S3_* to make the intent clear",
				"bucket: "+cfg.S3.Bucket)
		}
		return dPass(name, "scheduled S3 backups are off (JANUS_BACKUP_TICK unset)")
	}
	detail := []string{
		fmt.Sprintf("every %s to s3://%s/%s (region %s)", cfg.Tick, cfg.S3.Bucket, cfg.S3.Prefix, cfg.S3.Region),
	}
	if cfg.Retention == 0 {
		detail = append(detail, "retention: keep all objects (JANUS_BACKUP_RETENTION unset)")
	}
	return dPass(name, "scheduled S3 backups are enabled", detail...)
}

// ---------------------------------------------------------------------------
// Check: running server
// ---------------------------------------------------------------------------

func checkServerReachable(ctx context.Context, o doctorOpts, listenAddr string, tlsCfg api.TLSConfig, scrub *scrubber) doctorCheck {
	const name = "server.status"
	if o.offline {
		return dSkip(name, "skipped (--offline)")
	}
	base := o.address
	if base == "" {
		base = os.Getenv("JANUS_ADDR")
	}
	if base == "" {
		base = localServerURL(listenAddr, tlsCfg.Enabled())
	}
	if base == "" {
		return dSkip(name, "skipped: no server address to probe")
	}

	cctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	hc := nethard.SafeHTTPClient(o.timeout, nethard.Static(nethard.Policy{}))
	var body struct {
		Status      string `json:"status"`
		Initialized bool   `json:"initialized"`
		Sealed      bool   `json:"sealed"`
	}
	target, err := sanitizeProbeURL(base, "/v1/sys/health")
	if err != nil {
		return dSkip(name, "skipped: "+scrub.clean(err.Error()))
	}
	// #nosec G704 -- the target is rebuilt by sanitizeProbeURL from parsed
	// scheme+host only (http/https enforced, no user-controlled path), and the
	// client is nethard-guarded: it re-checks the resolved IP at connect time on
	// every dial. This is an operator running a diagnostic against their own
	// server address, not an attacker-supplied URL crossing a trust boundary.
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, target, nil)
	if err != nil {
		return dSkip(name, "skipped: "+scrub.clean(err.Error()))
	}
	resp, err := hc.Do(req) // #nosec G704 -- see the request construction above
	if err != nil {
		return dSkip(name, fmt.Sprintf("no server answered at %s — doctor's other checks do not need one", base),
			scrub.clean(err.Error()))
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body); err != nil || body.Status != "ok" {
		return dWarn(name, fmt.Sprintf("%s answered, but not with a Janus health response (HTTP %d)", base, resp.StatusCode),
			"point --address at the Janus instance; something else is bound to this address")
	}
	switch {
	case !body.Initialized:
		return dWarn(name, fmt.Sprintf("%s is running but the seal is NOT INITIALIZED", base),
			"run `janus init` once to create the master key (it prints the Shamir shares exactly once)")
	case body.Sealed:
		return dWarn(name, fmt.Sprintf("%s is running but SEALED — every secret operation returns 503", base),
			"run `janus unseal` once per share until the threshold is met (or retry auto-unseal for a KMS seal)")
	default:
		return dPass(name, fmt.Sprintf("%s is running and unsealed", base))
	}
}

// ---------------------------------------------------------------------------
// Probing helpers
// ---------------------------------------------------------------------------

type probeOutcome int

const (
	probeJanusOK probeOutcome = iota
	probeNotJanus
	probeUnreachable
	probeDNSFailure
	probeTLSUnverified
)

type probeResult struct {
	outcome probeOutcome
	detail  string
}

// sanitizeProbeURL rebuilds a probe target from a base URL's parsed scheme and
// host ONLY, appending a fixed path of doctor's choosing. Anything the operator
// wrote beyond scheme://host[:port] — path, query, fragment, credentials — is
// discarded rather than forwarded, so the request doctor issues is always
// exactly the liveness/health probe it intends.
func sanitizeProbeURL(base, fixedPath string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", fmt.Errorf("%q is not a URL", base)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%q must be an http:// or https:// URL", base)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%q has no host", base)
	}
	clean := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: fixedPath}
	return clean.String(), nil
}

// probeJanus asks whether a Janus instance answers at base by calling the
// liveness probe, which touches neither the database nor the keyring.
func probeJanus(ctx context.Context, hc *http.Client, base string, timeout time.Duration) probeResult {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	target, err := sanitizeProbeURL(base, "/v1/sys/live")
	if err != nil {
		return probeResult{outcome: probeUnreachable, detail: "not a usable URL"}
	}
	// #nosec G704 -- see checkServerReachable: sanitized scheme+host, fixed
	// path, nethard-guarded client with a connect-time resolved-IP check.
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, target, nil)
	if err != nil {
		return probeResult{outcome: probeUnreachable, detail: "not a usable URL"}
	}
	resp, err := hc.Do(req) // #nosec G704 -- see the request construction above
	if err != nil {
		return classifyProbeError(err)
	}
	defer resp.Body.Close()
	var body struct {
		Status string `json:"status"`
	}
	if derr := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body); derr != nil || body.Status != "live" {
		return probeResult{outcome: probeNotJanus, detail: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	return probeResult{outcome: probeJanusOK}
}

// classifyProbeError separates the three failures that mean different things:
// a name that does not resolve (almost always a typo), a certificate this host
// cannot verify (the server IS there), and nothing listening.
func classifyProbeError(err error) probeResult {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return probeResult{outcome: probeDNSFailure, detail: "no such host"}
	}
	var certErr *tls.CertificateVerificationError
	var authErr x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	if errors.As(err, &certErr) || errors.As(err, &authErr) || errors.As(err, &hostErr) {
		return probeResult{outcome: probeTLSUnverified, detail: "certificate not trusted by this host"}
	}
	// Deliberately terse: a transport error string can carry the full URL.
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return probeResult{outcome: probeUnreachable, detail: "timed out"}
	}
	return probeResult{outcome: probeUnreachable, detail: "connection failed"}
}

// containerDetector is the indirection tests use to pin the containerised /
// not-containerised branch of the origin check; production always uses the real
// probe.
var containerDetector = runningInContainer

// runningInContainer reports whether this process looks containerised. Inside a
// container the published host port is invisible, so a loopback origin naming a
// different port is expected rather than suspicious.
func runningInContainer() bool {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// hostIsLoopback reports whether a host names the local machine.
func hostIsLoopback(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// originPort returns an origin's effective TCP port, filling in the scheme
// default when it is written without one.
func originPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// effectiveListenAddr renders the address the server binds, defaults included.
func effectiveListenAddr(listenAddr string) string {
	if strings.TrimSpace(listenAddr) == "" {
		return ":8200 (default)"
	}
	return listenAddr
}

// listenPortOf extracts the port the server binds, or "" when it cannot be
// determined.
func listenPortOf(listenAddr string) string {
	if strings.TrimSpace(listenAddr) == "" {
		return "8200"
	}
	_, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return ""
	}
	return port
}

// localServerURL derives the base URL of the server this configuration
// describes, for the optional liveness probe.
func localServerURL(listenAddr string, tlsEnabled bool) string {
	addr := strings.TrimSpace(listenAddr)
	if addr == "" {
		addr = ":8200"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return ""
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	scheme := "http"
	if tlsEnabled {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

// checkOIDCGroupMaxAge warns when the OIDC group snapshot has no time bound.
//
// Group membership from an IdP is a snapshot refreshed at login, which normally
// self-corrects. The exception is Entra: past roughly 200 groups it stops
// emitting the claim and sends a Microsoft Graph pointer instead, so Janus
// retains the last good snapshot rather than clearing it — correct, since
// clearing would look exactly like a legitimate removal from every group, but
// it means that user's membership stops tracking the IdP indefinitely.
//
// JANUS_OIDC_GROUP_MAX_AGE bounds it. This check exists because the setting is
// off by default (enabling it by default would silently revoke access on
// upgrade) and is therefore easy never to discover.
func checkOIDCGroupMaxAge() doctorCheck {
	const name = "oidc.group-max-age"
	v := strings.TrimSpace(os.Getenv("JANUS_OIDC_GROUP_MAX_AGE"))
	if v == "" {
		return dWarn(name, "no maximum age for OIDC group snapshots",
			"set JANUS_OIDC_GROUP_MAX_AGE (e.g. 30d worth: 720h) so group-derived access expires if a user's membership stops being refreshed — Entra stops sending the group claim past ~200 groups, and the retained snapshot would otherwise never expire",
			"local groups are never affected; only oidc-derived membership expires")
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return dFail(name, "JANUS_OIDC_GROUP_MAX_AGE is not a positive Go duration: "+v,
			"use a value like 720h (30 days); an invalid value leaves the bound OFF")
	}
	return dPass(name, "oidc group snapshots expire after "+d.String())
}
