# go-gha-otel-exporter

This application allows you to emit OpenTelemetry traces to an OTEL-compatible tracing backend that measure the length of different steps within a GitHub Action pipeline.

## Usage

To build:

```bash
go build -o go-gha-otel-exporter
```

To run:

```bash
./go-gha-otel-exporter \
    --gha-pat {Your Github PAT} \
    --owner {Your Github Org} \
    --repo {Your Github Repo}
```


## Inspiration

* https://github.com/inception-health/otel-export-trace-action
* Grafana "GraCIe" App https://grafana.com/blog/2023/11/20/ci-cd-observability-via-opentelemetry-at-grafana-labs/
* OTEL CI/CD Proposal https://github.com/open-telemetry/oteps/pull/223

### Jaeger UI Example

![Jaeger UI](assets/jaeger-ui.png)
