package monitor

import (
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"jobmonitor/internal/config"
	"jobmonitor/internal/models"
)

// ConnectivitySource exposes connectivity probe results.
type ConnectivitySource interface {
	Latest() (models.ConnectivityStatus, bool)
	History() []models.ConnectivityStatus
	HistorySince(time.Time) []models.ConnectivityStatus
}

// ConnectivityMonitor periodically probes connectivity to a DNS endpoint.
type ConnectivityMonitor struct {
	cfg        config.MonitorDNS
	interval   time.Duration
	maxHistory int

	mu      sync.RWMutex
	latest  *models.ConnectivityStatus
	history []models.ConnectivityStatus

	stopCh chan struct{}
	doneCh chan struct{}
}

// NewConnectivityMonitor configures a new connectivity monitor.
func NewConnectivityMonitor(cfg config.MonitorDNS) *ConnectivityMonitor {
	interval := time.Duration(cfg.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 60 * time.Second
	}

	historyCap := 2048
	if cfg.Enabled {
		slots := int((30 * 24 * time.Hour) / interval)
		if slots < 0 {
			slots = 0
		}
		slots += 128 // small buffer
		if slots > historyCap {
			historyCap = slots
		}
		const maxCap = 100000
		if historyCap > maxCap {
			historyCap = maxCap
		}
	}

	return &ConnectivityMonitor{
		cfg:        cfg,
		interval:   interval,
		maxHistory: historyCap,
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
}

// Start launches the monitoring loop. If disabled, the monitor exits immediately.
func (m *ConnectivityMonitor) Start() {
	if !m.cfg.Enabled {
		close(m.doneCh)
		return
	}
	go m.run()
}

// Stop requests the monitoring loop to terminate.
func (m *ConnectivityMonitor) Stop() {
	select {
	case <-m.doneCh:
		return
	default:
	}
	close(m.stopCh)
	<-m.doneCh
}

// Latest returns the most recent connectivity sample.
func (m *ConnectivityMonitor) Latest() (models.ConnectivityStatus, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.latest == nil {
		return models.ConnectivityStatus{}, false
	}
	return *m.latest, true
}

// History returns up to maxHistory previous connectivity samples.
func (m *ConnectivityMonitor) History() []models.ConnectivityStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.history) == 0 {
		return nil
	}
	out := make([]models.ConnectivityStatus, len(m.history))
	copy(out, m.history)
	return out
}

// HistorySince returns samples whose timestamp is >= cutoff.
func (m *ConnectivityMonitor) HistorySince(cutoff time.Time) []models.ConnectivityStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.history) == 0 {
		return nil
	}

	if cutoff.IsZero() {
		out := make([]models.ConnectivityStatus, len(m.history))
		copy(out, m.history)
		return out
	}

	idx := sort.Search(len(m.history), func(i int) bool {
		return !m.history[i].CheckedAt.Before(cutoff)
	})
	if idx >= len(m.history) {
		return nil
	}
	out := make([]models.ConnectivityStatus, len(m.history)-idx)
	copy(out, m.history[idx:])
	return out
}

func (m *ConnectivityMonitor) run() {
	defer close(m.doneCh)

	interval := m.interval
	timeout := time.Duration(m.cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 4 * time.Second
	}

	m.probe(timeout)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.probe(timeout)
		case <-m.stopCh:
			return
		}
	}
}

func (m *ConnectivityMonitor) probe(timeout time.Duration) {
	target := strings.TrimSpace(m.cfg.Target)
	if target == "" {
		target = "1.1.1.1"
	}

	address := target
	if !strings.Contains(address, ":") {
		address = net.JoinHostPort(address, "53")
	}

	started := time.Now()
	conn, err := net.DialTimeout("tcp", address, timeout)

	status := models.ConnectivityStatus{
		Target:    target,
		CheckedAt: time.Now().UTC(),
	}

	if err != nil {
		status.Error = err.Error()
	} else {
		status.OK = true
		status.LatencyMs = int64(time.Since(started) / time.Millisecond)
		_ = conn.Close()
	}

	m.mu.Lock()
	m.latest = &status
	m.history = append(m.history, status)
	if len(m.history) > m.maxHistory {
		m.history = m.history[len(m.history)-m.maxHistory:]
	}
	m.mu.Unlock()
}
