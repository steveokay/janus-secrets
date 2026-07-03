package main

import (
	"context"
	"fmt"
	"os"

	"github.com/steveokay/janus-secrets/internal/store"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Println("janus", version)
			return
		case "migrate":
			if err := runMigrate(); err != nil {
				fmt.Fprintln(os.Stderr, "migrate:", err)
				os.Exit(1)
			}
			fmt.Println("migrations applied")
			return
		}
	}
	fmt.Fprintln(os.Stderr, "janus server not yet implemented; see CLAUDE.md build phases")
	os.Exit(1)
}

// runMigrate applies database migrations using JANUS_DATABASE_URL.
func runMigrate() error {
	dsn := os.Getenv("JANUS_DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("JANUS_DATABASE_URL is not set")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Migrate(ctx)
}
