package main

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"
	"github.com/steveokay/janus-secrets/internal/store"
)

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations (JANUS_DATABASE_URL)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dsn := os.Getenv("JANUS_DATABASE_URL")
			if dsn == "" {
				return errors.New("JANUS_DATABASE_URL is not set")
			}
			ctx := context.Background()
			s, err := store.Open(ctx, dsn)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.Migrate(ctx); err != nil {
				return err
			}
			cmd.Println("migrations applied")
			return nil
		},
	}
}
