package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"jobmonitor/internal/models"
)

// ConnectivityStorage persists connectivity samples to disk.
type ConnectivityStorage struct {
	mu      sync.RWMutex
	path    string
	history []models.ConnectivityStatus
}

// NewConnectivityStorage initialises storage and loads existing samples if present.
func NewConnectivityStorage(path string) (*ConnectivityStorage, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("ensure data directory: %w", err)
	}
	store := &ConnectivityStorage{path: path}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

// History returns a copy of the persisted connectivity samples.
func (s *ConnectivityStorage) History() []models.ConnectivityStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.history) == 0 {
		return nil
	}
	out := make([]models.ConnectivityStatus, len(s.history))
	copy(out, s.history)
	return out
}

// Replace overwrites the stored history with the provided entries.
func (s *ConnectivityStorage) Replace(entries []models.ConnectivityStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.history = make([]models.ConnectivityStatus, len(entries))
	copy(s.history, entries)
	return s.persistLocked()
}

func (s *ConnectivityStorage) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.history = nil
			return nil
		}
		return fmt.Errorf("read connectivity history: %w", err)
	}
	if len(data) == 0 {
		s.history = nil
		return nil
	}

	var entries []models.ConnectivityStatus
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parse connectivity history: %w", err)
	}
	s.history = entries
	return nil
}

func (s *ConnectivityStorage) persistLocked() error {
	bytes, err := json.MarshalIndent(s.history, "", "  ")
	if err != nil {
		return fmt.Errorf("encode connectivity history: %w", err)
	}

	tmpPath := fmt.Sprintf("%s.%d.tmp", s.path, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, bytes, 0o644); err != nil {
		return fmt.Errorf("write temp connectivity history: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace connectivity history file: %w", err)
	}
	return nil
}
