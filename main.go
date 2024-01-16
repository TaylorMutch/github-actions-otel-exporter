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
	logEndpoint := flag.String("log-endpoint", "http://localhost:3100/loki/api/v1/push", "Loki endpoint")
	flag.Parse()
	gin.SetMode(gin.ReleaseMode)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Setup signals to handle graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()

	// Setup OTEL exporter
	shutdown, err := setupOTelSDK(ctx, serviceName, serviceVersion)
	if err != nil {
		panic(err)
	}
	defer shutdown(ctx)

	// Setup GitHub client
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: *pat},
	)
	tc := oauth2.NewClient(ctx, ts)
	ghclient := github.NewClient(tc)

	// Setup API
	api := NewAPI(ctx, ts, ghclient, *logEndpoint)
	server := &http.Server{
		Addr:    ":8080",
		Handler: api.Router,
	}

	go gracefulShutdown(ctx, server)

	slog.Info("starting server", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("failed to start server", "error", err)
	}
}

//nolint:contextcheck
func gracefulShutdown(ctx context.Context, server *http.Server) {
	// wait for signal
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancel()
	slog.Info("gracefully shutting down the server")
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("unable to shutdown server", "error", err)
	}
}
