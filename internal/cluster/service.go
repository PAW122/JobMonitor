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
	defaultHistoryLimit = 200
	requestTimeout      = 10 * time.Second
)

// Service aggregates local storage with peer snapshots.
type Service struct {
	node         Node
	storage      *storage.StatusStorage
	peers        []config.Peer
	refresh      time.Duration
	historyLimit int

	client *http.Client

	mu        sync.RWMutex
	peersData map[string]PeerSnapshot

	ctx    context.Context
	cancel context.CancelFunc
}

// NewService initialises cluster aggregator for a node.
func NewService(node Node, storage *storage.StatusStorage, cfg config.Config) *Service {
	refresh := time.Duration(cfg.PeerRefreshSec) * time.Second
	if refresh < 15*time.Second {
		refresh = 15 * time.Second
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
		node:         node,
		storage:      storage,
		peers:        cfg.Peers,
		refresh:      refresh,
		historyLimit: defaultHistoryLimit,
		client:       &http.Client{Transport: transport, Timeout: requestTimeout},
		peersData:    make(map[string]PeerSnapshot),
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Start launches background synchronisation with peers.
func (s *Service) Start() {
	go s.run()
}

// Stop terminates background synchronisation.
func (s *Service) Stop() {
	s.cancel()
}

func (s *Service) run() {
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
	historyURL := fmt.Sprintf("%s/api/node/history?limit=%d", baseURL, s.historyLimit)
	if err := s.getJSON(historyURL, peer.APIKey, &historyResp); err != nil {
		return fmt.Errorf("history fetch failed: %w", err)
	}

	uptimeResp := NodeUptimeResponse{}
	if err := s.getJSON(baseURL+"/api/node/uptime", peer.APIKey, &uptimeResp); err != nil {
		return fmt.Errorf("uptime fetch failed: %w", err)
	}

	s.mu.Lock()
	s.peersData[peer.ID] = PeerSnapshot{
		Node:      Node{ID: peer.ID, Name: resolveName(peer.Name, statusResp.Node.Name, peer.ID)},
		Status:    statusResp.Status,
		History:   limitHistory(historyResp.History, s.historyLimit),
		Services:  uptimeResp.Services,
		UpdatedAt: time.Now().UTC(),
		Source:    "peer",
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) resolveLocalSnapshot() PeerSnapshot {
	latest, ok := s.storage.Latest()
	history := s.storage.HistoryN(s.historyLimit)
	uptime := metrics.ComputeServiceUptime(history)
	var status *models.StatusEntry
	if ok {
		status = &latest
	}

	return PeerSnapshot{
		Node: Node{
			ID:   s.node.ID,
			Name: s.node.Name,
		},
		Status:    status,
		History:   history,
		Services:  uptime,
		UpdatedAt: time.Now().UTC(),
		Source:    "local",
	}
}

// Snapshot gathers local and remote data for API responses.
func (s *Service) Snapshot() ClusterSnapshot {
	nodes := []PeerSnapshot{s.resolveLocalSnapshot()}

	s.mu.RLock()
	for _, snap := range s.peersData {
		nodes = append(nodes, snap)
	}
	s.mu.RUnlock()

	return ClusterSnapshot{
		GeneratedAt: time.Now().UTC(),
		Nodes:       nodes,
	}
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

func limitHistory(entries []models.StatusEntry, limit int) []models.StatusEntry {
	if limit <= 0 || len(entries) <= limit {
		return entries
	}
	start := len(entries) - limit
	return entries[start:]
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
