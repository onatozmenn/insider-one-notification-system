package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/notifications")
	t.Setenv("PROVIDER_BASE_URL", "https://example.com/hook")
	t.Setenv("APP_ENV", "")
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("PROVIDER_TIMEOUT", "")
	t.Setenv("WORKER_CONCURRENCY", "")
	t.Setenv("RATE_LIMIT_PER_SECOND", "")
	t.Setenv("RATE_LIMIT_BURST", "")
	t.Setenv("MAX_ATTEMPTS", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("WS_ENABLED", "")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "development", cfg.AppEnv)
	assert.Equal(t, ":8080", cfg.HTTPAddr)
	assert.Equal(t, "localhost:6379", cfg.RedisAddr)
	assert.Equal(t, 10*time.Second, cfg.ProviderTimeout)
	assert.Equal(t, 50, cfg.WorkerConcurrency)
	assert.Equal(t, 100, cfg.RateLimitPerSecond)
	assert.Equal(t, 1, cfg.RateLimitBurst)
	assert.Equal(t, 5, cfg.MaxAttempts)
	assert.True(t, cfg.WebSocketEnabled)
}

func TestLoadRejectsInvalidProviderURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/test")
	t.Setenv("PROVIDER_BASE_URL", "not-a-url")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PROVIDER_BASE_URL")
}

func TestLoadRejectsInvalidBurst(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/test")
	t.Setenv("PROVIDER_BASE_URL", "https://example.com/hook")
	t.Setenv("RATE_LIMIT_BURST", "0")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RATE_LIMIT_BURST")
}

func TestLoadRejectsInvalidLogLevel(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/test")
	t.Setenv("PROVIDER_BASE_URL", "https://example.com/hook")
	t.Setenv("LOG_LEVEL", "verbose")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LOG_LEVEL")
}
