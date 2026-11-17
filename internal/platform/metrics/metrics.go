package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	providerBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 15}
	queueBuckets    = []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60, 120}
	rateBuckets     = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60}
)

// Metrics holds the Prometheus collectors for the API and worker.
type Metrics struct {
	registry *prometheus.Registry

	NotificationCreated *prometheus.CounterVec
	NotificationStatus  *prometheus.CounterVec
	ProcessingDuration  *prometheus.HistogramVec
	QueueWait           *prometheus.HistogramVec
	ProviderRequests    *prometheus.CounterVec
	ProviderDuration    *prometheus.HistogramVec
	RateLimitWait       *prometheus.HistogramVec
	QueueDepth          *prometheus.GaugeVec
	HTTPRequests        *prometheus.CounterVec
	HTTPDuration        *prometheus.HistogramVec
	BuildInfo           *prometheus.GaugeVec
}

// New creates a registry and all application metrics.
func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),

		NotificationCreated: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notification_created_total",
			Help: "Notifications accepted by the API.",
		}, []string{"channel", "priority"}),

		NotificationStatus: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notification_status_total",
			Help: "Notification status transitions.",
		}, []string{"channel", "status"}),

		ProcessingDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "notification_processing_duration_seconds",
			Help:    "Time from claim to terminal state.",
			Buckets: providerBuckets,
		}, []string{"channel", "status"}),

		QueueWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "notification_queue_wait_seconds",
			Help:    "Time between creation and claim.",
			Buckets: queueBuckets,
		}, []string{"priority", "channel"}),

		ProviderRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "provider_request_total",
			Help: "Provider calls by result.",
		}, []string{"channel", "result"}),

		ProviderDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "provider_request_duration_seconds",
			Help:    "Provider call latency.",
			Buckets: providerBuckets,
		}, []string{"channel"}),

		RateLimitWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "rate_limit_wait_seconds",
			Help:    "Time spent waiting for a channel token.",
			Buckets: rateBuckets,
		}, []string{"channel"}),

		QueueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "queue_depth",
			Help: "Pending work per asynq queue.",
		}, []string{"queue"}),

		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP requests.",
		}, []string{"method", "route", "status"}),

		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),

		BuildInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "build_info",
			Help: "Build metadata.",
		}, []string{"version", "commit", "date"}),
	}

	m.registry.MustRegister(
		m.NotificationCreated,
		m.NotificationStatus,
		m.ProcessingDuration,
		m.QueueWait,
		m.ProviderRequests,
		m.ProviderDuration,
		m.RateLimitWait,
		m.QueueDepth,
		m.HTTPRequests,
		m.HTTPDuration,
		m.BuildInfo,
	)

	return m
}

// Handler returns the Prometheus HTTP handler.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// SetBuildInfo records the running build.
func (m *Metrics) SetBuildInfo(version, commit, date string) {
	if m == nil {
		return
	}
	m.BuildInfo.WithLabelValues(version, commit, date).Set(1)
}

// SetQueueDepth records the current depth of a queue.
func (m *Metrics) SetQueueDepth(queue string, depth int) {
	if m == nil {
		return
	}
	m.QueueDepth.WithLabelValues(queue).Set(float64(depth))
}

// IncCreated records an accepted API create.
func (m *Metrics) IncCreated(channel, priority string) {
	if m == nil {
		return
	}
	m.NotificationCreated.WithLabelValues(channel, priority).Inc()
}

// IncStatus records a status transition.
func (m *Metrics) IncStatus(channel, status string) {
	if m == nil {
		return
	}
	m.NotificationStatus.WithLabelValues(channel, status).Inc()
}

// ObserveHTTP records one HTTP request.
func (m *Metrics) ObserveHTTP(method, route string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	m.HTTPRequests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.HTTPDuration.WithLabelValues(method, route).Observe(duration.Seconds())
}

// ObserveProvider records one provider call.
func (m *Metrics) ObserveProvider(channel, result string, duration time.Duration) {
	if m == nil {
		return
	}
	m.ProviderRequests.WithLabelValues(channel, result).Inc()
	m.ProviderDuration.WithLabelValues(channel).Observe(duration.Seconds())
}

// ObserveRateLimitWait records time spent waiting for a token.
func (m *Metrics) ObserveRateLimitWait(channel string, duration time.Duration) {
	if m == nil {
		return
	}
	m.RateLimitWait.WithLabelValues(channel).Observe(duration.Seconds())
}

// ObserveQueueWait records time between creation and claim.
func (m *Metrics) ObserveQueueWait(priority, channel string, duration time.Duration) {
	if m == nil {
		return
	}
	m.QueueWait.WithLabelValues(priority, channel).Observe(duration.Seconds())
}

// ObserveProcessing records claim-to-terminal time.
func (m *Metrics) ObserveProcessing(channel, status string, duration time.Duration) {
	if m == nil {
		return
	}
	m.ProcessingDuration.WithLabelValues(channel, status).Observe(duration.Seconds())
}
