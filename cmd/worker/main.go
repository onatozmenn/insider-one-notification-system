package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-redis/redis_rate/v10"
	"github.com/hibiken/asynq"

	"github.com/onatozmenn/insider-one-notification-system/internal/delivery"
	"github.com/onatozmenn/insider-one-notification-system/internal/notification"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/config"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/logger"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/postgres"
	redisplatform "github.com/onatozmenn/insider-one-notification-system/internal/platform/redis"
	"github.com/onatozmenn/insider-one-notification-system/internal/provider"
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

	redisClient, err := redisplatform.Connect(ctx, cfg.RedisAddr, cfg.RedisPassword)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer func() {
		_ = redisClient.Close()
	}()

	redisOptions := asynq.RedisClientOpt{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
	}

	queue := delivery.NewQueue(redisOptions, cfg.MaxAttempts-1, log)
	defer func() {
		_ = queue.Close()
	}()

	repository := notification.NewRepository(db)
	limiter := redis_rate.NewLimiter(redisClient)
	providerClient := provider.NewClient(cfg.ProviderBaseURL, cfg.ProviderTimeout)

	worker := delivery.NewWorker(delivery.WorkerOptions{
		Redis:         redisOptions,
		Repository:    repository,
		Provider:      providerClient,
		Limiter:       limiter,
		Logger:        log,
		Concurrency:   cfg.WorkerConcurrency,
		RatePerSecond: cfg.RateLimitPerSecond,
		Burst:         cfg.RateLimitBurst,
	})

	reconciler := delivery.NewReconciler(repository, queue, log)
	reconcilerDone := make(chan struct{})
	go func() {
		reconciler.Run(ctx)
		close(reconcilerDone)
	}()

	log.Info("worker started",
		"concurrency", cfg.WorkerConcurrency,
		"rate_limit_per_second", cfg.RateLimitPerSecond,
		"rate_limit_burst", cfg.RateLimitBurst,
		"max_attempts", cfg.MaxAttempts,
	)

	if err := worker.Start(ctx); err != nil {
		return fmt.Errorf("worker: %w", err)
	}

	<-reconcilerDone
	log.Info("worker stopped")
	return nil
}
