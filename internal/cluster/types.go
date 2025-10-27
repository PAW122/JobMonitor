package cluster

import (
	"time"

	"jobmonitor/internal/config"
	"jobmonitor/internal/metrics"
	"jobmonitor/internal/models"
)

// Node describes a JobMonitor instance.
type Node struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	IntervalMinutes int    `json:"interval_minutes"`
}

// Peer wraps configuration for a remote node.
type Peer struct {
	config.Peer
}

// NodeStatusResponse describes the payload exposed by /api/node/status.
type NodeStatusResponse struct {
	Node        Node                `json:"node"`
	Status      *models.StatusEntry `json:"status,omitempty"`
	GeneratedAt time.Time           `json:"generated_at"`
}

// NodeHistoryResponse describes history payload from /api/node/history.
type NodeHistoryResponse struct {
	Node        Node                 `json:"node"`
	History     []models.StatusEntry `json:"history"`
	GeneratedAt time.Time            `json:"generated_at"`
	Range       string               `json:"range"`
	RangeStart  time.Time            `json:"range_start"`
	RangeEnd    time.Time            `json:"range_end"`
}

// NodeUptimeResponse describes uptime payload from /api/node/uptime.
type NodeUptimeResponse struct {
	Node        Node                    `json:"node"`
	Services    []metrics.ServiceUptime `json:"services"`
	GeneratedAt time.Time               `json:"generated_at"`
	Range       string                  `json:"range"`
	RangeStart  time.Time               `json:"range_start"`
	RangeEnd    time.Time               `json:"range_end"`
}

// PeerSnapshot stores last known data for a peer.
type PeerSnapshot struct {
	Node      Node                    `json:"node"`
	Status    *models.StatusEntry     `json:"status,omitempty"`
	History   []models.StatusEntry    `json:"history"`
	Services  []metrics.ServiceUptime `json:"services"`
	Targets   []models.Target         `json:"targets,omitempty"`
	UpdatedAt time.Time               `json:"updated_at"`
	Error     string                  `json:"error,omitempty"`
	Source    string                  `json:"source"`
}

// ClusterSnapshot is returned by /api/cluster.
type ClusterSnapshot struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Range       string         `json:"range"`
	RangeStart  time.Time      `json:"range_start"`
	RangeEnd    time.Time      `json:"range_end"`
	Nodes       []PeerSnapshot `json:"nodes"`
}
