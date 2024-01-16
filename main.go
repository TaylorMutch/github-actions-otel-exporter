package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v58/github"
	"golang.org/x/oauth2"
)

const (
	serviceName         = "github-actions-otel-exporter"
	serviceVersion      = "0.0.1"
	httpShutdownTimeout = time.Second * 5
)

func main() {
	pat := flag.String("gha-pat", "", "GitHub Actions Personal Access Token")
	address := flag.String("address", ":8081", "Address to listen on")
	logEndpoint := flag.String("log-endpoint", "http://localhost:3100/loki/api/v1/push", "Loki endpoint")
	otelInsecure := flag.Bool("otel-insecure", false, "Use insecure connection for OTEL")
	flag.Parse()
	gin.SetMode(gin.ReleaseMode)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Setup signals to handle graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()

	// Setup OTEL exporter
	shutdown, err := setupOTelSDK(ctx, serviceName, serviceVersion, *otelInsecure)
	if err != nil {
		slog.Error("failed to setup OTEL SDK", "error", err)
		os.Exit(1)
	}
	defer shutdown(ctx)

	// Setup GitHub client
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: *pat},
	)
	tc := oauth2.NewClient(ctx, ts)
	ghclient := github.NewClient(tc)

	// Setup API
	api, err := NewAPI(ctx, ts, ghclient, *logEndpoint)
	if err != nil {
		slog.Error("failed to setup api", "error", err)
		os.Exit(1)
	}
	// Start the backend tracer queue
	go api.ght.run()

	// Start the server
	server := &http.Server{
		Addr:    *address,
		Handler: api.Router,
	}
	go gracefulShutdown(ctx, server, api)
	slog.Info("starting server", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("failed to start server", "error", err)
	}
}

//nolint:contextcheck
func gracefulShutdown(ctx context.Context, server *http.Server, api *API) {
	// wait for signal
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancel()
	slog.Info("gracefully shutting down the server")
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("unable to shutdown server", "error", err)
	}
	if err := api.Shutdown(); err != nil {
		slog.Error("unable to shutdown api", "error", err)
	}
}
