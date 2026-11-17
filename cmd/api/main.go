package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"

	"github.com/onatozmenn/insider-one-notification-system/internal/api"
	"github.com/onatozmenn/insider-one-notification-system/internal/delivery"
	"github.com/onatozmenn/insider-one-notification-system/internal/notification"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/config"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/logger"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/metrics"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/postgres"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/redis"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/tracing"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/version"
	"github.com/onatozmenn/insider-one-notification-system/internal/realtime"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "api: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logger.New(cfg.LogLevel, "api")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := tracing.Setup(ctx, cfg.OTelEndpoint, "api", version.Version)
	if err != nil {
		log.Warn("tracing setup failed", "error", err)
	} else {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = shutdownTracing(shutdownCtx)
		}()
	}

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

	store := notification.NewRepository(db)

	queue := delivery.NewQueue(asynq.RedisClientOpt{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
	}, cfg.MaxAttempts-1, log)
	defer func() {
		_ = queue.Close()
	}()

	notifications := notification.NewService(store, cfg.MaxAttempts, time.Now).
		WithEnqueuer(queue).
		WithLogger(log)

	appMetrics := metrics.New()
	build := version.Build()
	appMetrics.SetBuildInfo(build.Version, build.Commit, build.Date)

	go delivery.PollQueueDepth(ctx, queue, appMetrics, 5*time.Second)

	var hub *realtime.Hub
	if cfg.WebSocketEnabled {
		hub = realtime.NewHub()
		go hub.Run(ctx, rdb, log)
	}

	router := api.NewRouter(api.Dependencies{
		Logger: log,
		Readiness: api.ReadinessChecks{
			Postgres: db.Ping,
			Redis: func(ctx context.Context) error {
				return rdb.Ping(ctx).Err()
			},
		},
		Notifications: notifications,
		Metrics:       appMetrics,
		QueueDepth:    queue,
		Realtime:      hub,
	})

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Info("api listening", "addr", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown requested")
	case err := <-serverErrors:
		return fmt.Errorf("http server: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	log.Info("api stopped")
	return nil
}
