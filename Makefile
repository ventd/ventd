# ventd — developer convenience targets.
# Shells to existing tools and scripts. No new deps.

.PHONY: help build test cover lint e2e safety-run verify-repro clean pre-push

help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build a snapshot binary via goreleaser.
	goreleaser build --snapshot --clean --single-target

pre-push: ## Run every CI gate locally (mirrors scripts/ci-local.sh). Catches gofmt/lint/build issues before they cost a 25-40 min CI cycle.
	bash scripts/ci-local.sh

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

sbom: ## Generate CycloneDX + SPDX SBOMs into dist/ via goreleaser snapshot (requires syft in PATH).
	goreleaser release --snapshot --clean
	@echo "SBOMs written to dist/ (*.cdx.json and *.spdx.json)"

verify-repro: ## Smoke-test reproducibility: two sequential builds must produce identical sha256 hashes.
	@export SOURCE_DATE_EPOCH=$$(git show -s --format=%ct HEAD); \
	export CGO_ENABLED=0; \
	export GOFLAGS=-trimpath; \
	VERSION=$$(git describe --tags --exact-match 2>/dev/null || git rev-parse --short HEAD); \
	COMMIT=$$(git rev-parse HEAD); \
	TMPDIR1=$$(mktemp -d); TMPDIR2=$$(mktemp -d); \
	MODCACHE1=$$(mktemp -d); MODCACHE2=$$(mktemp -d); \
	echo "Build 1 -> $${TMPDIR1}"; \
	GOMODCACHE=$${MODCACHE1} GOARCH=amd64 GOOS=linux go build -trimpath \
	  -ldflags="-s -w -X main.version=$${VERSION} -X main.commit=$${COMMIT} -X main.buildDate=$${SOURCE_DATE_EPOCH}" \
	  -o "$${TMPDIR1}/ventd" ./cmd/ventd; \
	GOMODCACHE=$${MODCACHE1} GOARCH=amd64 GOOS=linux go build -trimpath \
	  -ldflags="-s -w" -o "$${TMPDIR1}/ventd-recover" ./cmd/ventd-recover; \
	echo "Build 2 -> $${TMPDIR2}"; \
	GOMODCACHE=$${MODCACHE2} GOARCH=amd64 GOOS=linux go build -trimpath \
	  -ldflags="-s -w -X main.version=$${VERSION} -X main.commit=$${COMMIT} -X main.buildDate=$${SOURCE_DATE_EPOCH}" \
	  -o "$${TMPDIR2}/ventd" ./cmd/ventd; \
	GOMODCACHE=$${MODCACHE2} GOARCH=amd64 GOOS=linux go build -trimpath \
	  -ldflags="-s -w" -o "$${TMPDIR2}/ventd-recover" ./cmd/ventd-recover; \
	echo "SHA256 comparison:"; \
	for bin in ventd ventd-recover; do \
	  H1=$$(sha256sum "$${TMPDIR1}/$$bin" | awk '{print $$1}'); \
	  H2=$$(sha256sum "$${TMPDIR2}/$$bin" | awk '{print $$1}'); \
	  if [ "$$H1" = "$$H2" ]; then echo "  MATCH   $$bin: $$H1"; \
	  else echo "  MISMATCH $$bin:"; echo "    build1: $$H1"; echo "    build2: $$H2"; exit 1; fi; \
	done; \
	rm -rf "$${TMPDIR1}" "$${TMPDIR2}" "$${MODCACHE1}" "$${MODCACHE2}"; \
	echo "OK: builds are reproducible"

sync-install-sh: ## Refresh internal/web/install.sh.embedded from scripts/install.sh after editing the canonical script.
	cp scripts/install.sh internal/web/install.sh.embedded
	@echo "OK: internal/web/install.sh.embedded refreshed"

clean: ## Remove build artifacts.
	rm -rf dist/ coverage.out
