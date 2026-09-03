package metrics

import (
	"time"
)

// MetricsQueryOptions defines query parameters for metrics
type MetricsQueryOptions struct {
	StartDate time.Time
	EndDate   time.Time
	Repo      string
	Profile   string
	Plugin    string
	Days      int // Override for "last N days"
}

// MetricsSummary contains aggregated metrics for dashboard
type MetricsSummary struct {
	// Time range
	StartDate time.Time `json:"start_date"`
	EndDate   time.Time `json:"end_date"`

	// Run statistics
	TotalRuns     int     `json:"total_runs"`
	SuccessRate   float64 `json:"success_rate"`
	AvgDurationMs int64   `json:"avg_duration_ms"`

	// Action statistics
	TotalActions      int             `json:"total_actions"`
	ActionsByStatus   map[string]int  `json:"actions_by_status"`
	TopFailingPlugins []PluginFailure `json:"top_failing_plugins"`

	// Retry statistics
	RetryRate    float64 `json:"retry_rate"`
	TotalRetries int     `json:"total_retries"`

	// Plugin statistics
	PluginRunCounts map[string]int     `json:"plugin_run_counts"`
	PluginFailRates map[string]float64 `json:"plugin_fail_rates"`

	// Coverage (if available)
	AvgCoverage float64 `json:"avg_coverage,omitempty"`
}

// PluginFailure represents a plugin's failure statistics
type PluginFailure struct {
	Plugin    string  `json:"plugin"`
	RunCount  int     `json:"run_count"`
	FailCount int     `json:"fail_count"`
	FailRate  float64 `json:"fail_rate"`
}
