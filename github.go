package main

import (
	"context"
	"fmt"

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

func listWorkflowJobs(accessToken, owner, repo string) {
	// Create a GitHub client using an access token
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: accessToken},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// Retrieve the workflows for a repository
	workflows, _, err := client.Actions.ListWorkflows(ctx, owner, repo, nil)
	if err != nil {
		fmt.Printf("Error retrieving workflows: %v\n", err)
		return
	}

	// Print the workflows
	// TODO - we should not list all workflows each time we create a trace; this is
	// just for testing
	fmt.Println("Workflows:")
	for _, workflow := range workflows.Workflows {
		fmt.Printf("- %s\n", *workflow.Name)

		runs, _, err := client.Actions.ListWorkflowRunsByID(ctx, owner, repo, *workflow.ID, nil)
		if err != nil {
			fmt.Printf("Error retrieving workflow runs: %v\n", err)
			return
		}

		// Print the runs
		fmt.Println("  Runs:")
		for _, run := range runs.WorkflowRuns {
			traceWorkflowRun(ctx, client, owner, repo, run)
		}
	}
}

func traceWorkflowRun(ctx context.Context, client *github.Client, owner, repo string, run *github.WorkflowRun) {
	fmt.Printf("  - %d\n", *run.ID)
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

	// Add head commit attributes if available
	/*
		if run.HeadCommit != nil {
			rootSpan.SetAttributes(
				attribute.String("github.head_commit.url", *run.HeadCommit.URL),
				attribute.String("github.head_commit.message", *run.HeadCommit.Message),
				attribute.String("github.head_commit.id", *run.HeadCommit.ID),
				attribute.String("github.head_commit.author.name", *run.HeadCommit.Author.Name),
				attribute.String("github.head_commit.author.email", *run.HeadCommit.Author.Email),
			)
		}
	*/

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
		fmt.Printf("Error retrieving workflow run jobs: %v\n", err)
		return
	}

	// End the queue span at the first job's start time
	if len(jobs.Jobs) > 0 {
		queueSpan.End(trace.WithTimestamp(*jobs.Jobs[0].StartedAt.GetTime()))
	}

	// Print the jobs
	fmt.Println("    Jobs:")
	for _, job := range jobs.Jobs {
		fmt.Printf("    - %s\n", *job.Name)
		jobCtx, jobSpan := tracer.Start(
			workflowCtx,
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
		fmt.Println("      Steps:")
		for _, step := range job.Steps {
			fmt.Printf("      - %s\n", *step.Name)
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
		}
		if job.Conclusion != nil {
			if job.Conclusion == github.String("failure") {
				jobSpan.SetStatus(codes.Error, "workflow job failed")
			}
		}
		jobSpan.End(trace.WithTimestamp(*job.CompletedAt.GetTime()))
	}
	if run.Conclusion != nil {
		if run.Conclusion == github.String("failure") {
			workflowSpan.SetStatus(codes.Error, "workflow run failed")
		}
	}
	workflowSpan.End(trace.WithTimestamp(*run.UpdatedAt.GetTime()))
}
