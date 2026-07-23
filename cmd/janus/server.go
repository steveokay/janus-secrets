package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	gcpkms "cloud.google.com/go/kms/apiv1"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/spf13/cobra"
	"github.com/steveokay/janus-secrets/internal/api"
	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/backupsched"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
	"github.com/steveokay/janus-secrets/internal/version"
)

func newServerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "server",
		Short: "Run the Janus server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServer(cmd.Context())
		},
	}
}

func runServer(ctx context.Context) error {
	dsn := os.Getenv("JANUS_DATABASE_URL")
	if dsn == "" {
		return errors.New("JANUS_DATABASE_URL is not set")
	}
	logger := buildLogger()

	idle := 30 * time.Minute // production default; 0 disables
	if v := os.Getenv("JANUS_SESSION_IDLE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return fmt.Errorf("invalid JANUS_SESSION_IDLE_TIMEOUT %q: use a Go duration like 30m, or 0 to disable", v)
		}
		idle = d
	}

	rotationTick := 60 * time.Second // production default; 0 disables
	if v := os.Getenv("JANUS_ROTATION_TICK"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return fmt.Errorf("invalid JANUS_ROTATION_TICK %q: use a Go duration like 60s, or 0 to disable", v)
		}
		rotationTick = d
	}

	syncTick := 60 * time.Second // production default; 0 disables
	if v := os.Getenv("JANUS_SYNC_TICK"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return fmt.Errorf("invalid JANUS_SYNC_TICK %q: use a Go duration like 60s, or 0 to disable", v)
		}
		syncTick = d
	}

	dynamicTick := 60 * time.Second // production default; 0 disables
	if v := os.Getenv("JANUS_DYNAMIC_TICK"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return fmt.Errorf("invalid JANUS_DYNAMIC_TICK %q: use a Go duration like 60s, or 0 to disable", v)
		}
		dynamicTick = d
	}

	notifyTick := 30 * time.Second // production default; 0 disables
	if v := os.Getenv("JANUS_NOTIFY_TICK"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return fmt.Errorf("invalid JANUS_NOTIFY_TICK %q: use a Go duration like 30s, or 0 to disable", v)
		}
		notifyTick = d
	}

	// Scheduled encrypted S3 backups (off by default: unset JANUS_BACKUP_TICK or
	// unset bucket disables the engine). See docs/guides/backup-and-restore.md.
	backupSchedule, err := parseBackupSchedule(version.Version)
	if err != nil {
		return err
	}

	httpRead := 30 * time.Second
	if v := os.Getenv("JANUS_HTTP_READ_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return fmt.Errorf("invalid JANUS_HTTP_READ_TIMEOUT %q: use a Go duration like 30s, or 0 to disable", v)
		}
		httpRead = d
	}
	httpIdle := 120 * time.Second
	if v := os.Getenv("JANUS_HTTP_IDLE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return fmt.Errorf("invalid JANUS_HTTP_IDLE_TIMEOUT %q: use a Go duration like 2m, or 0 to disable", v)
		}
		httpIdle = d
	}
	httpWrite := time.Duration(0) // disabled by default: audit export streams
	if v := os.Getenv("JANUS_HTTP_WRITE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return fmt.Errorf("invalid JANUS_HTTP_WRITE_TIMEOUT %q: use a Go duration like 60s, or 0 to disable", v)
		}
		httpWrite = d
	}
	var httpMaxBody int64 = 10 << 20 // 10 MiB default
	if v := os.Getenv("JANUS_HTTP_MAX_BODY_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return fmt.Errorf("invalid JANUS_HTTP_MAX_BODY_BYTES %q: use a non-negative byte count, or 0 to disable", v)
		}
		httpMaxBody = n
	}

	shutdownGrace := 10 * time.Second // graceful-drain window on SIGTERM
	if v := os.Getenv("JANUS_SHUTDOWN_GRACE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("invalid JANUS_SHUTDOWN_GRACE %q: use a positive Go duration like 10s", v)
		}
		shutdownGrace = d
	}

	// pgx connection-pool tuning. Each knob is optional; unset leaves pgx's own
	// default in place. Invalid values fail boot with a clear error.
	pool, err := parsePoolConfig()
	if err != nil {
		return err
	}

	// Progressive account-lockout policy. Absent/unparseable values fall back to
	// the defaults with a logged warning (never fail boot on a bad knob).
	lockout := auth.DefaultLockoutPolicy()
	if v := os.Getenv("JANUS_LOCKOUT_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			lockout.Enabled = b
		} else {
			logger.Warn("invalid JANUS_LOCKOUT_ENABLED; using default", "value", v, "default", lockout.Enabled)
		}
	}
	if v := os.Getenv("JANUS_LOCKOUT_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			lockout.Threshold = n
		} else {
			logger.Warn("invalid JANUS_LOCKOUT_THRESHOLD; using default", "value", v, "default", lockout.Threshold)
		}
	}
	if v := os.Getenv("JANUS_LOCKOUT_BASE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			lockout.Base = d
		} else {
			logger.Warn("invalid JANUS_LOCKOUT_BASE; using default", "value", v, "default", lockout.Base.String())
		}
	}
	if v := os.Getenv("JANUS_LOCKOUT_MAX"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			lockout.Max = d
		} else {
			logger.Warn("invalid JANUS_LOCKOUT_MAX; using default", "value", v, "default", lockout.Max.String())
		}
	}
	if lockout.Max < lockout.Base {
		logger.Warn("JANUS_LOCKOUT_MAX is below JANUS_LOCKOUT_BASE; clamping max up to base",
			"max", lockout.Max.String(), "base", lockout.Base.String())
		lockout.Max = lockout.Base
	}

	// Advisory unused-secret threshold (days). Non-positive/invalid → default 90
	// (applied in the secrets service). Never fail boot on this advisory knob.
	unusedSecretDays := 0 // 0 → service default (DefaultUnusedSecretDays = 90)
	if v := os.Getenv("JANUS_UNUSED_SECRET_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			unusedSecretDays = n
		} else {
			logger.Warn("invalid JANUS_UNUSED_SECRET_DAYS; using default (90)", "value", v)
		}
	}

	// Break-glass TTL ceiling. A grant's requested TTL is clamped to this max.
	// Non-positive/invalid → the api package default (1h). Never fail boot on it.
	breakGlassMaxTTL := time.Duration(0) // 0 → api default (1h)
	if v := os.Getenv("JANUS_BREAKGLASS_MAX_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			breakGlassMaxTTL = d
		} else {
			logger.Warn("invalid JANUS_BREAKGLASS_MAX_TTL; using default (1h)", "value", v)
		}
	}

	// Native TLS listener (optional). Static certs and ACME are mutually
	// exclusive; validation is enforced in the api package at serve time, but we
	// catch the both-halves-of-static-cert and both-modes cases early for a clear
	// startup error.
	tlsCfg, err := buildTLSConfig()
	if err != nil {
		return err
	}

	bc := api.BootConfig{
		DatabaseURL:        dsn,
		ListenAddr:         os.Getenv("JANUS_LISTEN_ADDR"), // "" → :8200 default
		SealType:           os.Getenv("JANUS_SEAL_TYPE"),
		Logger:             logger,
		SessionIdleTimeout: idle,
		RotationTick:       rotationTick,
		SyncTick:           syncTick,
		DynamicTick:        dynamicTick,
		NotificationTick:   notifyTick,
		BackupSchedule:     backupSchedule,
		Version:            version.Version,
		HTTPReadTimeout:    httpRead,
		HTTPWriteTimeout:   httpWrite,
		HTTPIdleTimeout:    httpIdle,
		HTTPMaxBodyBytes:   httpMaxBody,
		Lockout:            lockout,
		MetricsToken:       os.Getenv("JANUS_METRICS_TOKEN"), // "" → /metrics 404s
		BreakGlassMaxTTL:   breakGlassMaxTTL,                 // 0 → default 1h
		UnusedSecretDays:   unusedSecretDays,                 // 0 → default 90 days
		TLS:                tlsCfg,
		Pool:               pool,
		HTTPShutdownGrace:  shutdownGrace,

		NewKMSClient: newKMSClient,
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, st, err := api.Boot(ctx, bc)
	if err != nil {
		return err
	}
	defer st.Close()

	logger.Info("janus server listening",
		"addr", firstNonEmpty(os.Getenv("JANUS_LISTEN_ADDR"), ":8200"),
		"seal_type", firstNonEmpty(os.Getenv("JANUS_SEAL_TYPE"), "(from storage)"),
		"serving", tlsMode(tlsCfg))
	return srv.ListenAndServe(ctx)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// newKMSClient builds the cloud-KMS client for auto-unseal based on the
// resolved seal type. Each provider relies on that cloud's ambient credentials
// (AWS default credential chain, GCP application-default credentials, Azure
// DefaultAzureCredential) and is pinned to a single operator-configured key.
func newKMSClient(ctx context.Context, sealType string) (crypto.KMSClient, error) {
	switch sealType {
	case crypto.SealTypeAWSKMS:
		arn := os.Getenv("JANUS_AWS_KMS_KEY_ARN")
		if arn == "" {
			return nil, errors.New("JANUS_AWS_KMS_KEY_ARN is not set")
		}
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, err
		}
		return crypto.NewAWSKMSClient(kms.NewFromConfig(cfg), arn), nil

	case crypto.SealTypeGCPKMS:
		keyName := os.Getenv("JANUS_GCP_KMS_KEY")
		if keyName == "" {
			return nil, errors.New("JANUS_GCP_KMS_KEY is not set")
		}
		// Uses GCP application-default credentials from the ambient environment.
		gcpClient, err := gcpkms.NewKeyManagementClient(ctx)
		if err != nil {
			return nil, err
		}
		return crypto.NewGCPKMSClient(gcpClient, keyName), nil

	case crypto.SealTypeAzureKV:
		vaultURL := os.Getenv("JANUS_AZURE_KEYVAULT_URL")
		if vaultURL == "" {
			return nil, errors.New("JANUS_AZURE_KEYVAULT_URL is not set")
		}
		keyName := os.Getenv("JANUS_AZURE_KEY_NAME")
		if keyName == "" {
			return nil, errors.New("JANUS_AZURE_KEY_NAME is not set")
		}
		// Optional; empty means "current version".
		keyVersion := os.Getenv("JANUS_AZURE_KEY_VERSION")
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, err
		}
		azClient, err := azkeys.NewClient(vaultURL, cred, nil)
		if err != nil {
			return nil, err
		}
		return crypto.NewAzureKeyVaultClient(azClient, keyName, keyVersion), nil

	default:
		return nil, fmt.Errorf("seal type %q does not use a KMS client", sealType)
	}
}

