package main

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/google/go-github/v58/github"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/oauth2"
)

var (
	tracer = otel.GetTracerProvider().Tracer("github.actions")
)

// traceWorkflowRun traces a given workflow run
func traceWorkflowRun(
	ctx context.Context,
	ts oauth2.TokenSource,
	client *github.Client,
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
	jobs, _, err := client.Actions.ListWorkflowJobs(ctx, owner, repo, *run.ID, nil)
	if err != nil {
		return fmt.Errorf("error retrieving workflow run jobs: %w", err)
	}

	// End the queue span at the first job's start time
	if len(jobs.Jobs) > 0 {
		queueSpan.End(trace.WithTimestamp(*jobs.Jobs[0].StartedAt.GetTime()))
	}

	// Print the jobs
	for _, job := range jobs.Jobs {
		err := traceWorkflowJob(workflowCtx, ts, client, owner, repo, job)
		if err != nil {
			return fmt.Errorf("error tracing workflow job: %w", err)
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

func traceWorkflowJob(
	ctx context.Context,
	ts oauth2.TokenSource,
	client *github.Client,
	owner,
	repo string,
	job *github.WorkflowJob,
) error {
	jobCtx, jobSpan := tracer.Start(
		ctx,
		*job.Name,
		trace.WithTimestamp(*job.StartedAt.GetTime()),
		trace.WithAttributes(
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
		err := traceWorkflowStep(jobCtx, ts, client, owner, repo, step)
		if err != nil {
			return fmt.Errorf("error tracing workflow step: %w", err)
		}
	}
	if job.Conclusion != nil {
		if job.Conclusion == github.String("failure") {
			jobSpan.SetStatus(codes.Error, "workflow job failed")
		}
	}
	jobSpan.End(trace.WithTimestamp(*job.CompletedAt.GetTime()))

	getWorkflowJobLogs(ctx, ts, client, owner, repo, job)
	return nil
}

// traceWorkflowStep traces a given workflow step
func traceWorkflowStep(
	ctx context.Context,
	ts oauth2.TokenSource,
	client *github.Client,
	owner,
	repo string,
	step *github.TaskStep,
) error {
	_, stepSpan := tracer.Start(
		ctx,
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

// getWorkflowJobLogs retrieves the logs for a given workflow job
func getWorkflowJobLogs(
	ctx context.Context,
	ts oauth2.TokenSource,
	client *github.Client,
	owner,
	repo string,
	job *github.WorkflowJob,
) error {
	// Get the log retrieval url
	url, _, err := client.Actions.GetWorkflowJobLogs(ctx, owner, repo, *job.ID, 1)
	if err != nil {
		return fmt.Errorf("error retrieving workflow job logs url: %w", err)
	}

	logClient := oauth2.NewClient(ctx, ts)
	req, err := http.NewRequestWithContext(ctx, "GET", url.String(), nil)
	if err != nil {
		return fmt.Errorf("error creating request for retrieving workflow job logs: %w", err)
	}
	resp, err := logClient.Do(req)
	if err != nil {
		return fmt.Errorf("error retrieving workflow job logs: %w", err)
	}
	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response body: %w", err)
	}

	// TODO - ingest the logs for a given trace into Loki
	return nil
}
