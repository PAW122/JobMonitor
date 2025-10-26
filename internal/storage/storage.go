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

// StatusStorage handles persistence of status history to disk.
type StatusStorage struct {
	mu      sync.RWMutex
	path    string
	history []models.StatusEntry
}

// NewStatusStorage creates a storage instance and loads existing history if present.
func NewStatusStorage(path string) (*StatusStorage, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("ensure data directory: %w", err)
	}

	s := &StatusStorage{path: path}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Append adds a new status entry and persists it to disk.
func (s *StatusStorage) Append(entry models.StatusEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.history = append(s.history, entry)
	return s.persist()
}

// Latest returns the latest status entry if it exists.
func (s *StatusStorage) Latest() (models.StatusEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.history) == 0 {
		return models.StatusEntry{}, false
	}
	return s.history[len(s.history)-1], true
}

// History returns a copy of the entire history slice.
func (s *StatusStorage) History() []models.StatusEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	copied := make([]models.StatusEntry, len(s.history))
	copy(copied, s.history)
	return copied
}

func (s *StatusStorage) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.history = []models.StatusEntry{}
			return nil
		}
		return fmt.Errorf("read history: %w", err)
	}

	if len(data) == 0 {
		s.history = []models.StatusEntry{}
		return nil
	}

	var entries []models.StatusEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parse history: %w", err)
	}

	s.history = entries
	return nil
}

func (s *StatusStorage) persist() error {
	bytes, err := json.MarshalIndent(s.history, "", "  ")
	if err != nil {
		return fmt.Errorf("encode history: %w", err)
	}

	tmpPath := fmt.Sprintf("%s.%d.tmp", s.path, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, bytes, 0o644); err != nil {
		return fmt.Errorf("write temp history: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace history file: %w", err)
	}
	return nil
}
