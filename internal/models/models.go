package models

import (
	"time"
)

// Target defines a monitored systemd service.
type Target struct {
	ID             string `yaml:"id" json:"id"`
	Name           string `yaml:"name" json:"name"`
	Service        string `yaml:"service" json:"service"`
	TimeoutSeconds int    `yaml:"timeout_seconds" json:"timeout_seconds"`
	UseSudo        bool   `yaml:"use_sudo" json:"use_sudo"`
}

// CheckResult captures the outcome of a single target check.
type CheckResult struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	OK    bool    `json:"ok"`
	State string  `json:"state,omitempty"`
	Error *string `json:"error,omitempty"`
}

// StatusEntry stores the results of all checks at a moment in time.
type StatusEntry struct {
	Timestamp time.Time     `json:"timestamp"`
	Checks    []CheckResult `json:"checks"`
}
