package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v58/github"
	"github.com/grafana/loki-client-go/loki"
	"github.com/grafana/loki-client-go/pkg/urlutil"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	sloggin "github.com/samber/slog-gin"
)

// API is the main API struct
type API struct {
	ctx    context.Context
	Router *gin.Engine
	ght    *GitHubTracer
}

// NewAPI creates a new API instance
func NewAPI(ctx context.Context, ghclient *github.Client, logEndpoint string) (*API, error) {
	var lokiClient *loki.Client
	if logEndpoint != "" {
		var u urlutil.URLValue
		u.Set(logEndpoint)
		lokiConf, err := loki.NewDefaultConfig(logEndpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to create loki config: %w", err)
		}
		lokiClient, err = loki.New(lokiConf)
		if err != nil {
			return nil, fmt.Errorf("failed to create loki client: %w", err)
		}
	}

	ght := &GitHubTracer{
		ctx:         ctx,
		ghclient:    ghclient,
		logEndpoint: logEndpoint,
		lokiClient:  lokiClient,
		queue:       make(chan github.WorkflowRunEvent),
		quit:        make(chan struct{}),
	}
	api := API{
		ctx:    ctx,
		Router: gin.New(),
		ght:    ght,
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	api.Router.Use(
		sloggin.NewWithFilters(
			logger,
			sloggin.IgnorePath("/liveness", "/readiness"),
		),
	)
	api.Router.Use(gin.Recovery())

	// Load proxy paths
	api.Router.POST("/webhook", api.handleWebhook)
	api.Router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// If running on k8s, add liveness and readiness endpoints
	api.Router.GET("/liveness", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	api.Router.GET("/readiness", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return &api, nil
}

func (api *API) Shutdown() error {
	slog.Debug("shutting down loki client")
	api.ght.lokiClient.Stop()
	close(api.ght.quit)
	return nil
}

// Handle webhook handles the github.WorkflowRunEvent webhook
// and executes the traceWorkflowRun function
func (api *API) handleWebhook(c *gin.Context) {
	payload := github.WorkflowRunEvent{}
	err := c.BindJSON(&payload)
	if err != nil {
		slog.Debug("failed to unmarshal github.WorkflowRunEvent", "error", err)
		c.String(http.StatusBadRequest, "bad payload")
		return
	}

	// If this is a ping event, return ok
	if c.GetHeader("X-GitHub-Event") == "ping" {
		c.String(http.StatusOK, "ok")
		return
	}

	// If the payload did not bind a workflow run, return bad request
	if payload.WorkflowRun == nil {
		slog.Debug("payload does not contain workflow run")
		c.String(http.StatusBadRequest, "bad payload")
	}

	// Don't trace workflows that are not completed
	if *payload.WorkflowRun.Status != "completed" {
		slog.Debug("workflow run not completed", "status", payload.WorkflowRun.Status)
		c.String(http.StatusOK, "ok")
		return
	}

	// Queue the workflow to be traced
	api.ght.queue <- payload

	c.String(http.StatusOK, "ok")
}
