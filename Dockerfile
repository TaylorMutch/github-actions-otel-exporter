FROM golang:1.21 as build
WORKDIR /work/app
COPY . ./
RUN go mod download
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -v -ldflags '-extldflags "-static"' -o /work/app/github-actions-otel-exporter

# hadolint ignore=DL3007
FROM gcr.io/distroless/static:latest
COPY --from=build /work/app/github-actions-otel-exporter /app/github-actions-otel-exporter
ENTRYPOINT [ "/app/github-actions-otel-exporter" ]
