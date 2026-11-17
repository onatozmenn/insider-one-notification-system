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
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/redis"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "worker: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logger.New(cfg.LogLevel, "worker")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer db.Close()

	rdb, err := redis.Connect(ctx, cfg.RedisAddr, cfg.RedisPassword)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	log.Info("worker started",
		"concurrency", cfg.WorkerConcurrency,
		"rate_limit_per_second", cfg.RateLimitPerSecond,
		"rate_limit_burst", cfg.RateLimitBurst,
		"max_attempts", cfg.MaxAttempts,
	)

	<-ctx.Done()
	log.Info("worker stopped")
	return nil
}
