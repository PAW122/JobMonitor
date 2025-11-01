package models

import "time"

// ConnectivityStatus captures the outcome of a connectivity probe.
type ConnectivityStatus struct {
	Target    string    `json:"target"`
	OK        bool      `json:"ok"`
	LatencyMs int64     `json:"latency_ms"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}
