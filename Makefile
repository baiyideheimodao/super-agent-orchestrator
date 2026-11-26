SHELL := /bin/bash
.DEFAULT_GOAL := help

.PHONY: help dev test build clean run docker

help:
	@printf '%s\n' \
	  'make dev    start Go API and Vite frontend together' \
	  'make test   run Go tests and frontend typecheck/build' \
	  'make build  build the production frontend and Go binary' \
	  'make run    serve the production build on :8080' \
	  'make docker build and run with docker compose' \
	  'make clean  remove generated build artifacts'

dev:
	@./scripts/dev.sh

test:
	@./scripts/test.sh

build:
	@bash -c 'source ./scripts/env.sh; setup_node; setup_pnpm; install_web; pnpm_web build; \
	  mkdir -p bin .cache/go-build .cache/gomod; GOCACHE=$$PWD/.cache/go-build GOMODCACHE=$$PWD/.cache/gomod go build -o bin/server ./cmd/server'
	@echo '[ok] bin/server and web/dist are ready'

clean:
	@rm -rf bin web/dist .cache

run: build
	@WEB_DIR=web/dist ./bin/server

docker:
	@docker compose up --build
