package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/spf13/cobra"
	"github.com/steveokay/janus-secrets/internal/api"
	"github.com/steveokay/janus-secrets/internal/crypto"
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
	logger := slog.Default()

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

	bc := api.BootConfig{
		DatabaseURL:        dsn,
		ListenAddr:         os.Getenv("JANUS_LISTEN_ADDR"), // "" → :8200 default
		SealType:           os.Getenv("JANUS_SEAL_TYPE"),
		Logger:             logger,
		SessionIdleTimeout: idle,
		RotationTick:       rotationTick,
		SyncTick:           syncTick,
		DynamicTick:        dynamicTick,
		Version:            version,
		HTTPReadTimeout:    httpRead,
		HTTPWriteTimeout:   httpWrite,
		HTTPIdleTimeout:    httpIdle,
		HTTPMaxBodyBytes:   httpMaxBody,
		NewKMSClient: func(ctx context.Context) (crypto.KMSClient, error) {
			arn := os.Getenv("JANUS_AWS_KMS_KEY_ARN")
			if arn == "" {
				return nil, errors.New("JANUS_AWS_KMS_KEY_ARN is not set")
			}
			cfg, err := awsconfig.LoadDefaultConfig(ctx)
			if err != nil {
				return nil, err
			}
			return crypto.NewAWSKMSClient(kms.NewFromConfig(cfg), arn), nil
		},
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
		"seal_type", firstNonEmpty(os.Getenv("JANUS_SEAL_TYPE"), "(from storage)"))
	return srv.ListenAndServe(ctx)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
