package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"jobmonitor/internal/config"
	"jobmonitor/internal/metrics"
	"jobmonitor/internal/models"
	"jobmonitor/internal/storage"
)

const (
	requestTimeout = 10 * time.Second
	maxWindow      = 30 * 24 * time.Hour
)

// Service aggregates local storage with peer snapshots.
type Service struct {
	node       Node
	storage    *storage.StatusStorage
	targets    []models.Target
	interval   time.Duration
	peers      []config.Peer
	refresh    time.Duration
	historyCap int

	client *http.Client

	mu        sync.RWMutex
	peersData map[string]PeerSnapshot

	ctx    context.Context
	cancel context.CancelFunc
}

// NewService initialises cluster aggregator for a node.
func NewService(node Node, storage *storage.StatusStorage, cfg config.Config, targets []models.Target) *Service {
	refresh := time.Duration(cfg.PeerRefreshSec) * time.Second
	if refresh < 15*time.Second {
		refresh = 15 * time.Second
	}

	interval := time.Duration(cfg.IntervalMinutes) * time.Minute
	historyCap := 200
	if interval > 0 {
		expected := int(maxWindow/interval) + 5
		if expected > historyCap {
			historyCap = expected
		}
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		node:       node,
		storage:    storage,
		targets:    targets,
		interval:   interval,
		peers:      cfg.Peers,
		refresh:    refresh,
		historyCap: historyCap,
		client:     &http.Client{Transport: transport, Timeout: requestTimeout},
		peersData:  make(map[string]PeerSnapshot),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start launches the background synchronisation loop.
func (s *Service) Start() {
	go s.run()
}

// Stop requests the background synchronisation loop to exit.
func (s *Service) Stop() {
	s.cancel()
}

func (s *Service) run() {
	if len(s.peers) == 0 {
		return
	}

	ticker := time.NewTicker(s.refresh)
	defer ticker.Stop()

	s.fetchAllPeers()

	for {
		select {
		case <-ticker.C:
			s.fetchAllPeers()
		case <-s.ctx.Done():
			return
		}
	}
}

// Snapshot assembles local and remote data for the requested window.
func (s *Service) Snapshot(start, end time.Time) ClusterSnapshot {
	now := time.Now().UTC()
	if end.After(now) {
		end = now
	}

	nodes := []PeerSnapshot{s.localSnapshot(start, end)}

	s.mu.RLock()
	for _, snap := range s.peersData {
		nodes = append(nodes, s.materialisePeerSnapshot(snap, start, end))
	}
	s.mu.RUnlock()

	return ClusterSnapshot{
		GeneratedAt: now,
		Range:       windowKey(start, end),
		RangeStart:  start,
		RangeEnd:    end,
		Nodes:       nodes,
	}
}

func (s *Service) localSnapshot(start, end time.Time) PeerSnapshot {
	history := s.storage.HistorySince(start)
	history = filterHistory(history, start, end)
	latest, ok := s.storage.Latest()
	var status *models.StatusEntry
	if ok {
		status = &latest
	}

	services := metrics.ComputeServiceUptime(history, start, end, s.interval, s.targets)

	return PeerSnapshot{
		Node: Node{
			ID:              s.node.ID,
			Name:            s.node.Name,
			IntervalMinutes: int(s.interval / time.Minute),
		},
		Status:    status,
		History:   history,
		Services:  services,
		Targets:   s.targets,
		UpdatedAt: time.Now().UTC(),
		Source:    "local",
	}
}

func (s *Service) materialisePeerSnapshot(snapshot PeerSnapshot, start, end time.Time) PeerSnapshot {
	history := filterHistory(snapshot.History, start, end)
	interval := time.Duration(snapshot.Node.IntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = s.interval
	}
	endpoint := end
	if endpoint.Before(start) {
		endpoint = start
	}
	services := metrics.ComputeServiceUptime(history, start, endpoint, interval, snapshot.Targets)

	return PeerSnapshot{
		Node:      snapshot.Node,
		Status:    snapshot.Status,
		History:   history,
		Services:  services,
		Targets:   snapshot.Targets,
		UpdatedAt: snapshot.UpdatedAt,
		Error:     snapshot.Error,
		Source:    snapshot.Source,
	}
}

func (s *Service) fetchAllPeers() {
	for _, peer := range s.peers {
		if !peer.Enabled {
			continue
		}
		peer := peer
		if err := s.fetchPeer(peer); err != nil {
			s.mu.Lock()
			s.peersData[peer.ID] = PeerSnapshot{
				Node: Node{
					ID:   peer.ID,
					Name: peer.Name,
				},
				UpdatedAt: time.Now().UTC(),
				Error:     err.Error(),
				Source:    "peer",
			}
			s.mu.Unlock()
		}
	}
}

func (s *Service) fetchPeer(peer config.Peer) error {
	baseURL := strings.TrimSuffix(peer.BaseURL, "/")
	if baseURL == "" {
		return fmt.Errorf("peer %s has empty base_url", peer.ID)
	}

	statusResp := NodeStatusResponse{}
	if err := s.getJSON(baseURL+"/api/node/status", peer.APIKey, &statusResp); err != nil {
		return fmt.Errorf("status fetch failed: %w", err)
	}

	historyResp := NodeHistoryResponse{}
	historyURL := fmt.Sprintf("%s/api/node/history?range=30d&limit=%d", baseURL, s.historyCap)
	if err := s.getJSON(historyURL, peer.APIKey, &historyResp); err != nil {
		return fmt.Errorf("history fetch failed: %w", err)
	}

	targets := deriveTargets(statusResp.Status, historyResp.History)

	s.mu.Lock()
	s.peersData[peer.ID] = PeerSnapshot{
		Node: Node{
			ID:              peer.ID,
			Name:            resolveName(peer.Name, statusResp.Node.Name, peer.ID),
			IntervalMinutes: statusResp.Node.IntervalMinutes,
		},
		Status:    statusResp.Status,
		History:   capHistory(historyResp.History, s.historyCap),
		Targets:   targets,
		UpdatedAt: time.Now().UTC(),
		Source:    "peer",
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) getJSON(url, apiKey string, dest any) error {
	ctx, cancel := context.WithTimeout(s.ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}

func filterHistory(entries []models.StatusEntry, start, end time.Time) []models.StatusEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]models.StatusEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Timestamp.Before(start) || entry.Timestamp.After(end) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func capHistory(entries []models.StatusEntry, limit int) []models.StatusEntry {
	if limit <= 0 || len(entries) <= limit {
		return entries
	}
	start := len(entries) - limit
	copied := make([]models.StatusEntry, limit)
	copy(copied, entries[start:])
	return copied
}

func resolveName(configured, remote, fallback string) string {
	if configured != "" {
		return configured
	}
	if remote != "" {
		return remote
	}
	return fallback
}

func deriveTargets(status *models.StatusEntry, history []models.StatusEntry) []models.Target {
	seen := make(map[string]bool)
	targets := make([]models.Target, 0)

	add := func(id, name string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		targets = append(targets, models.Target{ID: id, Name: name})
	}

	if status != nil {
		for _, check := range status.Checks {
			add(check.ID, check.Name)
		}
	}
	for _, entry := range history {
		for _, check := range entry.Checks {
			add(check.ID, check.Name)
		}
	}
	return targets
}

func windowKey(start, end time.Time) string {
	duration := end.Sub(start)
	switch {
	case duration >= 30*24*time.Hour:
		return "30d"
	case duration >= 24*time.Hour:
		return "24h"
	default:
		return ""
	}
}
