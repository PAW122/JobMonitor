package server

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"math"
	"net/http"
	"sort"
	"time"

	"jobmonitor/internal/models"
	"jobmonitor/internal/storage"
)

//go:embed static/*
var embeddedStatic embed.FS

// Server wraps HTTP serving of API + static assets.
type Server struct {
	httpServer *http.Server
	storage    *storage.StatusStorage
	staticFS   fs.FS
}

// New creates a configured HTTP server for the monitor.
func New(addr string, storage *storage.StatusStorage) *Server {
	staticFS, err := fs.Sub(embeddedStatic, "static")
	if err != nil {
		panic("static assets missing: " + err.Error())
	}

	mux := http.NewServeMux()
	s := &Server{
		httpServer: &http.Server{Addr: addr, Handler: mux},
		storage:    storage,
		staticFS:   staticFS,
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

func (s *Server) handleHistory(w http.ResponseWriter, _ *http.Request) {
	history := s.storage.History()
	writeJSON(w, http.StatusOK, history)
}

func (s *Server) handleUptime(w http.ResponseWriter, _ *http.Request) {
	history := s.storage.History()
	summary := computeUptime(history)
	writeJSON(w, http.StatusOK, summary)
}

func computeUptime(entries []models.StatusEntry) []map[string]any {
	type acc struct {
		name    string
		passing int
		failing int
		latency []float64
	}
	state := make(map[string]*acc)
	for _, entry := range entries {
		for _, check := range entry.Checks {
			target := state[check.ID]
			if target == nil {
				target = &acc{name: check.Name}
				state[check.ID] = target
			}
			if check.OK {
				target.passing++
			} else {
				target.failing++
			}
			if check.LatencyMS != nil {
				target.latency = append(target.latency, *check.LatencyMS)
			}
		}
	}
	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	now := time.Now().UTC()
	results := make([]map[string]any, 0, len(keys))
	for _, id := range keys {
		data := state[id]
		total := data.passing + data.failing
		uptime := 0.0
		if total > 0 {
			uptime = float64(data.passing) / float64(total) * 100
		}
		var avgLatency *float64
		if len(data.latency) > 0 {
			sum := 0.0
			for _, v := range data.latency {
				sum += v
			}
			value := sum / float64(len(data.latency))
			avgLatency = &value
		}

		results = append(results, map[string]any{
			"id":               id,
			"name":             data.name,
			"uptime_percent":   round2(uptime),
			"total_checks":     total,
			"passing":          data.passing,
			"failing":          data.failing,
			"avg_latency_ms":   avgLatency,
			"generated_at_utc": now.Format(time.RFC3339),
		})
	}
	return results
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}
