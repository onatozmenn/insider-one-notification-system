package accesslog

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/onatozmenn/insider-one-notification-system/internal/platform/metrics"
	"github.com/onatozmenn/insider-one-notification-system/internal/platform/requestid"
)

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

// Middleware logs one structured line and records metrics per HTTP request.
func Middleware(log *slog.Logger, appMetrics *metrics.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w}

			next.ServeHTTP(rw, r)

			if rw.status == 0 {
				rw.status = http.StatusOK
			}
			duration := time.Since(start)

			route := chi.RouteContext(r.Context()).RoutePattern()
			if route == "" {
				route = "unmatched"
			}
			appMetrics.ObserveHTTP(r.Method, route, rw.status, duration)

			log.Info("http request",
				"request_id", requestid.FromContext(r.Context()),
				"method", r.Method,
				"route", route,
				"path", r.URL.Path,
				"status", rw.status,
				"duration_ms", duration.Milliseconds(),
			)
		})
	}
}
