package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v58/github"
	"github.com/grafana/loki-client-go/loki"
	"github.com/prometheus/common/model"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/oauth2"
)

var (
	tracer = otel.GetTracerProvider().Tracer("github.actions")
)

// GitHubTracer is a struct that implements the Tracer interface
type GitHubTracer struct {
	ctx         context.Context
	ghclient    *github.Client
	ts          oauth2.TokenSource
	logEndpoint string
	logClient   *http.Client
	lokiClient  *loki.Client
	quit        chan struct{}
	queue       chan github.WorkflowRunEvent
}

// Run the GitHubTracer in a goroutine until it is called to quit
func (ght *GitHubTracer) run() {
	slog.Info("starting github tracer routine")
	for {
		select {
		case <-ght.quit:
			slog.Info("closing the github tracer routine")
			return

		case e := <-ght.queue:
			slog.Info("received workflow run event")
			fullname := e.Repo.GetFullName()
			parts := strings.Split(fullname, "/")
			owner, repo := parts[0], parts[1]
			err := ght.traceWorkflowRun(owner, repo, e.WorkflowRun)
			if err != nil {
				slog.Error("failed to trace workflow run", "error", err)
			} else {
				slog.Info("successfully traced workflow run", "run_id", *e.WorkflowRun.ID)
			}
		}
	}
}

// traceWorkflowRun traces a given workflow run
func (ght *GitHubTracer) traceWorkflowRun(
	owner,
	repo string,
	run *github.WorkflowRun,
) error {
	workflowCtx, workflowSpan := tracer.Start(
		context.Background(),
		*run.Name,
		trace.WithTimestamp(*run.CreatedAt.GetTime()),
		trace.WithAttributes(
			attribute.String("github.owner", owner),
			attribute.String("github.repo", repo),
			attribute.Int64("github.workflow_id", *run.WorkflowID),
			attribute.Int64("github.run_id", *run.ID),
			attribute.Int("github.run_number", *run.RunNumber),
			attribute.Int("github.run_attempt", *run.RunAttempt),
			attribute.String("github.html_url", *run.HTMLURL),
			attribute.String("github.created_at", run.CreatedAt.String()),
			attribute.String("github.run_started_at", run.RunStartedAt.String()),
			attribute.String("github.updated_at", run.UpdatedAt.String()),
			attribute.String("github.event", *run.Event),
			attribute.String("github.status", *run.Status),
			attribute.String("github.conclusion", *run.Conclusion),
			attribute.String("github.head_branch", *run.HeadBranch),
			attribute.String("github.head_sha", *run.HeadSHA),
			attribute.String("github.head_sha", *run.HeadSHA),
		),
	)

	// Add pull request attributes if this is a workflow triggered from a pull request
	if len(run.PullRequests) > 0 {
		workflowSpan.SetAttributes(
			attribute.String("github.head_ref", *run.PullRequests[0].Head.Ref),
			attribute.String("github.base_ref", *run.PullRequests[0].Base.Ref),
			attribute.String("github.base_sha", *run.PullRequests[0].Base.SHA),
			attribute.String("github.pull_request.url", *run.PullRequests[0].URL),
		)
	}

	// Create a span for the queue time
	_, queueSpan := tracer.Start(
		workflowCtx,
		"queue",
		trace.WithTimestamp(*run.CreatedAt.GetTime()),
	)

	// Retrieve the jobs for a workflow
	jobs, _, err := ght.ghclient.Actions.ListWorkflowJobs(ght.ctx, owner, repo, *run.ID, nil)
	if err != nil {
		return fmt.Errorf("error retrieving workflow run jobs: %w", err)
	}

	// End the queue span at the first job's start time
	if len(jobs.Jobs) > 0 {
		queueSpan.End(trace.WithTimestamp(*jobs.Jobs[0].StartedAt.GetTime()))
	}

	// Print the jobs
	for _, job := range jobs.Jobs {
		// Trace the workflow job
		jobSpanTraceID, err := ght.traceWorkflowJob(workflowCtx, owner, repo, job)
		if err != nil {
			return fmt.Errorf("error tracing workflow job: %w", err)
		}
		// Collect the logs
		if err := ght.getWorkflowJobLogs(jobSpanTraceID, owner, repo, run, job); err != nil {
			return err
		}
	}
	if run.Conclusion != nil {
		if run.Conclusion == github.String("failure") {
			workflowSpan.SetStatus(codes.Error, "workflow run failed")
		}
	}
	workflowSpan.End(trace.WithTimestamp(*run.UpdatedAt.GetTime()))
	return nil
}

