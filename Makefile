ifneq (,$(wildcard ./.env))
    include .env
    export
endif

BINARY_NAME=spotter-server
ADMIN_BINARY_NAME=spotter-admin
MAIN_PATH=./cmd/server/main.go
ADMIN_MAIN_PATH=./cmd/admin/main.go

# Determine version: git tag > branch name > short SHA
GIT_TAG    := $(shell git describe --exact-match --tags HEAD 2>/dev/null)
GIT_BRANCH := $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null)
GIT_SHA    := $(shell git rev-parse --short HEAD 2>/dev/null)
ifeq ($(GIT_TAG),)
    ifeq ($(GIT_BRANCH),HEAD)
        VERSION := $(GIT_SHA)
    else
        VERSION := $(GIT_BRANCH)
    endif
else
    VERSION := $(GIT_TAG)
endif
LDFLAGS := -ldflags "-X spotter/internal/version.Version=$(VERSION)"

.PHONY: all help deps ci-deps docker-deps generate css build build-binary build-admin run dev test test-coverage lint lint-docker lint-docs lint-go lint-md lint-templ lint-yaml clean docker-build docker-run docs-deps docs-build docs-serve docs-clean

all: build

help: ## Show this help message
	@echo 'Usage:'
	@echo '  make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

deps: ## Install all dependencies (Go, Node, tools, docs)
	@echo "Installing Go dependencies..."
	go mod download
	@echo "Installing development tools..."
	go install github.com/a-h/templ/cmd/templ@latest
	go install github.com/air-verse/air@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8
	@echo "Installing Node dependencies..."
	npm install
	@echo "Installing documentation dependencies..."
	cd docs-site && npm install
	@echo "✓ All dependencies installed"

ci-deps: ## Install CI dependencies (minimal, no dev tools)
	@echo "Installing Go dependencies..."
	go mod download
	@echo "Installing templ..."
	go install github.com/a-h/templ/cmd/templ@$(shell go list -m -f '{{.Version}}' github.com/a-h/templ)
	@echo "Installing golangci-lint..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8
	@echo "✓ CI dependencies installed"

docker-deps: ## Install Docker build dependencies (Go, templ, Node)
	@echo "Installing Go dependencies..."
	go mod download
	@echo "Installing templ..."
	go install github.com/a-h/templ/cmd/templ@latest
	@echo "Installing Node dependencies..."
	npm install
	@echo "✓ Docker dependencies installed"

generate: ## Generate code (Ent schemas and Templ templates)
	@echo "Generating Ent code..."
	go generate ./ent
	@echo "Generating Templ templates..."
	templ generate
	@echo "✓ Code generation complete"

css: ## Build CSS from Tailwind
	@echo "Building CSS..."
	npx tailwindcss -i ./static/css/input.css -o ./static/css/output.css --minify
	@echo "✓ CSS built"

build: generate css ## Build the application binary
	@echo "Building $(BINARY_NAME) (version: $(VERSION))..."
	go build $(LDFLAGS) -o $(BINARY_NAME) $(MAIN_PATH)
	@echo "✓ Build complete: $(BINARY_NAME)"

build-binary: ## Build only the binary (no code generation)
	@echo "Building $(BINARY_NAME) (version: $(VERSION))..."
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(BINARY_NAME) $(MAIN_PATH)
	@echo "✓ Build complete: $(BINARY_NAME)"

build-admin: ## Build the admin CLI binary
	@echo "Building $(ADMIN_BINARY_NAME)..."
	CGO_ENABLED=1 go build -o $(ADMIN_BINARY_NAME) $(ADMIN_MAIN_PATH)
	@echo "✓ Build complete: $(ADMIN_BINARY_NAME)"

run: dev ## Alias for 'make dev'

dev: ## Run development server with hot-reload (requires .env file)
	@echo "Starting development server..."
	@echo "Air: Go hot-reload on http://localhost:8080"
	@echo "Templ: Template watching with proxy on http://localhost:7331"
	@echo "Tailwind: CSS watching"
	@echo ""
	npx concurrently --kill-others --prefix none \
		"air" \
		"templ generate --watch --proxy='http://localhost:8080' --open-browser=false" \
		"npx tailwindcss -i ./static/css/input.css -o ./static/css/output.css --watch"

