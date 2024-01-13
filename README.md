# github-actions-otel-exporter

This application allows you to emit OpenTelemetry traces to an OTEL-compatible tracing backend that measure the length of different steps within a GitHub Action pipeline.

## Usage

To build:

```bash
go build -o github-actions-otel-exporter
```

To run:

```bash
# Start the backend
docker-compose up -d

# Create the traces
./github-actions-otel-exporter \
    --gha-pat {Your Github PAT} \
    --owner {Your Github Org} \
    --repo {Your Github Repo}
```

Visit the Jaeger UI at http://localhost:16686 to see the traces.

* **NOTE**: The traces will use the timestamps of the actions workflow runs and jobs, so you may need to adjust query times to see the traces.

## Inspiration

* Attribute mapping from https://github.com/inception-health/otel-export-trace-action
* Grafana "GraCIe" App https://grafana.com/blog/2023/11/20/ci-cd-observability-via-opentelemetry-at-grafana-labs/
* OTEL CI/CD Proposal https://github.com/open-telemetry/oteps/pull/223

### Jaeger UI Example

![Jaeger UI](assets/jaeger-ui.png)

## Architecture

The following diagram describes a high level architecture for this application.

![Architecture](assets/arch.png)

* A GitHub webhook is configured to deliver "workflow runs completed" events to the application
* The application receives the webhook and queries the GitHub API for the workflow run and job details
* The application emits a trace to the configured OTEL backend with spans for each step in the workflow run
* The application emits a log stream to a configured logging backend with the contents of the log files for each job in the workflow run

In the example above we are using [Grafana](https://github.com/grafana/grafana) as a visualization layer for the traces (stored in [Tempo](https://github.com/grafana/tempo)) and logs (stored in [Loki](https://github.com/grafana/loki)), but any OTEL-compatible backend can be used.