func (ght *GitHubTracer) traceWorkflowJob(
	workflowCtx context.Context,
	owner,
	repo string,
	job *github.WorkflowJob,
) (string, error) {
	jobCtx, jobSpan := tracer.Start(
		workflowCtx,
		*job.Name,
		trace.WithTimestamp(*job.StartedAt.GetTime()),
		trace.WithAttributes(
			attribute.Int64("github.job.id", *job.ID),
			attribute.Int64("github.job.run_id", *job.RunID),
			attribute.String("github.job.name", *job.Name),
			attribute.String("github.job.status", *job.Status),
			attribute.String("github.job.conclusion", *job.Conclusion),
			attribute.String("github.job.html_url", *job.HTMLURL),
			attribute.String("github.job.started_at", job.StartedAt.String()),
			attribute.String("github.job.completed_at", job.CompletedAt.String()),
			attribute.StringSlice("github.job.runs_on", job.Labels),
		),
	)

	// Add runner attributes if available
	if job.RunnerGroupID != nil {
		jobSpan.SetAttributes(
			attribute.Int64("github.job.runner_group_id", *job.RunnerGroupID),
			attribute.String("github.job.runner_group_name", *job.RunnerGroupName),
			attribute.String("github.job.runner_name", *job.RunnerName),
		)
	}

	// Prints the steps
	for _, step := range job.Steps {
		err := ght.traceWorkflowStep(jobCtx, owner, repo, step)
		if err != nil {
			return "", fmt.Errorf("error tracing workflow step: %w", err)
		}
	}
	if job.Conclusion != nil {
		if job.Conclusion == github.String("failure") {
			jobSpan.SetStatus(codes.Error, "workflow job failed")
		}
	}
	jobSpan.End(trace.WithTimestamp(*job.CompletedAt.GetTime()))
	return jobSpan.SpanContext().TraceID().String(), nil
}

// traceWorkflowStep traces a given workflow step
func (ght *GitHubTracer) traceWorkflowStep(
	jobCtx context.Context,
	owner,
	repo string,
	step *github.TaskStep,
) error {
	_, stepSpan := tracer.Start(
		jobCtx,
		*step.Name,
		trace.WithTimestamp(*step.StartedAt.GetTime()),
		trace.WithAttributes(
			attribute.String("github.step.name", *step.Name),
			attribute.String("github.step.status", *step.Status),
			attribute.String("github.step.conclusion", *step.Conclusion),
			attribute.String("github.step.started_at", step.StartedAt.String()),
			attribute.String("github.step.completed_at", step.CompletedAt.String()),
			attribute.Int64("github.step.number", *step.Number),
		),
	)
	if step.Conclusion != nil {
		if step.Conclusion == github.String("failure") {
			stepSpan.SetStatus(codes.Error, "workflow step failed")
		}
	}
	stepSpan.End(trace.WithTimestamp(*step.CompletedAt.GetTime()))
	return nil
}

const (
	// LogFormat
	timestampLayout = "2006-01-02T15:04:05.9999999Z"
)

// getWorkflowJobLogs retrieves the logs for a given workflow job
func (ght *GitHubTracer) getWorkflowJobLogs(
	jobSpanTraceID,
	owner,
	repo string,
	run *github.WorkflowRun,
	job *github.WorkflowJob,
) error {
	// Skip ingesting logs if we don't have a loki endpoint configured
	if ght.lokiClient == nil {
		slog.Debug("loki client not configured, not retrieving logs")
		return nil
	}

	// Get the log retrieval url
	url, _, err := ght.ghclient.Actions.GetWorkflowJobLogs(ght.ctx, owner, repo, *job.ID, 1)
	if err != nil {
		return fmt.Errorf("error retrieving workflow job logs url: %w", err)
	}
	// Retrieve the logs
	req, err := http.NewRequestWithContext(ght.ctx, "GET", url.String(), nil)
	if err != nil {
		return fmt.Errorf("error creating request for retrieving workflow job logs: %w", err)
	}
	resp, err := ght.logClient.Do(req)
	if err != nil {
		return fmt.Errorf("error retrieving workflow job logs: %w", err)
	}
	defer resp.Body.Close()
	logLinesRaw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response body: %w", err)
	}

	labels := model.LabelSet{
		// Allow us to link the logs to the job span
		"trace_id": model.LabelValue(jobSpanTraceID),
		// Common labels to associate with the run
		"repo_owner":        model.LabelValue(owner),
		"repo_name":         model.LabelValue(repo),
		"workflow_name":     model.LabelValue(run.GetName()),
		"workflow_id":       model.LabelValue(github.Stringify(run.ID)),
		"workflow_job_name": model.LabelValue(job.GetName()),
		"workflow_job_id":   model.LabelValue(github.Stringify(job.ID)),
	}

	// For each line in the log, parse the timestamp from the log line
	// and use that as the timestamp to ingest into Loki
	logLines := strings.Split(string(logLinesRaw), "\n")
	lastTimestamp := job.StartedAt.GetTime()
	for _, log := range logLines {
		// If the log line is empty, skip it
		if len(log) == 0 {
			continue
		}

		// Multi-line logs do not include the timestamp after the first line, so we need to
		// parse the timestamp from the first line and apply it to all subsequent lines
		timestamp, err := time.Parse(timestampLayout, log[:len(timestampLayout)])
		if err != nil {
			slog.Warn("error parsing timestamp from log line", "error", err)
		} else {
			// New timestamp found, update the last timestamp
			lastTimestamp = &timestamp
		}

		// Queue the logs to be send to Loki
		err = ght.lokiClient.Handle(
			labels,
			*lastTimestamp,
			log,
		)
		if err != nil {
			return fmt.Errorf("error queuing log line to be sent to loki: %w", err)
		}
	}
	return nil
}
