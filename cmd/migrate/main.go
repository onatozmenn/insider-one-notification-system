package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/onatozmenn/insider-one-notification-system/internal/platform/config"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/logger"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logger.New(cfg.LogLevel, "migrate")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer db.Close()

	// The schema and the golang-migrate runner arrive with M1-1.
	log.Info("migrations: no migrations embedded yet; schema lands in M1-1")
	return nil
}
