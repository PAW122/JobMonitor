package models

import "time"

// TimelinePoint represents a single compact point in a service timeline.
type TimelinePoint struct {
	ClassName string           `json:"className"`
	Label     string           `json:"label"`
	Start     time.Time        `json:"start"`
	End       time.Time        `json:"end"`
	Details   []TimelineDetail `json:"details,omitempty"`
}

// TimelineDetail carries extra information for problematic buckets.
type TimelineDetail struct {
	Timestamp time.Time `json:"timestamp"`
	State     string    `json:"state,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// ServiceTimeline aggregates timeline points for a single service.
type ServiceTimeline struct {
	ServiceID   string          `json:"service_id"`
	ServiceName string          `json:"service_name"`
	Timeline    []TimelinePoint `json:"timeline"`
}
