MIGRATIONS_DIR := db/migrations
DB_URL         ?= $(strip $(shell echo $$DB_URL))
# Pinned by tools.go, so no global install is needed. Override with GOOSE=goose if you have it.
GOOSE          ?= go run github.com/pressly/goose/v3/cmd/goose
.PHONY: up down reset migrate-up migrate-down migrate-status migrate-create

## Start infrastructure (postgres)
up:
	docker compose up -d

## Stop infrastructure
down:
	docker compose down

## Wipe volumes, restart infra, and apply all migrations
reset:
	docker compose down -v
	docker compose up -d
	$(GOOSE) -dir $(MIGRATIONS_DIR) postgres "$(DB_URL)" up

## Run all pending migrations
migrate-up:
	$(GOOSE) -dir $(MIGRATIONS_DIR) postgres "$(DB_URL)" up

## Roll back the last migration
migrate-down:
	$(GOOSE) -dir $(MIGRATIONS_DIR) postgres "$(DB_URL)" down

## Show migration status
migrate-status:
	$(GOOSE) -dir $(MIGRATIONS_DIR) postgres "$(DB_URL)" status

## Create a new migration: make migrate-create NAME=my_migration
migrate-create:
	$(GOOSE) -dir $(MIGRATIONS_DIR) create $(NAME) sql

