# ventd — developer convenience targets.
# Shells to existing tools and scripts. No new deps.

.PHONY: help build test cover lint e2e safety-run issue-review test-issue-logger clean

help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build a snapshot binary via goreleaser.
	goreleaser build --snapshot --clean --single-target

test: ## Run the full test suite with race detector.
	go test -race ./...

cover: ## Run tests with coverage and print per-package.
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -30

lint: ## Run golangci-lint.
	golangci-lint run --timeout=5m

e2e: ## Run the fresh-VM smoke suite (requires vagrant).
	@if command -v vagrant >/dev/null 2>&1; then \
		bash validation/fresh-vm-smoke.sh; \
	else \
		echo "vagrant not installed — skipping e2e"; \
	fi

safety-run: ## Run the hwmon-safety invariant subtests.
	go test -race -run 'Test.*Safety' ./internal/controller/...

issue-review: ## Audit recent CC output for missed issues.
	@if [ -x scripts/cc-issue-logger.sh ]; then \
		bash scripts/cc-issue-logger.sh --audit; \
	else \
		echo "scripts/cc-issue-logger.sh not found"; exit 1; \
	fi

test-issue-logger: ## Run the issue-logger self-tests.
	@if [ -f scripts/cc-issue-logger.test.sh ]; then \
		bash scripts/cc-issue-logger.test.sh; \
	else \
		echo "scripts/cc-issue-logger.test.sh not found — skipping"; \
	fi

sbom: ## Generate CycloneDX + SPDX SBOMs into dist/ via goreleaser snapshot (requires syft in PATH).
	goreleaser release --snapshot --clean
	@echo "SBOMs written to dist/ (*.cdx.json and *.spdx.json)"

clean: ## Remove build artifacts.
	rm -rf dist/ coverage.out
