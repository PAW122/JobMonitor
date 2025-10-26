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
	Targets         []models.Target `yaml:"targets"`
}

// DefaultConfig returns sensible defaults in case no configuration file is provided.
func DefaultConfig() Config {
	return Config{
		IntervalMinutes: 5,
		DataDirectory:   filepath.Join(".dist", "data"),
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
	if len(cfg.Targets) == 0 {
		return Config{}, errors.New("configuration must define at least one target")
	}
	for _, t := range cfg.Targets {
		if t.Service == "" {
			return Config{}, errors.New("each target must define a service name")
		}
	}
	return cfg, nil
}
