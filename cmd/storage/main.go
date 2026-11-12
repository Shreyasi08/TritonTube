package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Shreyasi08/TritonTube/internal/storage"
	pb "github.com/Shreyasi08/TritonTube/proto/storage/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	address := flag.String("listen", ":9001", "gRPC listen address")
	dir := flag.String("data", "data/node1", "storage directory")
	check := flag.String("health-check", "", "check a gRPC address and exit")
	flag.Parse()
	if *check != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		client, err := storage.Dial(*check)
		if err == nil {
			defer client.Close()
			err = client.Health(ctx)
		}
		if err != nil {
			slog.Error("health check failed", "error", err)
			os.Exit(1)
		}
		return
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(*address, *dir); err != nil {
		slog.Error("storage stopped", "error", err)
		os.Exit(1)
	}
}
func run(address, dir string) error {
	store, err := storage.NewServer(dir)
	if err != nil {
		return err
	}
	defer store.Close()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := grpc.NewServer(grpc.MaxRecvMsgSize(storage.ChunkSize + 1024))
	pb.RegisterStorageServer(server, store)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-signals
		healthServer.Shutdown()
		timer := time.AfterFunc(10*time.Second, server.Stop)
		server.GracefulStop()
		timer.Stop()
	}()
	slog.Info("storage listening", "address", listener.Addr().String(), "data", dir)
	err = server.Serve(listener)
	if err == nil {
		<-shutdownDone
	}
	return err
}
