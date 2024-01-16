.PHONY: up
up:
	docker-compose --env-file .env up -d

.PHONY: down
down:
	docker-compose down