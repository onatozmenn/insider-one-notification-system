package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRequestTimeoutSkipsWebSocket(t *testing.T) {
	handler := requestTimeout(time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasDeadline := r.Context().Deadline()
		require.False(t, hasDeadline)
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestRequestTimeoutAppliesToAPI(t *testing.T) {
	handler := requestTimeout(time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasDeadline := r.Context().Deadline()
		require.True(t, hasDeadline)
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestHealthEndpoints(t *testing.T) {
	router := NewRouter(Dependencies{
		Logger: testLogger(),
		Readiness: ReadinessChecks{
			Postgres: func(context.Context) error { return nil },
			Redis:    func(context.Context) error { return nil },
		},
	})

	for _, path := range []string{"/", "/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code, path)
		require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
		require.NotEmpty(t, rec.Header().Get("X-Request-ID"))
	}
}

func TestReadyzReturnsServiceUnavailable(t *testing.T) {
	router := NewRouter(Dependencies{
		Logger: testLogger(),
		Readiness: ReadinessChecks{
			Postgres: func(context.Context) error { return errors.New("postgres down") },
			Redis:    func(context.Context) error { return nil },
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), `"status":"unavailable"`)
	require.Contains(t, rec.Body.String(), `"postgres":"error"`)
}
