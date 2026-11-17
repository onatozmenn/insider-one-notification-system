package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// Message is the provider-facing payload for one notification.
type Message struct {
	ID            string
	To            string
	Channel       string
	Content       string
	CorrelationID string
}

// Result is the provider response for an accepted message.
type Result struct {
	MessageID string `json:"messageId"`
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

// Error is a classified provider failure.
type Error struct {
	Transient  bool
	RetryAfter time.Duration
	StatusCode int
	Message    string
	Err        error
}

func (e *Error) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("provider error: status=%d transient=%t: %s", e.StatusCode, e.Transient, e.Message)
	}
	if e.Err != nil {
		return fmt.Sprintf("provider error: %s: %v", e.Message, e.Err)
	}
	return "provider error: " + e.Message
}

// Unwrap exposes the underlying network error.
func (e *Error) Unwrap() error {
	return e.Err
}

// Client posts messages to the simulated provider.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a provider client.
func NewClient(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Send posts one message and classifies the response.
func (c *Client) Send(ctx context.Context, message Message) (Result, error) {
	payload, err := json.Marshal(map[string]string{
		"to":      message.To,
		"channel": message.Channel,
		"content": message.Content,
	})
	if err != nil {
		return Result{}, &Error{Message: "marshal provider payload", Err: err}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(payload))
	if err != nil {
		return Result{}, &Error{Message: "build provider request", Err: err}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", message.CorrelationID)
	request.Header.Set("Idempotency-Key", message.ID)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(request.Header))

	response, err := c.httpClient.Do(request)
	if err != nil {
		return Result{}, &Error{Transient: true, Message: "provider request failed", Err: err}
	}
	defer func() {
		_ = response.Body.Close()
	}()

	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		return Result{}, &Error{Transient: true, Message: "read provider response", Err: readErr}
	}

	if response.StatusCode == http.StatusAccepted {
		var result Result
		if err := json.Unmarshal(body, &result); err != nil {
			// The provider accepted the message; a malformed body is not a
			// delivery failure.
			return Result{}, nil
		}
		return result, nil
	}

	transient := response.StatusCode == http.StatusRequestTimeout ||
		response.StatusCode == http.StatusTooEarly ||
		response.StatusCode == http.StatusTooManyRequests ||
		response.StatusCode >= 500

	return Result{}, &Error{
		Transient:  transient,
		RetryAfter: parseRetryAfter(response.Header.Get("Retry-After")),
		StatusCode: response.StatusCode,
		Message:    fmt.Sprintf("provider returned %d", response.StatusCode),
	}
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}

	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}

	return 0
}
