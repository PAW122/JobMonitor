package monitor

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"jobmonitor/internal/models"
	"jobmonitor/internal/storage"
)

// Monitor periodically checks targets and persists their status.
type Monitor struct {
	interval time.Duration
	targets  []models.Target
	storage  *storage.StatusStorage
	client   *http.Client

	stopCh chan struct{}
	doneCh chan struct{}
}

// New creates a monitor for the given targets and interval.
func New(interval time.Duration, targets []models.Target, storage *storage.StatusStorage) *Monitor {
	if interval < time.Minute {
		interval = time.Minute
	}

	return &Monitor{
		interval: interval,
		targets:  targets,
		storage:  storage,
		client:   &http.Client{},
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start launches the monitoring loop in a goroutine.
func (m *Monitor) Start() {
	go m.run()
}

// Stop requests graceful loop termination and waits until it is done.
func (m *Monitor) Stop() {
	select {
	case <-m.doneCh:
		return
	default:
	}
	close(m.stopCh)
	<-m.doneCh
}

// RunOnce executes a single round of checks and returns the entry.
func (m *Monitor) RunOnce(ctx context.Context) (models.StatusEntry, error) {
	entry := models.StatusEntry{
		Timestamp: time.Now().UTC(),
		Checks:    make([]models.CheckResult, 0, len(m.targets)),
	}

	for _, t := range m.targets {
		checkCtx := ctx
		var cancel context.CancelFunc
		timeout := time.Duration(t.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		checkCtx, cancel = context.WithTimeout(checkCtx, timeout)
		result := m.checkTarget(checkCtx, t)
		cancel()

		entry.Checks = append(entry.Checks, result)
	}

	if err := m.storage.Append(entry); err != nil {
		return entry, err
	}
	return entry, nil
}

func (m *Monitor) run() {
	defer close(m.doneCh)

	if _, err := m.RunOnce(context.Background()); err != nil {
		log.Printf("initial check failed: %v", err)
	}

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if _, err := m.RunOnce(context.Background()); err != nil {
				log.Printf("monitor tick failed: %v", err)
			}
		case <-m.stopCh:
			return
		}
	}
}

func (m *Monitor) checkTarget(ctx context.Context, target models.Target) models.CheckResult {
	start := time.Now()
	res := models.CheckResult{
		ID:   target.ID,
		Name: target.Name,
		OK:   false,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		msg := err.Error()
		res.Error = &msg
		return res
	}

	response, err := m.client.Do(req)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "request timed out"
		}
		res.Error = &msg
		return res
	}
	defer response.Body.Close()

	latency := float64(time.Since(start).Milliseconds())
	res.LatencyMS = &latency
	res.StatusCode = &response.StatusCode
	res.OK = response.StatusCode >= 200 && response.StatusCode < 400
	if !res.OK {
		msg := http.StatusText(response.StatusCode)
		res.Error = &msg
	}

	return res
}
