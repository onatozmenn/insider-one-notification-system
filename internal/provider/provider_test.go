package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientSendAccepted(t *testing.T) {
	var (
		receivedBody   map[string]string
		requestID      string
		idempotencyKey string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		requestID = r.Header.Get("X-Request-ID")
		idempotencyKey = r.Header.Get("Idempotency-Key")

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedBody))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"messageId":"msg-1","status":"accepted","timestamp":"2026-11-17T19:00:00Z"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	result, err := client.Send(context.Background(), Message{
		ID:            "notification-1",
		To:            "+15555550100",
		Channel:       "sms",
		Content:       "hello",
		CorrelationID: "req-1",
	})

	require.NoError(t, err)
	assert.Equal(t, "msg-1", result.MessageID)
	assert.Equal(t, "accepted", result.Status)
	assert.Equal(t, "req-1", requestID)
	assert.Equal(t, "notification-1", idempotencyKey)
	assert.Equal(t, "+15555550100", receivedBody["to"])
	assert.Equal(t, "sms", receivedBody["channel"])
	assert.Equal(t, "hello", receivedBody["content"])
}

func TestClientSendPermanentError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	_, err := client.Send(context.Background(), Message{ID: "1", To: "+15555550100", Channel: "sms", Content: "hello"})

	require.Error(t, err)
	var providerErr *Error
	require.ErrorAs(t, err, &providerErr)
	assert.False(t, providerErr.Transient)
	assert.Equal(t, http.StatusBadRequest, providerErr.StatusCode)
}

func TestClientSendTransientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	_, err := client.Send(context.Background(), Message{ID: "1", To: "+15555550100", Channel: "sms", Content: "hello"})

	require.Error(t, err)
	var providerErr *Error
	require.ErrorAs(t, err, &providerErr)
	assert.True(t, providerErr.Transient)
	assert.Equal(t, http.StatusInternalServerError, providerErr.StatusCode)
}

func TestClientSendRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	_, err := client.Send(context.Background(), Message{ID: "1", To: "+15555550100", Channel: "sms", Content: "hello"})

	require.Error(t, err)
	var providerErr *Error
	require.ErrorAs(t, err, &providerErr)
	assert.True(t, providerErr.Transient)
	assert.Equal(t, 2*time.Second, providerErr.RetryAfter)
}

func TestClientSendTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := NewClient(server.URL, 5*time.Millisecond)
	_, err := client.Send(context.Background(), Message{ID: "1", To: "+15555550100", Channel: "sms", Content: "hello"})

	require.Error(t, err)
	var providerErr *Error
	require.ErrorAs(t, err, &providerErr)
	assert.True(t, providerErr.Transient)
}

func TestClientAcceptsMalformedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	result, err := client.Send(context.Background(), Message{ID: "1", To: "+15555550100", Channel: "sms", Content: "hello"})

	require.NoError(t, err)
	assert.Empty(t, result.MessageID)
}

func TestParseRetryAfterHTTPDate(t *testing.T) {
	when := time.Now().UTC().Add(3 * time.Second).Format(http.TimeFormat)
	delay := parseRetryAfter(when)
	assert.Greater(t, delay, time.Duration(0))
	assert.LessOrEqual(t, delay, 3*time.Second)
}
