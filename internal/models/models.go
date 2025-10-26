package models

import (
	"time"
)

// Target defines a monitored HTTP endpoint.
type Target struct {
	ID             string `yaml:"id" json:"id"`
	Name           string `yaml:"name" json:"name"`
	URL            string `yaml:"url" json:"url"`
	TimeoutSeconds int    `yaml:"timeout_seconds" json:"timeout_seconds"`
}

// CheckResult captures the outcome of a single target check.
type CheckResult struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	OK         bool     `json:"ok"`
	StatusCode *int     `json:"status_code,omitempty"`
	LatencyMS  *float64 `json:"latency_ms,omitempty"`
	Error      *string  `json:"error,omitempty"`
}

// StatusEntry stores the results of all checks at a moment in time.
type StatusEntry struct {
	Timestamp time.Time     `json:"timestamp"`
	Checks    []CheckResult `json:"checks"`
}
