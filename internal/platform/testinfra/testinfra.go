//go:build integration

// Package testinfra starts disposable PostgreSQL and Redis instances for
// integration tests. It uses TEST_DATABASE_URL and TEST_REDIS_ADDR when set
// (CI service containers) and falls back to testcontainers locally.
package testinfra

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepostgres "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/redis"

	platformpostgres "github.com/onatozmenn/insider-one-notification-system/internal/platform/postgres"
	"github.com/onatozmenn/insider-one-notification-system/migrations"
)

// Postgres is a migrated PostgreSQL database for one test package.
type Postgres struct {
	Pool *pgxpool.Pool
	URL  string

	cleanup      func()
	dropDatabase func()
}

// StartPostgres connects to TEST_DATABASE_URL or starts a container, applies
// all migrations, and returns a ready pool.
func StartPostgres(ctx context.Context) (*Postgres, error) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	var cleanup func()

	if databaseURL == "" {
		container, err := postgres.Run(ctx, "postgres:16-alpine",
			postgres.WithDatabase("notifications"),
			postgres.WithUsername("postgres"),
			postgres.WithPassword("postgres"),
			postgres.BasicWaitStrategies(),
		)
		if err != nil {
			return nil, fmt.Errorf("start postgres container: %w", err)
		}
		cleanup = func() {
			_ = testcontainers.TerminateContainer(container)
		}

		databaseURL, err = container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("postgres connection string: %w", err)
		}
	}

	// Each test package gets its own database, so parallel packages cannot
	// truncate each other's rows or race on migrations.
	databaseURL, dropDatabase, err := isolatedDatabase(ctx, databaseURL)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, err
	}

	if err := migrateUp(databaseURL); err != nil {
		dropDatabase()
		if cleanup != nil {
			cleanup()
		}
		return nil, err
	}

	pool, err := platformpostgres.Connect(ctx, databaseURL)
	if err != nil {
		dropDatabase()
		if cleanup != nil {
			cleanup()
		}
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	return &Postgres{
		Pool:         pool,
		URL:          databaseURL,
		cleanup:      cleanup,
		dropDatabase: dropDatabase,
	}, nil
}

// Reset empties the application tables between tests.
func (p *Postgres) Reset(ctx context.Context) error {
	_, err := p.Pool.Exec(ctx,
		`TRUNCATE TABLE notification_events, notifications, idempotency_keys, batches, templates CASCADE`,
	)
	return err
}

// Close releases the pool and stops a container if one was started.
func (p *Postgres) Close() {
	p.Pool.Close()
	if p.dropDatabase != nil {
		p.dropDatabase()
	}
	if p.cleanup != nil {
		p.cleanup()
	}
}

func isolatedDatabase(ctx context.Context, baseURL string) (string, func(), error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", nil, fmt.Errorf("parse test database url: %w", err)
	}

	name := "test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{name}.Sanitize()

	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		return "", nil, fmt.Errorf("connect admin database: %w", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+identifier); err != nil {
		_ = admin.Close(ctx)
		return "", nil, fmt.Errorf("create test database: %w", err)
	}
	_ = admin.Close(ctx)

	parsed.Path = "/" + name
	drop := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		conn, err := pgx.Connect(cleanupCtx, baseURL)
		if err != nil {
			return
		}
		defer func() {
			_ = conn.Close(cleanupCtx)
		}()
		_, _ = conn.Exec(cleanupCtx, "DROP DATABASE IF EXISTS "+identifier+" WITH (FORCE)")
	}

	return parsed.String(), drop, nil
}

// Redis is a disposable Redis instance.
type Redis struct {
	Client *goredis.Client
	Addr   string

	cleanup func()
}

// StartRedis connects to TEST_REDIS_ADDR or TEST_REDIS_URL, or starts a
// container when neither is set.
func StartRedis(ctx context.Context) (*Redis, error) {
	var (
		options *goredis.Options
		cleanup func()
	)

	switch {
	case os.Getenv("TEST_REDIS_ADDR") != "":
		options = &goredis.Options{Addr: os.Getenv("TEST_REDIS_ADDR")}
	case os.Getenv("TEST_REDIS_URL") != "":
		parsed, err := goredis.ParseURL(os.Getenv("TEST_REDIS_URL"))
		if err != nil {
			return nil, fmt.Errorf("parse TEST_REDIS_URL: %w", err)
		}
		options = parsed
	default:
		container, err := redis.Run(ctx, "redis:7-alpine")
		if err != nil {
			return nil, fmt.Errorf("start redis container: %w", err)
		}
		cleanup = func() {
			_ = testcontainers.TerminateContainer(container)
		}

		connectionString, err := container.ConnectionString(ctx)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("redis connection string: %w", err)
		}
		options, err = goredis.ParseURL(connectionString)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("parse redis connection string: %w", err)
		}
	}

	client := goredis.NewClient(options)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		if cleanup != nil {
			cleanup()
		}
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &Redis{Client: client, Addr: options.Addr, cleanup: cleanup}, nil
}

// Close releases the Redis client and stops a container if one was started.
func (r *Redis) Close() {
	_ = r.Client.Close()
	if r.cleanup != nil {
		r.cleanup()
	}
}

func migrateUp(databaseURL string) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open migration database: %w", err)
	}
	defer func() {
		_ = db.Close()
	}()

	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	driver, err := migratepostgres.WithInstance(db, &migratepostgres.Config{})
	if err != nil {
		return fmt.Errorf("create migration driver: %w", err)
	}

	migrator, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer func() {
		_, _ = migrator.Close()
	}()

	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