// buildTLSConfig reads the JANUS_TLS_* environment into an api.TLSConfig and
// validates the static-cert / ACME constraints. All fields empty → plain HTTP
// (TLS delegated to a reverse proxy, the historical default).
//
// Env vars:
//   - JANUS_TLS_CERT / JANUS_TLS_KEY:        paths to a PEM cert/chain + key
//     (both required together → static-cert HTTPS).
//   - JANUS_TLS_ACME_DOMAINS:                comma-separated hostnames → ACME.
//   - JANUS_TLS_ACME_EMAIL:                  optional ACME contact address.
//   - JANUS_TLS_ACME_CACHE:                  cert cache dir (default ./.janus-acme).
//   - JANUS_TLS_REDIRECT_HTTP:               addr (e.g. :80) for an HTTP→HTTPS
//     301 redirect server (static-cert path only; off by default).
func buildTLSConfig() (api.TLSConfig, error) {
	cfg := api.TLSConfig{
		CertFile:     strings.TrimSpace(os.Getenv("JANUS_TLS_CERT")),
		KeyFile:      strings.TrimSpace(os.Getenv("JANUS_TLS_KEY")),
		ACMEEmail:    strings.TrimSpace(os.Getenv("JANUS_TLS_ACME_EMAIL")),
		ACMECache:    strings.TrimSpace(os.Getenv("JANUS_TLS_ACME_CACHE")),
		RedirectHTTP: strings.TrimSpace(os.Getenv("JANUS_TLS_REDIRECT_HTTP")),
	}
	if v := strings.TrimSpace(os.Getenv("JANUS_TLS_ACME_DOMAINS")); v != "" {
		for _, d := range strings.Split(v, ",") {
			if d = strings.TrimSpace(d); d != "" {
				cfg.ACMEDomains = append(cfg.ACMEDomains, d)
			}
		}
	}
	if err := cfg.Validate(); err != nil {
		return api.TLSConfig{}, fmt.Errorf("invalid JANUS_TLS_* configuration: %w", err)
	}
	return cfg, nil
}

