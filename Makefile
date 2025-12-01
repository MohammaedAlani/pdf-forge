.PHONY: all build run test lint clean docker docker-run help

# Variables
APP_NAME := pdf-forge
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -ldflags "-w -s -X main.Version=$(VERSION)"

# Go parameters
GOCMD := go
GOBUILD := $(GOCMD) build
GOTEST := $(GOCMD) test
GOGET := $(GOCMD) get
GOMOD := $(GOCMD) mod
BINARY_DIR := bin
MAIN_PATH := ./cmd/server

# Docker
DOCKER_IMAGE := $(APP_NAME)
DOCKER_TAG := $(VERSION)

# Default target
all: lint test build

## Build

build: ## Build the binary
	@echo "Building $(APP_NAME)..."
	@mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 $(GOBUILD) $(LDFLAGS) -o $(BINARY_DIR)/$(APP_NAME) $(MAIN_PATH)
	@echo "Binary: $(BINARY_DIR)/$(APP_NAME)"

build-all: ## Build for all platforms
	@echo "Building for all platforms..."
	@mkdir -p $(BINARY_DIR)
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BINARY_DIR)/$(APP_NAME)-linux-amd64 $(MAIN_PATH)
	GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BINARY_DIR)/$(APP_NAME)-linux-arm64 $(MAIN_PATH)
	GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BINARY_DIR)/$(APP_NAME)-darwin-amd64 $(MAIN_PATH)
	GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BINARY_DIR)/$(APP_NAME)-darwin-arm64 $(MAIN_PATH)
	@echo "Built all platforms in $(BINARY_DIR)/"

## Run

run: ## Run the application locally
	$(GOCMD) run $(MAIN_PATH)

run-dev: ## Run with hot reload (requires air)
	air -c .air.toml

## Test

test: ## Run tests
	$(GOTEST) -v -race -cover ./...

test-coverage: ## Run tests with coverage report
	$(GOTEST) -v -race -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

bench: ## Run benchmarks
	$(GOTEST) -bench=. -benchmem ./...

## Lint

lint: ## Run linters
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run ./...

fmt: ## Format code
	$(GOCMD) fmt ./...
	gofumpt -l -w .

## Dependencies

deps: ## Download dependencies
	$(GOMOD) download
	$(GOMOD) tidy

deps-update: ## Update dependencies
	$(GOMOD) tidy
	$(GOCMD) get -u ./...
	$(GOMOD) tidy

## Docker

docker: ## Build Docker image
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .
	docker tag $(DOCKER_IMAGE):$(DOCKER_TAG) $(DOCKER_IMAGE):latest

docker-run: ## Run Docker container
	docker run -p 8080:8080 --rm $(DOCKER_IMAGE):latest

docker-push: ## Push Docker image
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)
	docker push $(DOCKER_IMAGE):latest

compose-up: ## Start with docker-compose
	docker-compose up -d

compose-down: ## Stop docker-compose
	docker-compose down

compose-logs: ## View docker-compose logs
	docker-compose logs -f

## Clean

clean: ## Clean build artifacts
	rm -rf $(BINARY_DIR)
	rm -f coverage.out coverage.html
	rm -f *.pdf
	$(GOCMD) clean

## Tools

tools: ## Install development tools
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install mvdan.cc/gofumpt@latest
	go install github.com/cosmtrek/air@latest

## Help

help: ## Show this help
	@echo "PDF Forge - Makefile Commands"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# Aliases
.DEFAULT_GOAL := help