test: generate ## Run all tests
	@echo "Running tests..."
	go test -v ./...

test-coverage: generate ## Run tests with coverage report
	@echo "Running tests with coverage..."
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "✓ Coverage report generated: coverage.html"

lint: ## Run all linters
	@echo "Running all linters..."
	@echo ""
	@echo "→ Go (golangci-lint)..."
	@$(MAKE) lint-go
	@echo ""
	@echo "→ Templ templates..."
	@$(MAKE) lint-templ
	@echo ""
	@echo "→ YAML files..."
	@$(MAKE) lint-yaml
	@echo ""
	@echo "→ Markdown files..."
	@$(MAKE) lint-md
	@echo ""
	@echo "→ Dockerfile..."
	@$(MAKE) lint-docker
	@echo ""
	@echo "→ Documentation (Docusaurus build)..."
	@$(MAKE) lint-docs
	@echo ""
	@echo "✓ All linters passed"

lint-yaml: ## Run yamllint on YAML files
	@echo "Linting YAML files..."
	@yamllint .
	@echo "✓ YAML linting passed"

lint-docker: ## Run hadolint on Dockerfile
	@echo "Linting Dockerfile..."
	@docker run --rm -i hadolint/hadolint hadolint --failure-threshold error - < Dockerfile
	@echo "✓ Dockerfile linting passed"

lint-templ: ## Run templ fmt to check Templ template formatting
	@echo "Checking Templ template formatting..."
	@templ fmt -fail .
	@echo "✓ Templ formatting check passed"

lint-md: ## Run markdownlint on Markdown files
	@echo "Linting Markdown files..."
	@npx markdownlint "**/*.md" --ignore node_modules --ignore "docs-site/node_modules"
	@echo "✓ Markdown linting passed"

lint-docs: ## Build Docusaurus docs (checks for broken links)
	@echo "Building documentation to check for broken links..."
	@cd docs-site && npm run build
	@echo "✓ Documentation build passed"

lint-go: generate ## Run golangci-lint on Go code
	@echo "Running golangci-lint..."
	@golangci-lint run ./...
	@echo "✓ Go linting passed"

clean: ## Remove build artifacts
	@echo "Cleaning build artifacts..."
	rm -f $(BINARY_NAME)
	rm -f $(ADMIN_BINARY_NAME)
	rm -f ./static/css/output.css
	rm -f coverage.out coverage.html
	rm -rf tmp/
	rm -f build-errors.log
	@echo "✓ Clean complete"

clean-all: clean ## Remove all generated files (ent, node_modules, cached images)
	@echo "Cleaning all generated files..."
	rm -rf node_modules/
	rm -rf data/images/
	find ent/ -type f ! -path 'ent/schema/*' ! -name 'generate.go' -delete 2>/dev/null || true
	find ent/ -type d -empty -delete 2>/dev/null || true
	@echo "✓ Full clean complete"

regenerate: clean-all deps generate ## Clean all and regenerate from scratch
	@echo "✓ Regeneration complete"

docker-build: ## Build Docker image
	@echo "Building Docker image..."
	docker build -t spotter .
	@echo "✓ Docker image built: spotter"

docker-run: ## Run application in Docker
	@echo "Starting Spotter in Docker..."
	docker run -p 8080:8080 --env-file .env spotter

# ===================
# Documentation
# ===================

docs-deps: ## Install documentation dependencies
	@echo "Installing documentation dependencies..."
	cd docs-site && npm install
	@echo "✓ Documentation dependencies installed"

docs-build: ## Build documentation site
	@echo "Building documentation..."
	cd docs-site && npm run build
	@echo "✓ Documentation built to docs-site/build/"

docs-serve: ## Start local documentation server with hot-reload
	@echo "Starting documentation server..."
	@echo "Documentation will be available at http://localhost:3000/spotter/"
	cd docs-site && npm start

docs-clean: ## Clean documentation build artifacts
	@echo "Cleaning documentation artifacts..."
	rm -rf docs-site/build docs-site/.docusaurus docs-site/.cache-loader
	@echo "✓ Documentation cleaned"
