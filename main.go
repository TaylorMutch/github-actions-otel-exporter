package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kelseyhightower/envconfig"
)

const (
	serviceName         = "github-actions-otel-exporter"
	serviceVersion      = "0.0.1"
	httpShutdownTimeout = time.Second * 5
)

type Config struct {
	// GithubPAT is a personal access token with permissions to read workflow runs.
	// This is used for local development or testing. If you are running this in
	// production, you should use a GitHub app using the GithubAppFilename, GithubAppID, and
	// GithubInstallID configuration options.
	GithubPAT string `envconfig:"GHA_PAT" default:""`
	// GithubAppFilename is the path to the private key file for the GitHub App
	GithubAppFilename string `envconfig:"GHA_APP_FILENAME" default:""`
	// GithubAppID is the GitHub App ID
	GithubAppID int64 `envconfig:"GHA_APP_ID" default:"0"`
	// GithubInstallID is the GitHub App Installation ID
	GithubInstallID int64 `envconfig:"GHA_INSTALL_ID" default:"0"`
	// Address is the address to listen on
	Address string `envconfig:"ADDRESS" default:":8081"`
	// LogEndpoint is the endpoint to send logs to
	LogEndpoint string `envconfig:"LOG_ENDPOINT" default:"http://localhost:3100/loki/api/v1/push"`
	// OTELInsecure is whether to use an insecure connection to the OTEL collector
	OTELInsecure bool `envconfig:"OTEL_INSECURE" default:"false"`
}

func main() {
	var conf Config
	err := envconfig.Process("", &conf)
	if err != nil {
		slog.Error("failed to process env vars", "error", err)
		os.Exit(1)
	}

	gin.SetMode(gin.ReleaseMode)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Setup signals to handle graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()

	// Setup OTEL exporter
	shutdown, err := setupOTelSDK(ctx, serviceName, serviceVersion, conf.OTELInsecure)
	if err != nil {
		slog.Error("failed to setup OTEL SDK", "error", err)
		os.Exit(1)
	}
	defer shutdown(ctx)

	// Setup GitHub client
	ghclient, err := getGithubClient(
		conf.GithubPAT,
		conf.GithubAppFilename,
		conf.GithubAppID,
		conf.GithubInstallID,
	)
	if err != nil {
		slog.Error("failed to setup github client", "error", err)
		os.Exit(1)
	}

	// Setup API
	api, err := NewAPI(ctx, ghclient, conf.LogEndpoint)
	if err != nil {
		slog.Error("failed to setup api", "error", err)
		os.Exit(1)
	}
	// Start the backend tracer queue
	go api.ght.run()

	// Start the server
	server := &http.Server{
		Addr:    conf.Address,
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
	slog.Info("server shutdown complete, see you next time")
}