// parsePoolConfig reads the JANUS_DB_* environment into a store.PoolConfig.
// Every knob is optional; an unset var leaves pgx's own default in place (the
// zero field is a no-op in store). Invalid values fail boot with a clear error,
// mirroring the JANUS_HTTP_* parsing style.
//
// Env vars:
//   - JANUS_DB_MAX_CONNS:          max pool size (positive int).
//   - JANUS_DB_MIN_CONNS:          idle connections kept warm (non-negative int).
//   - JANUS_DB_MAX_CONN_LIFETIME:  max connection lifetime (Go duration).
//   - JANUS_DB_MAX_CONN_IDLE_TIME: max idle time before close (Go duration).
func parsePoolConfig() (store.PoolConfig, error) {
	var pc store.PoolConfig
	if v := os.Getenv("JANUS_DB_MAX_CONNS"); v != "" {
		// ParseInt with bitSize 32 rejects values that would overflow int32,
		// so the conversion below is always in range.
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n <= 0 {
			return store.PoolConfig{}, fmt.Errorf("invalid JANUS_DB_MAX_CONNS %q: use a positive integer", v)
		}
		pc.MaxConns = int32(n)
	}
	if v := os.Getenv("JANUS_DB_MIN_CONNS"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 0 {
			return store.PoolConfig{}, fmt.Errorf("invalid JANUS_DB_MIN_CONNS %q: use a non-negative integer", v)
		}
		pc.MinConns = int32(n)
	}
	if v := os.Getenv("JANUS_DB_MAX_CONN_LIFETIME"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return store.PoolConfig{}, fmt.Errorf("invalid JANUS_DB_MAX_CONN_LIFETIME %q: use a positive Go duration like 1h", v)
		}
		pc.MaxConnLifetime = d
	}
	if v := os.Getenv("JANUS_DB_MAX_CONN_IDLE_TIME"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return store.PoolConfig{}, fmt.Errorf("invalid JANUS_DB_MAX_CONN_IDLE_TIME %q: use a positive Go duration like 30m", v)
		}
		pc.MaxConnIdleTime = d
	}
	return pc, nil
}

