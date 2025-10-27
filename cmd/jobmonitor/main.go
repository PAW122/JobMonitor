package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"jobmonitor/internal/cluster"
	"jobmonitor/internal/config"
	"jobmonitor/internal/monitor"
	"jobmonitor/internal/server"
	"jobmonitor/internal/storage"
)

func main() {
	var (
		configPath = flag.String("config", "config.yaml", "path to configuration file (YAML)")
		addr       = flag.String("addr", ":8080", "address for the web server")
	)
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	log.Printf("Loaded %d target(s) from %s", len(cfg.Targets), *configPath)

	historyPath := filepath.Join(cfg.DataDirectory, "status_history.json")
	store, err := storage.NewStatusStorage(historyPath)
	if err != nil {
		log.Fatalf("initialise storage: %v", err)
	}

	mon := monitor.New(time.Duration(cfg.IntervalMinutes)*time.Minute, cfg.Targets, store)
	mon.Start()
	defer mon.Stop()

	node := cluster.Node{
		ID:              cfg.NodeID,
		Name:            cfg.NodeName,
		IntervalMinutes: cfg.IntervalMinutes,
	}
	clusterSvc := cluster.NewService(node, store, cfg, cfg.Targets)
	clusterSvc.Start()
	defer clusterSvc.Stop()

	srv := server.New(*addr, node, store, clusterSvc, cfg.Targets)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown: %v", err)
		}
	}()

	log.Printf("JobMonitor listening on %s (interval %d minutes)", *addr, cfg.IntervalMinutes)
	if err := srv.Run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}
