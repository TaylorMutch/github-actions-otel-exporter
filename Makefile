.PHONY: default
default: help

.PHONY: up
up: ## Start the docker-compose stack.
	docker-compose --env-file .env up -d

.PHONY: down
down: ## Stop the docker-compose stack.
	docker-compose down

.PHONY: run
run: ## Run the exporter locally. This is useful for testing outside of docker-compose.
	GHA_PAT="$(GITHUB_PAT)" OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4317" LOG_ENDPOINT="http://localhost:3100/loki/api/v1/push" go run .

.PHONY: smee
smee: ## Run the smee.io proxy client. Must have SMEE_URL set.
	smee --url $(SMEE_URL) --path /webhook --port 8081

.PHONY: help
help: ## Makefile help.
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
