package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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

	bc := api.BootConfig{
		DatabaseURL: dsn,
		ListenAddr:  os.Getenv("JANUS_LISTEN_ADDR"), // "" → :8200 default
		SealType:    os.Getenv("JANUS_SEAL_TYPE"),
		Logger:      logger,
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
