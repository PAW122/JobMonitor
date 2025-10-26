package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"jobmonitor/internal/models"
)

// Config represents configuration data for the monitoring service.
type Config struct {
	IntervalMinutes int             `yaml:"interval_minutes"`
	DataDirectory   string          `yaml:"data_directory"`
	NodeID          string          `yaml:"node_id"`
	NodeName        string          `yaml:"node_name"`
	Peers           []Peer          `yaml:"peers"`
	PeerRefreshSec  int             `yaml:"peer_refresh_seconds"`
	Targets         []models.Target `yaml:"targets"`
}

// Peer defines a remote JobMonitor instance to aggregate.
type Peer struct {
	ID      string `yaml:"id"`
	Name    string `yaml:"name"`
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
	Enabled bool   `yaml:"enabled"`
}

// DefaultConfig returns sensible defaults in case no configuration file is provided.
func DefaultConfig() Config {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "jobmonitor-local"
	}

	return Config{
		IntervalMinutes: 5,
		DataDirectory:   filepath.Join(".dist", "data"),
		NodeID:          hostname,
		NodeName:        hostname,
		PeerRefreshSec:  60,
		Targets: []models.Target{
			{
				ID:             "example",
				Name:           "Example Service (ssh)",
				Service:        "ssh",
				TimeoutSeconds: 10,
			},
		},
	}
}

// Load reads configuration from yaml file. Missing files fall back to defaults.
func Load(path string) (Config, error) {
	if path == "" {
		return DefaultConfig(), nil
	}

	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.IntervalMinutes <= 0 {
		cfg.IntervalMinutes = 5
	}
	if cfg.DataDirectory == "" {
		cfg.DataDirectory = DefaultConfig().DataDirectory
	}
	if cfg.NodeID == "" {
		cfg.NodeID = DefaultConfig().NodeID
	}
	if cfg.NodeName == "" {
		cfg.NodeName = cfg.NodeID
	}
	if cfg.PeerRefreshSec <= 0 {
		cfg.PeerRefreshSec = 60
	}
	if len(cfg.Targets) == 0 {
		return Config{}, errors.New("configuration must define at least one target")
	}
	for _, t := range cfg.Targets {
		if t.Service == "" {
			return Config{}, errors.New("each target must define a service name")
		}
	}
	for i, peer := range cfg.Peers {
		if !peer.Enabled {
			continue
		}
		if peer.ID == "" {
			return Config{}, fmt.Errorf("peer %d is missing id", i)
		}
		if peer.BaseURL == "" {
			return Config{}, fmt.Errorf("peer %s base_url is required", peer.ID)
		}
	}
	return cfg, nil
}
