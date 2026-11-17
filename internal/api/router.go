package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/onatozmenn/insider-one-notification-system/internal/platform/accesslog"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/metrics"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/requestid"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/tracing"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/version"
	"github.com/onatozmenn/insider-one-notification-system/internal/realtime"
)

// ReadinessChecks are the dependencies the API must reach before it can serve
// traffic.
type ReadinessChecks struct {
	Postgres func(context.Context) error
	Redis    func(context.Context) error
}

// Dependencies are the collaborators required by the router.
type Dependencies struct {
	Logger        *slog.Logger
	Readiness     ReadinessChecks
	Notifications NotificationService
	Metrics       *metrics.Metrics
	QueueDepth    QueueDepthProvider
	Realtime      *realtime.Hub
}

// NewRouter builds the HTTP router. Feature routes are added as their
// milestones land.
func NewRouter(deps Dependencies) http.Handler {
	r := chi.NewRouter()
	r.Use(requestid.Middleware)
	r.Use(tracing.Middleware)
	r.Use(accesslog.Middleware(deps.Logger, deps.Metrics))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/", info)
	r.Get("/healthz", healthz)
	r.Get("/readyz", readyz(deps.Readiness, deps.Logger))

	if deps.Metrics != nil {
		r.Handle("/metrics", deps.Metrics.Handler())
	}

	if deps.Notifications != nil {
		registerNotificationRoutes(r, deps.Notifications, deps.QueueDepth, deps.Metrics, deps.Logger)
		registerWebSocketRoute(r, deps.Notifications, deps.Realtime, deps.Logger)
	}

	return r
}

func info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "insider-one-notification-system",
		"status":  "ok",
		"version": version.Build(),
	})
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func readyz(checks ReadinessChecks, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()

		results := map[string]string{
			"postgres": "ok",
			"redis":    "ok",
		}
		status := http.StatusOK

		if checks.Postgres != nil {
			if err := checks.Postgres(ctx); err != nil {
				results["postgres"] = "error"
				status = http.StatusServiceUnavailable
				log.Warn("postgres readiness check failed", "error", err)
			}
		}

		if checks.Redis != nil {
			if err := checks.Redis(ctx); err != nil {
				results["redis"] = "error"
				status = http.StatusServiceUnavailable
				log.Warn("redis readiness check failed", "error", err)
			}
		}

		body := map[string]any{
			"status": "ok",
			"checks": results,
		}
		if status != http.StatusOK {
			body["status"] = "unavailable"
		}

		writeJSON(w, status, body)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
