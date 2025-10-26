package server

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"
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
	clusterService *cluster.Service
	historyLimit   int
}

// New creates a configured HTTP server for the monitor.
func New(addr string, node cluster.Node, storage *storage.StatusStorage, clusterService *cluster.Service) *Server {
	staticFS, err := fs.Sub(embeddedStatic, "static")
	if err != nil {
		panic("static assets missing: " + err.Error())
	}

	mux := http.NewServeMux()
	s := &Server{
		httpServer:     &http.Server{Addr: addr, Handler: mux},
		storage:        storage,
		staticFS:       staticFS,
		node:           node,
		clusterService: clusterService,
		historyLimit:   200,
	}
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
	limit := parseLimit(r, s.historyLimit)
	history := s.storage.HistoryN(limit)
	writeJSON(w, http.StatusOK, history)
}

func (s *Server) handleUptime(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r, s.historyLimit)
	history := s.storage.HistoryN(limit)
	summary := metrics.ComputeServiceUptime(history)
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleNodeStatus(w http.ResponseWriter, _ *http.Request) {
	entry, ok := s.storage.Latest()
	resp := cluster.NodeStatusResponse{
		Node:        s.node,
		GeneratedAt: time.Now().UTC(),
	}
	if ok {
		resp.Status = &entry
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleNodeHistory(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r, s.historyLimit)
	history := s.storage.HistoryN(limit)
	resp := cluster.NodeHistoryResponse{
		Node:        s.node,
		History:     history,
		GeneratedAt: time.Now().UTC(),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleNodeUptime(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r, s.historyLimit)
	history := s.storage.HistoryN(limit)
	resp := cluster.NodeUptimeResponse{
		Node:        s.node,
		Services:    metrics.ComputeServiceUptime(history),
		GeneratedAt: time.Now().UTC(),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCluster(w http.ResponseWriter, _ *http.Request) {
	if s.clusterService == nil {
		writeJSON(w, http.StatusOK, cluster.ClusterSnapshot{
			GeneratedAt: time.Now().UTC(),
			Nodes:       []cluster.PeerSnapshot{s.localPeerSnapshot()},
		})
		return
	}
	writeJSON(w, http.StatusOK, s.clusterService.Snapshot())
}

func (s *Server) localPeerSnapshot() cluster.PeerSnapshot {
	latest, ok := s.storage.Latest()
	history := s.storage.HistoryN(s.historyLimit)
	var status *models.StatusEntry
	if ok {
		status = &latest
	}
	return cluster.PeerSnapshot{
		Node:      s.node,
		Status:    status,
		History:   history,
		Services:  metrics.ComputeServiceUptime(history),
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}
