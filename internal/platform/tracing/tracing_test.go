package tracing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupDisabled(t *testing.T) {
	shutdown, err := Setup(context.Background(), "", "test-service", "dev")
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	require.NoError(t, shutdown(context.Background()))
}

func TestEndpointURLAddsDefaultPath(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{
			name:     "host and port",
			endpoint: "http://jaeger:4318",
			want:     "http://jaeger:4318/v1/traces",
		},
		{
			name:     "trailing slash",
			endpoint: "http://jaeger:4318/",
			want:     "http://jaeger:4318/v1/traces",
		},
		{
			name:     "explicit path",
			endpoint: "https://collector.example.com/otlp/v1/traces",
			want:     "https://collector.example.com/otlp/v1/traces",
		},
		{
			name:     "uppercase scheme",
			endpoint: "HTTP://jaeger:4318",
			want:     "http://jaeger:4318/v1/traces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := endpointURL(tt.endpoint)
			require.NoError(t, err)
			assert.Equal(t, tt.want, parsed.String())
		})
	}
}
