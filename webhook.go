package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v58/github"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	sloggin "github.com/samber/slog-gin"
	"golang.org/x/oauth2"
)

// API is the main API struct
type API struct {
	ctx    context.Context
	Router *gin.Engine
	ght    *GitHubTracer
}

// NewAPI creates a new API instance
func NewAPI(ctx context.Context, ts oauth2.TokenSource, ghclient *github.Client, logEndpoint string) *API {
	ght := &GitHubTracer{
		ctx:         ctx,
		ghclient:    ghclient,
		ts:          ts,
		logEndpoint: logEndpoint,
		logClient:   oauth2.NewClient(ctx, ts),
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
	return &api
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

	// Don't trace workflows that are not completed
	if *payload.WorkflowRun.Status != "completed" {
		slog.Debug("workflow run not completed", "status", payload.WorkflowRun.Status)
		c.String(http.StatusOK, "ok")
		return
	}

	fullname := payload.Repo.GetFullName()
	parts := strings.Split(fullname, "/")
	owner, repo := parts[0], parts[1]

	// TODO - send the workflow run to a channel to be processed
	// so that we can return a response to GitHub as quickly as possible;
	// we don't want to fail http requests by us taking too long to process
	err = api.ght.traceWorkflowRun(owner, repo, payload.WorkflowRun)
	if err != nil {
		slog.Error("failed to trace workflow run", "error", err)
		// We don't want to return an error to GitHub, so we just log it
	} else {
		slog.Info("successfully traced workflow run", "run_id", *payload.WorkflowRun.ID)
	}
	c.String(http.StatusOK, "ok")
}
