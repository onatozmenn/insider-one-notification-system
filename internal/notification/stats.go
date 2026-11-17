package notification

// Stats is the human-readable delivery summary used by /api/v1/stats.
type Stats struct {
	QueueDepth   map[string]int `json:"queue_depth"`
	Window       string         `json:"window"`
	Counts       map[Status]int `json:"counts"`
	SuccessRate  float64        `json:"success_rate"`
	P95LatencyMS int64          `json:"p95_latency_ms"`
}
