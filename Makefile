SHELL := /bin/bash

BINARY    := filenget
MODULE    := github.com/thomaslaurenson/filenget
VERSION   := $(shell git describe --tags --always --dirty --match 'v*' 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X $(MODULE)/cmd.Version=$(VERSION)
GOIMPORTS := go run golang.org/x/tools/cmd/goimports@latest -local $(MODULE)

TAG ?= $(shell git describe --tags --abbrev=0 --match 'v*' 2>/dev/null)

##@ BUILD

.PHONY: help
help: ## Show this help message
	@awk 'BEGIN {FS = ":.*?## "} /^##@ / {printf "\n%s\n", substr($$0, 5)} \
		/^[a-zA-Z_-]+:.*## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build the filenget binary into dist/
	@go build -ldflags="$(LDFLAGS)" -o dist/$(BINARY) .
	@printf '[*] built dist/%s (version %s)\n' "$(BINARY)" "$(VERSION)"

.PHONY: snapshot
snapshot: ## Build snapshot binaries with goreleaser, as release.yml would
	@goreleaser build --snapshot --clean

##@ TEST

.PHONY: test
test: ## Run tests with the race detector
	@go test -race -count=1 ./...

.PHONY: test_integration
test_integration: ## Run the live-share tests too (needs FILENGET_TEST_NETWORK)
	@go test -race -count=1 -tags=integration ./...

.PHONY: test_coverage
test_coverage: ## Run tests with a coverage report (internal/ only; cmd/ is wiring)
	@go test -race -count=1 -tags=integration -coverpkg=./internal/... \
		-coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out
	@rm coverage.out

##@ LINT

.PHONY: format
format: ## Format all Go source and group imports
	@$(GOIMPORTS) -w .

.PHONY: check_format
check_format: ## Fail if any file needs formatting
	@out="$$($(GOIMPORTS) -l .)"; test -z "$$out" || { printf 'not formatted:\n%s\n' "$$out"; exit 1; }

.PHONY: check_mod
check_mod: ## Fail if go.mod/go.sum are not tidy
	@go mod tidy
	@git diff --exit-code -- go.mod go.sum || \
	  { printf 'go.mod/go.sum not tidy; commit the diff\n' >&2; exit 1; }

.PHONY: vet
vet: ## Run go vet, including the integration-tagged files
	@go vet ./...
	@go vet -tags=integration ./...

.PHONY: check_cross
check_cross: ## Type-check the windows and darwin builds
	@GOOS=windows go vet ./...
	@GOOS=darwin go vet ./...

.PHONY: check_all
check_all: check_format check_mod vet check_cross ## Run all static checks

.PHONY: vuln
vuln: ## Scan for known vulnerabilities reachable from this code
	@go run golang.org/x/vuln/cmd/govulncheck@latest ./...

##@ GET

.PHONY: get_changelog
get_changelog: ## Print release notes for TAG to stdout (TAG=v1.0.0)
	@tag="$(TAG)"; tag="$${tag#v}"; \
	if [[ -z "$$tag" ]]; then \
	  printf 'get_changelog: TAG is empty; pass TAG=v1.0.0\n' >&2; \
	  exit 1; \
	fi; \
	notes="$$(awk -v tag="$$tag" ' \
	  /^## / { if (found) exit; if (index($$0,"## "tag" ")==1 || $$0=="## "tag) found=1; next } \
	  found { lines[n++]=$$0 } \
	  END { \
	    s=0; while (s<n && lines[s]~/^[[:space:]]*$$/) s++; \
	    e=n-1; while (e>=s && lines[e]~/^[[:space:]]*$$/) e--; \
	    for (i=s;i<=e;i++) print lines[i] \
	  }' CHANGELOG.md)"; \
	if [[ -z "$$notes" ]]; then \
	  printf 'get_changelog: no CHANGELOG entry for %s\n' "$$tag" >&2; \
	  exit 1; \
	fi; \
	printf '%s\n' "$$notes"

.PHONY: get_version
get_version: ## Print the version that would be baked into the binary
	@echo "$(VERSION)"

##@ CI

.PHONY: ci
ci: check_all test ## Run everything CI runs

.PHONY: clean
clean: ## Remove build output and the generated release artefacts
	@rm -rf dist install.sh install.ps1 checksums.txt checksums.txt.sigstore.json coverage.out
	@printf '[*] cleaned\n'
