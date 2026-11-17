package config

import (
	"fmt"
	"net/url"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config is the validated runtime configuration shared by the API, worker, and
// migration commands.
type Config struct {
	AppEnv             string        `env:"APP_ENV" envDefault:"development"`
	HTTPAddr           string        `env:"HTTP_ADDR" envDefault:":8080"`
	WorkerMetricsAddr  string        `env:"WORKER_METRICS_ADDR" envDefault:":9090"`
	DatabaseURL        string        `env:"DATABASE_URL,required"`
	RedisAddr          string        `env:"REDIS_ADDR" envDefault:"localhost:6379"`
	RedisPassword      string        `env:"REDIS_PASSWORD"`
	ProviderBaseURL    string        `env:"PROVIDER_BASE_URL,required"`
	ProviderTimeout    time.Duration `env:"PROVIDER_TIMEOUT" envDefault:"10s"`
	WorkerConcurrency  int           `env:"WORKER_CONCURRENCY" envDefault:"50"`
	RateLimitPerSecond int           `env:"RATE_LIMIT_PER_SECOND" envDefault:"100"`
	RateLimitBurst     int           `env:"RATE_LIMIT_BURST" envDefault:"1"`
	MaxAttempts        int           `env:"MAX_ATTEMPTS" envDefault:"5"`
	LogLevel           string        `env:"LOG_LEVEL" envDefault:"info"`
	OTelEndpoint       string        `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	WebSocketEnabled   bool          `env:"WS_ENABLED" envDefault:"true"`
}

// Load reads the environment, applies defaults, and validates the result.
func Load() (Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse environment: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate rejects settings that would fail at runtime.
func (c Config) Validate() error {
	if c.HTTPAddr == "" {
		return fmt.Errorf("HTTP_ADDR is required")
	}
	if c.RedisAddr == "" {
		return fmt.Errorf("REDIS_ADDR is required")
	}
	if c.WorkerMetricsAddr == "" {
		return fmt.Errorf("WORKER_METRICS_ADDR is required")
	}

	u, err := url.Parse(c.ProviderBaseURL)
	if err != nil {
		return fmt.Errorf("PROVIDER_BASE_URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("PROVIDER_BASE_URL: scheme must be http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("PROVIDER_BASE_URL: host is required")
	}

	if c.ProviderTimeout <= 0 {
		return fmt.Errorf("PROVIDER_TIMEOUT must be positive")
	}
	if c.WorkerConcurrency < 1 {
		return fmt.Errorf("WORKER_CONCURRENCY must be at least 1")
	}
	if c.RateLimitPerSecond < 1 {
		return fmt.Errorf("RATE_LIMIT_PER_SECOND must be at least 1")
	}
	if c.RateLimitBurst < 1 {
		return fmt.Errorf("RATE_LIMIT_BURST must be at least 1")
	}
	if c.MaxAttempts < 1 {
		return fmt.Errorf("MAX_ATTEMPTS must be at least 1")
	}

	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("LOG_LEVEL must be one of debug, info, warn, error")
	}

	return nil
}