// parseBackupSchedule reads the JANUS_BACKUP_* environment into a
// backupsched.Config. The engine is OFF unless both a bucket and a positive tick
// are set. Static S3 credentials are required when a bucket is configured (a
// backup destination's identity is explicit, never the host's ambient AWS
// identity). Endpoint is optional for S3-compatible stores (MinIO/R2/etc.).
//
// Env vars:
//   - JANUS_BACKUP_TICK:                 scheduler interval (Go duration; 0/unset = disabled).
//   - JANUS_BACKUP_RETENTION:            keep the N most recent objects (non-negative int; 0 = keep all).
//   - JANUS_BACKUP_S3_BUCKET:            destination bucket (required to enable).
//   - JANUS_BACKUP_S3_PREFIX:            key prefix under the bucket (optional).
//   - JANUS_BACKUP_S3_REGION:            S3 region (required to enable).
//   - JANUS_BACKUP_S3_ENDPOINT:          custom endpoint for S3-compatible storage (optional).
//   - JANUS_BACKUP_S3_ACCESS_KEY_ID:     static access key id (required to enable).
//   - JANUS_BACKUP_S3_SECRET_ACCESS_KEY: static secret access key (required to enable).
func parseBackupSchedule(ver string) (backupsched.Config, error) {
	cfg := backupsched.Config{Version: ver}
	cfg.S3.Bucket = strings.TrimSpace(os.Getenv("JANUS_BACKUP_S3_BUCKET"))
	cfg.S3.Prefix = strings.TrimSpace(os.Getenv("JANUS_BACKUP_S3_PREFIX"))
	cfg.S3.Region = strings.TrimSpace(os.Getenv("JANUS_BACKUP_S3_REGION"))
	cfg.S3.Endpoint = strings.TrimSpace(os.Getenv("JANUS_BACKUP_S3_ENDPOINT"))
	cfg.S3.AccessKeyID = strings.TrimSpace(os.Getenv("JANUS_BACKUP_S3_ACCESS_KEY_ID"))
	cfg.S3.SecretAccessKey = os.Getenv("JANUS_BACKUP_S3_SECRET_ACCESS_KEY")

	if v := os.Getenv("JANUS_BACKUP_TICK"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return backupsched.Config{}, fmt.Errorf("invalid JANUS_BACKUP_TICK %q: use a Go duration like 6h, or 0 to disable", v)
		}
		cfg.Tick = d
	}
	if v := os.Getenv("JANUS_BACKUP_RETENTION"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return backupsched.Config{}, fmt.Errorf("invalid JANUS_BACKUP_RETENTION %q: use a non-negative integer (0 = keep all)", v)
		}
		cfg.Retention = n
	}

	// Not enabling? (no bucket and no tick) → return the zero-ish config; disabled.
	if cfg.S3.Bucket == "" && cfg.Tick == 0 {
		return backupsched.Config{Version: ver}, nil
	}
	// Enabling requires a complete, coherent config. Fail boot on a half-set one
	// rather than silently running (or silently not running) a DR backup.
	if cfg.Tick > 0 {
		var missing []string
		if cfg.S3.Bucket == "" {
			missing = append(missing, "JANUS_BACKUP_S3_BUCKET")
		}
		if cfg.S3.Region == "" {
			missing = append(missing, "JANUS_BACKUP_S3_REGION")
		}
		if cfg.S3.AccessKeyID == "" {
			missing = append(missing, "JANUS_BACKUP_S3_ACCESS_KEY_ID")
		}
		if cfg.S3.SecretAccessKey == "" {
			missing = append(missing, "JANUS_BACKUP_S3_SECRET_ACCESS_KEY")
		}
		if len(missing) > 0 {
			return backupsched.Config{}, fmt.Errorf(
				"JANUS_BACKUP_TICK is set (scheduled S3 backups enabled) but required config is missing: %s",
				strings.Join(missing, ", "))
		}
	}
	return cfg, nil
}

// tlsMode returns a short human label for the startup log line.
func tlsMode(cfg api.TLSConfig) string {
	switch {
	case cfg.IsACME():
		return "https (acme)"
	case cfg.IsStaticCerts():
		return "https (static-cert)"
	default:
		return "http"
	}
}

// buildLogger constructs the process slog.Logger from JANUS_LOG_LEVEL
// (debug|info|warn|error, default info) and JANUS_LOG_FORMAT (text|json,
// default text). Invalid values warn (to stderr, via the default handler) and
// fall back to the defaults — a bad knob never fails boot.
func buildLogger() *slog.Logger {
	level := slog.LevelInfo
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_LOG_LEVEL"))); v {
	case "", "info":
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		slog.Default().Warn("invalid JANUS_LOG_LEVEL; using default", "value", v, "default", "info")
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv("JANUS_LOG_FORMAT"))); v {
	case "", "text":
		handler = slog.NewTextHandler(os.Stderr, opts)
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	default:
		slog.Default().Warn("invalid JANUS_LOG_FORMAT; using default", "value", v, "default", "text")
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.New(handler)
}
