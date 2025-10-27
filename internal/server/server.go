package server

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"jobmonitor/internal/cluster"
	"jobmonitor/internal/metrics"
	"jobmonitor/internal/models"
	"jobmonitor/internal/storage"
)

//go:embed static/*
var embeddedStatic embed.FS

// Server wraps HTTP serving of API + static assets.
type Server struct {
	httpServer     *http.Server
	storage        *storage.StatusStorage
	staticFS       fs.FS
	node           cluster.Node
	interval       time.Duration
	targets        []models.Target
	clusterService *cluster.Service
	historyLimit   int
}

// New creates a configured HTTP server for the monitor.
func New(addr string, node cluster.Node, storage *storage.StatusStorage, clusterService *cluster.Service, targets []models.Target) *Server {
	staticFS, err := fs.Sub(embeddedStatic, "static")
	if err != nil {
		panic("static assets missing: " + err.Error())
	}

	interval := time.Duration(node.IntervalMinutes) * time.Minute
	historyLimit := 200
	if interval > 0 {
		expected := int((30 * 24 * time.Hour) / interval)
		if expected+5 > historyLimit {
			historyLimit = expected + 5
		}
	}

	mux := http.NewServeMux()
	s := &Server{
		httpServer:     &http.Server{Addr: addr, Handler: mux},
		storage:        storage,
		staticFS:       staticFS,
		node:           node,
		interval:       interval,
		targets:        targets,
		clusterService: clusterService,
		historyLimit:   historyLimit,
	}
	s.node.IntervalMinutes = int(interval / time.Minute)
	s.registerRoutes(mux)
	return s
}

// Run blocks and serves HTTP traffic.
func (s *Server) Run() error {
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts the server down.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	fileServer := http.FileServer(http.FS(s.staticFS))

	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(s.staticFS, "index.html")
		if err != nil {
			http.Error(w, "index missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	}))
	mux.Handle("/static/", http.StripPrefix("/static/", fileServer))
	mux.Handle("/favicon.ico", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		icon, err := fs.ReadFile(s.staticFS, "favicon.ico")
		if err != nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "image/x-icon")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(icon)
	}))
	mux.HandleFunc("/api/status", s.handleLatest)
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/uptime", s.handleUptime)
	mux.HandleFunc("/api/node/status", s.handleNodeStatus)
	mux.HandleFunc("/api/node/history", s.handleNodeHistory)
	mux.HandleFunc("/api/node/uptime", s.handleNodeUptime)
	mux.HandleFunc("/api/cluster", s.handleCluster)
}

func (s *Server) handleLatest(w http.ResponseWriter, _ *http.Request) {
	entry, ok := s.storage.Latest()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"timestamp": nil,
			"checks":    []models.CheckResult{},
		})
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	window := parseWindow(r)
	history := s.storage.HistorySince(window.start)
	history = filterHistory(history, window.start, window.end)
	if limit := parseLimit(r, s.historyLimit); limit > 0 && len(history) > limit {
		history = history[len(history)-limit:]
	}
	writeJSON(w, http.StatusOK, history)
}

func (s *Server) handleUptime(w http.ResponseWriter, r *http.Request) {
	window := parseWindow(r)
	history := s.storage.HistorySince(window.start)
	history = filterHistory(history, window.start, window.end)
	summary := metrics.ComputeServiceUptime(history, window.start, window.end, s.interval, s.targets)
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleNodeStatus(w http.ResponseWriter, _ *http.Request) {
	entry, ok := s.storage.Latest()
	resp := cluster.NodeStatusResponse{
		Node:        s.node,
		GeneratedAt: time.Now().UTC(),
	}
	resp.Node.IntervalMinutes = int(s.interval / time.Minute)
	if ok {
		resp.Status = &entry
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleNodeHistory(w http.ResponseWriter, r *http.Request) {
	window := parseWindow(r)
	history := s.storage.HistorySince(window.start)
	history = filterHistory(history, window.start, window.end)
	if limit := parseLimit(r, s.historyLimit); limit > 0 && len(history) > limit {
		history = history[len(history)-limit:]
	}
	resp := cluster.NodeHistoryResponse{
		Node:        s.node,
		History:     history,
		GeneratedAt: time.Now().UTC(),
		Range:       window.key,
		RangeStart:  window.start,
		RangeEnd:    window.end,
	}
	resp.Node.IntervalMinutes = int(s.interval / time.Minute)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleNodeUptime(w http.ResponseWriter, r *http.Request) {
	window := parseWindow(r)
	history := s.storage.HistorySince(window.start)
	history = filterHistory(history, window.start, window.end)
	resp := cluster.NodeUptimeResponse{
		Node:        s.node,
		Services:    metrics.ComputeServiceUptime(history, window.start, window.end, s.interval, s.targets),
		GeneratedAt: time.Now().UTC(),
		Range:       window.key,
		RangeStart:  window.start,
		RangeEnd:    window.end,
	}
	resp.Node.IntervalMinutes = int(s.interval / time.Minute)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCluster(w http.ResponseWriter, r *http.Request) {
	window := parseWindow(r)
	if s.clusterService == nil {
		local := s.localPeerSnapshot(window.start, window.end)
		writeJSON(w, http.StatusOK, cluster.ClusterSnapshot{
			GeneratedAt: time.Now().UTC(),
			Range:       window.key,
			RangeStart:  window.start,
			RangeEnd:    window.end,
			Nodes:       []cluster.PeerSnapshot{local},
		})
		return
	}
	writeJSON(w, http.StatusOK, s.clusterService.Snapshot(window.start, window.end))
}

func (s *Server) localPeerSnapshot(start, end time.Time) cluster.PeerSnapshot {
	history := s.storage.HistorySince(start)
	history = filterHistory(history, start, end)
	latest, ok := s.storage.Latest()
	var status *models.StatusEntry
	if ok {
		status = &latest
	}
	services := metrics.ComputeServiceUptime(history, start, end, s.interval, s.targets)
	return cluster.PeerSnapshot{
		Node:      s.node,
		Status:    status,
		History:   history,
		Services:  services,
		Targets:   s.targets,
		UpdatedAt: time.Now().UTC(),
		Source:    "local",
	}
}

func parseLimit(r *http.Request, fallback int) int {
	if fallback <= 0 {
		return fallback
	}
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	if value > fallback {
		return fallback
	}
	return value
}

type window struct {
	key      string
	start    time.Time
	end      time.Time
	duration time.Duration
}

func parseWindow(r *http.Request) window {
	raw := strings.ToLower(r.URL.Query().Get("range"))
	now := time.Now().UTC()
	duration := 24 * time.Hour
	key := "24h"
	if raw == "30d" || raw == "30day" || raw == "30days" {
		duration = 30 * 24 * time.Hour
		key = "30d"
	}
	start := now.Add(-duration)
	return window{
		key:      key,
		start:    start,
		end:      now,
		duration: duration,
	}
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}
