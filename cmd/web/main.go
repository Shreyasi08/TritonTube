package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Shreyasi08/TritonTube/internal/api"
	"github.com/Shreyasi08/TritonTube/internal/cluster"
	"github.com/Shreyasi08/TritonTube/internal/metadata"
	"github.com/Shreyasi08/TritonTube/internal/metadata/etcdstore"
	"github.com/Shreyasi08/TritonTube/internal/observability"
	"github.com/Shreyasi08/TritonTube/internal/transcode"
)

func main() {
	address := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	db := flag.String("db", "data/metadata.db", "SQLite database file")
	work := flag.String("work", "data/tmp", "temporary media directory")
	nodeList := flag.String("nodes", "node1=127.0.0.1:9001,node2=127.0.0.1:9002,node3=127.0.0.1:9003", "initial id=address pairs; persisted membership wins after first start")
	vnodes := flag.Int("virtual-nodes", 100, "virtual nodes per physical node; immutable after initialization")
	healthURL := flag.String("health-check", "", "check an HTTP health URL and exit")
	backend := flag.String("metadata", "sqlite", "metadata backend: sqlite or etcd")
	endpoints := flag.String("etcd-endpoints", "http://127.0.0.1:2379,http://127.0.0.1:2381,http://127.0.0.1:2383", "comma-separated external etcd client endpoints")
	prefix := flag.String("etcd-prefix", "/tritontube", "etcd metadata namespace")
	flag.Parse()
	observability.Configure()
	if *healthURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := checkHealth(ctx, *healthURL); err != nil {
			slog.Error("web health check failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(*address, *db, *work, *nodeList, *vnodes, metadataConfig{*backend, *endpoints, *prefix}); err != nil {
		slog.Error("web stopped", "error", err)
		os.Exit(1)
	}
}

type metadataConfig struct{ backend, endpoints, prefix string }

func run(address, db, work, nodeList string, vnodes int, config metadataConfig) error {
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			return fmt.Errorf("required media tool %s: %w", binary, err)
		}
	}
	if config.backend == "sqlite" {
		if err := os.MkdirAll(filepath.Dir(db), 0700); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(work, 0700); err != nil {
		return err
	}
	var repo metadata.Repository
	var err error
	switch config.backend {
	case "sqlite":
		repo, err = metadata.Open(db)
	case "etcd":
		repo, err = etcdstore.Open(strings.Split(config.endpoints, ","), config.prefix)
	default:
		return fmt.Errorf("unknown metadata backend %q", config.backend)
	}
	if err != nil {
		return err
	}
	defer repo.Close()
	startupCtx, startupCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer startupCancel()
	if err = repo.Ping(startupCtx); err != nil {
		return fmt.Errorf("metadata startup check: %w", err)
	}
	var initial []cluster.Node
	for _, pair := range strings.Split(nodeList, ",") {
		id, addr, ok := strings.Cut(pair, "=")
		if !ok || id == "" || addr == "" {
			return fmt.Errorf("invalid node pair %q", pair)
		}
		initial = append(initial, cluster.Node{ID: id, Address: addr})
	}
	// Validate initial settings before persisting them.
	check, err := cluster.New(initial, vnodes, nil)
	if err != nil {
		return err
	}
	check.Close()
	nodes, err := repo.InitializeNodes(startupCtx, initial, vnodes)
	if err != nil {
		return err
	}
	router, err := cluster.New(nodes, vnodes, repo)
	if err != nil {
		return err
	}
	defer router.Close()
	lastMigration, err := repo.Migration(startupCtx)
	if err != nil {
		return err
	}
	router.RestoreMigration(lastMigration)
	if err = repo.RecoverInterrupted(startupCtx); err != nil {
		return err
	}
	app := api.New(repo, router, work, &transcode.FFmpeg{Router: router})
	server := &http.Server{Addr: address, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 11 * time.Minute, WriteTimeout: 11 * time.Minute, IdleTimeout: 60 * time.Second}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopped)
	shutdownDone := make(chan error, 1)
	go func() {
		<-stopped
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := server.Shutdown(ctx)
		if err != nil {
			err = errors.Join(err, server.Close())
		}
		shutdownDone <- err
	}()
	slog.Info("web listening", "address", address, "metadata", config.backend)
	if err = server.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return <-shutdownDone
}

func checkHealth(ctx context.Context, url string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}
