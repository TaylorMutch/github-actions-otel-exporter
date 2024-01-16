.PHONY: up
up:
	docker-compose --env-file .env up -d

.PHONY: down
down:
	docker-compose down

run:
	OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4317" go run . --gha-pat $(GITHUB_PAT)
