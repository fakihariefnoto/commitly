# Commitly — build, test, lint, package

.PHONY: build run install test test-coverage lint security completions man snapshot release-check tools clean help

APP_NAME := commitly
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE     := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"

build: ## Build the CLI binary into bin/
	go build $(LDFLAGS) -o bin/$(APP_NAME) ./cmd/$(APP_NAME)
	ln -sf $(APP_NAME) bin/git-cm

run: ## Run the CLI (pass args with ARGS="...")
	go run ./cmd/$(APP_NAME) $(ARGS)

install: ## Install into $GOPATH/bin for local use
	go install $(LDFLAGS) ./cmd/$(APP_NAME)

test: ## Run tests (includes golden-file output tests)
	go test ./... -count=1

test-coverage: ## Run tests with HTML coverage report
	go test ./... -coverprofile=coverage.out -covermode=atomic
	go tool cover -html=coverage.out -o coverage.html

lint: ## Run linter (includes gosec as one of the bundled linters)
	golangci-lint run ./...

security: ## Run gosec standalone
	gosec ./...

completions: build ## Generate shell completions into completions/ (packaged by brew/deb/rpm)
	@mkdir -p completions
	./bin/$(APP_NAME) completion bash > completions/$(APP_NAME).bash
	./bin/$(APP_NAME) completion zsh  > completions/_$(APP_NAME)
	./bin/$(APP_NAME) completion fish > completions/$(APP_NAME).fish
	./bin/$(APP_NAME) completion powershell > completions/$(APP_NAME).ps1

man: build ## Generate the man page into man/ (expected by .deb/.rpm users)
	@mkdir -p man
	./bin/$(APP_NAME) man > man/$(APP_NAME).1

snapshot: ## Build all release artifacts locally without publishing (verify before tagging)
	goreleaser release --snapshot --clean

release-check: ## Validate .goreleaser.yaml without building
	goreleaser check

tools: ## Install dev tools (golangci-lint, gosec, goreleaser)
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	go install github.com/goreleaser/goreleaser/v2@latest

clean: ## Clean build artifacts
	rm -rf bin/ dist/ completions/ man/ coverage.out coverage.html

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
