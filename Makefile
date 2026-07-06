# WUT - AI-Powered Command Helper
# Cross-platform Makefile for building, testing, and deployment

# Binary name
BINARY_NAME=wut
PACKAGE=github.com/thirawat27/wut

# Version info
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "1.0.0")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S' 2>/dev/null || echo "unknown")
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_HOST=$(shell hostname 2>/dev/null || echo "unknown")
GO_VERSION=$(shell go version | cut -d' ' -f3)

# Build flags
LDFLAGS=-ldflags "-s -w \
	-X main.Version=$(VERSION) \
	-X main.BuildTime=$(BUILD_TIME) \
	-X main.Commit=$(COMMIT) \
	-X main.BuildHost=$(BUILD_HOST) \
	-X main.GoVersion=$(GO_VERSION)"

# Go settings
GOCMD=go
GOBUILD=$(GOCMD) build -trimpath
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt

# Directories
BUILD_DIR=./build
BUILD_WINDOWS_DIR=$(BUILD_DIR)/windows
DIST_DIR=./dist
SCRIPTS_DIR=./scripts

# Platforms for cross-compilation
PLATFORMS=darwin/amd64 darwin/arm64 \
	linux/amd64 linux/arm64 linux/386 linux/arm \
	windows/amd64 windows/arm64 windows/386 \
	freebsd/amd64 freebsd/arm64 \
	openbsd/amd64 \
	netbsd/amd64

# Colors for terminal output
BLUE=\033[36m
GREEN=\033[32m
YELLOW=\033[33m
RED=\033[31m
NC=\033[0m # No Color

.PHONY: all build clean test deps lint fmt install uninstall run help \
	build-all build-windows build-linux build-macos build-bsd \
	build-windows-installer installer-windows \
	install-local install-global \
	docker docker-push \
	release release-check release-goreleaser release-snapshot \
	fmt-check vet staticcheck \
	check ci \
	goreleaser-check goreleaser-snapshot goreleaser-release

# Default target
all: clean deps check build

# Build for current platform
build:
	@echo "$(BLUE)Building $(BINARY_NAME) for current platform...$(NC)"
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) .
	@echo "$(GREEN)✓ Build complete: $(BUILD_DIR)/$(BINARY_NAME)$(NC)"

# Build for all platforms
build-all: $(PLATFORMS)

$(PLATFORMS):
	@echo "$(BLUE)Building for $@...$(NC)"
	@mkdir -p $(DIST_DIR)
	@GOOS=$(word 1,$(subst /, ,$@)) GOARCH=$(word 2,$(subst /, ,$@)) \
		$(GOBUILD) $(LDFLAGS) \
		-o $(DIST_DIR)/$(BINARY_NAME)-$(word 1,$(subst /, ,$@))-$(word 2,$(subst /, ,$@))$(if $(filter windows,$(word 1,$(subst /, ,$@))),.exe,) .

# Platform-specific builds
build-windows:
	@echo "$(BLUE)Building for Windows...$(NC)"
	@mkdir -p $(DIST_DIR)
	@GOOS=windows GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-windows-amd64.exe .
	@GOOS=windows GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-windows-arm64.exe .
	@GOOS=windows GOARCH=386 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-windows-386.exe .
	@echo "$(GREEN)✓ Windows builds complete$(NC)"

build-linux:
	@echo "$(BLUE)Building for Linux...$(NC)"
	@mkdir -p $(DIST_DIR)
	@GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 .
	@GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 .
	@GOOS=linux GOARCH=arm $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm .
	@GOOS=linux GOARCH=386 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-linux-386 .
	@echo "$(GREEN)✓ Linux builds complete$(NC)"

build-macos:
	@echo "$(BLUE)Building for macOS...$(NC)"
	@mkdir -p $(DIST_DIR)
	@GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 .
	@GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 .
	@echo "$(GREEN)✓ macOS builds complete$(NC)"

build-bsd:
	@echo "$(BLUE)Building for BSD...$(NC)"
	@mkdir -p $(DIST_DIR)
	@GOOS=freebsd GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-freebsd-amd64 .
	@GOOS=openbsd GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-openbsd-amd64 .
	@GOOS=netbsd GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-netbsd-amd64 .
	@echo "$(GREEN)✓ BSD builds complete$(NC)"

# Build Windows exe for installer (into build/windows/)
build-windows-installer:
	@echo "$(BLUE)Building Windows executable for installer...$(NC)"
	@mkdir -p $(BUILD_WINDOWS_DIR)
	@GOOS=windows GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_WINDOWS_DIR)/$(BINARY_NAME).exe .
	@echo "$(GREEN)✓ Windows build complete: $(BUILD_WINDOWS_DIR)/$(BINARY_NAME).exe$(NC)"

# Build Windows installer using Inno Setup
installer-windows: build-windows-installer
	@echo "$(BLUE)Building Windows installer with Inno Setup...$(NC)"
	@if command -v iscc >/dev/null 2>&1 || command -v "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" >/dev/null 2>&1; then \
		mkdir -p $(DIST_DIR); \
		if command -v iscc >/dev/null 2>&1; then \
			iscc $(SCRIPTS_DIR)/wut-installer.iss; \
		else \
			"C:\Program Files (x86)\Inno Setup 6\ISCC.exe" $(SCRIPTS_DIR)/wut-installer.iss; \
		fi; \
		echo "$(GREEN)✓ Installer created in $(DIST_DIR)/$(NC)"; \
	else \
		echo "$(RED)✗ Inno Setup not found$(NC)"; \
		echo "$(YELLOW)Please install Inno Setup from: https://jrsoftware.org/isdl.php$(NC)"; \
		exit 1; \
	fi

# Clean build artifacts
clean:
	@echo "$(YELLOW)Cleaning...$(NC)"
	@$(GOCLEAN)
	@rm -rf $(BUILD_DIR) $(DIST_DIR)
	@echo "$(GREEN)✓ Clean complete$(NC)"

# Run tests
test:
	@echo "$(BLUE)Running tests...$(NC)"
	@$(GOTEST) -v -race ./...

# Run tests with coverage
test-coverage:
	@echo "$(BLUE)Running tests with coverage...$(NC)"
	@$(GOTEST) -v -race -coverprofile=coverage.out ./...
	@$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "$(GREEN)✓ Coverage report: coverage.html$(NC)"

# Download dependencies
deps:
	@echo "$(BLUE)Downloading dependencies...$(NC)"
	@$(GOMOD) download
	@$(GOMOD) tidy
	@echo "$(GREEN)✓ Dependencies ready$(NC)"

# Format code
fmt:
	@echo "$(BLUE)Formatting code...$(NC)"
	@$(GOFMT) ./...
	@echo "$(GREEN)✓ Formatting complete$(NC)"

# Check formatting
fmt-check:
	@echo "$(BLUE)Checking formatting...$(NC)"
	@test -z "$$($(GOFMT) -l .)" || (echo "$(RED)✗ Formatting issues found$(NC)" && $(GOFMT) -d . && exit 1)
	@echo "$(GREEN)✓ Formatting OK$(NC)"

# Run vet
vet:
	@echo "$(BLUE)Running go vet...$(NC)"
	@$(GOCMD) vet ./...
	@echo "$(GREEN)✓ Vet passed$(NC)"

# Run staticcheck (if installed)
staticcheck:
	@if command -v staticcheck >/dev/null 2>&1; then \
		echo "$(BLUE)Running staticcheck...$(NC)"; \
		staticcheck ./...; \
		echo "$(GREEN)✓ Staticcheck passed$(NC)"; \
	else \
		echo "$(YELLOW)⚠ staticcheck not installed, skipping$(NC)"; \
	fi

# Run linter (golangci-lint)
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		echo "$(BLUE)Running linter...$(NC)"; \
		golangci-lint run; \
		echo "$(GREEN)✓ Lint passed$(NC)"; \
	else \
		echo "$(YELLOW)⚠ golangci-lint not installed$(NC)"; \
		echo "$(BLUE)Install from: https://golangci-lint.run/usage/install/$(NC)"; \
	fi

# Run all checks
check: fmt-check vet staticcheck lint

# CI target (runs all checks and tests)
ci: deps check test

# Install locally (current user)
install-local: build
	@echo "$(BLUE)Installing locally...$(NC)"
	@mkdir -p $(HOME)/.local/bin
	@cp $(BUILD_DIR)/$(BINARY_NAME) $(HOME)/.local/bin/
	@echo "$(GREEN)✓ Installed to $(HOME)/.local/bin/$(BINARY_NAME)$(NC)"
	@echo "$(YELLOW)Make sure $(HOME)/.local/bin is in your PATH$(NC)"

# Install globally (requires sudo)
install-global: build
	@echo "$(BLUE)Installing globally...$(NC)"
	@sudo cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/
	@echo "$(GREEN)✓ Installed to /usr/local/bin/$(BINARY_NAME)$(NC)"

# Uninstall local
uninstall-local:
	@echo "$(YELLOW)Uninstalling local installation...$(NC)"
	@rm -f $(HOME)/.local/bin/$(BINARY_NAME)
	@echo "$(GREEN)✓ Uninstalled$(NC)"

# Uninstall global
uninstall-global:
	@echo "$(YELLOW)Uninstalling global installation...$(NC)"
	@sudo rm -f /usr/local/bin/$(BINARY_NAME)
	@echo "$(GREEN)✓ Uninstalled$(NC)"

# Run the application
run: build
	@$(BUILD_DIR)/$(BINARY_NAME)

# Development mode with hot reload (requires air)
dev:
	@if command -v air >/dev/null 2>&1; then \
		air; \
	else \
		echo "$(YELLOW)air not installed. Install with: go install github.com/cosmtrek/air@latest$(NC)"; \
		exit 1; \
	fi

# Docker build
docker:
	@echo "$(BLUE)Building Docker image...$(NC)"
	@docker build -t $(BINARY_NAME):$(VERSION) -t $(BINARY_NAME):latest .
	@echo "$(GREEN)✓ Docker image built$(NC)"

# Docker push (requires login)
docker-push: docker
	@echo "$(BLUE)Pushing Docker image...$(NC)"
	@docker tag $(BINARY_NAME):$(VERSION) ghcr.io/thirawat27/$(BINARY_NAME):$(VERSION)
	@docker tag $(BINARY_NAME):latest ghcr.io/thirawat27/$(BINARY_NAME):latest
	@docker push ghcr.io/thirawat27/$(BINARY_NAME):$(VERSION)
	@docker push ghcr.io/thirawat27/$(BINARY_NAME):latest
	@echo "$(GREEN)✓ Docker image pushed$(NC)"

# Check release prerequisites
release-check:
	@echo "$(BLUE)Checking release prerequisites...$(NC)"
	@if ! command -v git >/dev/null 2>&1; then \
		echo "$(RED)✗ git is required$(NC)"; exit 1; \
	fi
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "$(YELLOW)⚠ Uncommitted changes detected$(NC)"; \
	fi
	@if ! git describe --tags --exact-match HEAD >/dev/null 2>&1; then \
		echo "$(YELLOW)⚠ Current commit is not tagged$(NC)"; \
	fi
	@echo "$(GREEN)✓ Release check complete$(NC)"

# Create release
release: clean release-check build-all
	@echo "$(BLUE)Creating release $(VERSION)...$(NC)"
	@mkdir -p $(DIST_DIR)
	@cd $(DIST_DIR) && for f in *; do \
		if [ -f "$$f" ]; then \
			sha256sum "$$f" > "$$f.sha256"; \
		fi; \
	done
	@echo "$(GREEN)✓ Release artifacts ready in $(DIST_DIR)/$(NC)"

# Generate completions
completions: build
	@echo "$(BLUE)Generating shell completions...$(NC)"
	@mkdir -p completions
	@$(BUILD_DIR)/$(BINARY_NAME) completion bash > completions/$(BINARY_NAME).bash
	@$(BUILD_DIR)/$(BINARY_NAME) completion zsh > completions/_$(BINARY_NAME)
	@$(BUILD_DIR)/$(BINARY_NAME) completion fish > completions/$(BINARY_NAME).fish
	@$(BUILD_DIR)/$(BINARY_NAME) completion powershell > completions/_$(BINARY_NAME).ps1
	@echo "$(GREEN)✓ Completions generated in completions/$(NC)"

# Install shell integration
install-shell: build
	@echo "$(BLUE)Installing shell integration...$(NC)"
	@$(BUILD_DIR)/$(BINARY_NAME) install --all
	@echo "$(GREEN)✓ Shell integration installed$(NC)"

# Generate man pages
man: build
	@if command -v go-md2man >/dev/null 2>&1 || command -v pandoc >/dev/null 2>&1; then \
		echo "$(BLUE)Generating man pages...$(NC)"; \
		mkdir -p man; \
		$(BUILD_DIR)/$(BINARY_NAME) man > man/$(BINARY_NAME).1; \
		echo "$(GREEN)✓ Man pages generated$(NC)"; \
	else \
		echo "$(YELLOW)⚠ go-md2man or pandoc required for man pages$(NC)"; \
	fi

# Show help
help:
	@echo "$(BLUE)WUT - AI-Powered Command Helper$(NC)"
	@echo ""
	@echo "$(BOLD)Build Targets:$(NC)"
	@echo "  make build           Build for current platform"
	@echo "  make build-all       Build for all platforms"
	@echo "  make build-windows         Build for Windows
  make build-windows-installer  Build Windows exe for installer (build/windows/)
  make installer-windows     Build Windows installer with Inno Setup"
	@echo "  make build-linux     Build for Linux"
	@echo "  make build-macos     Build for macOS"
	@echo "  make build-bsd       Build for BSD systems"
	@echo ""
	@echo "$(BOLD)Development:$(NC)"
	@echo "  make test            Run tests"
	@echo "  make test-coverage   Run tests with coverage"
	@echo "  make fmt             Format code"
	@echo "  make lint            Run linter"
	@echo "  make vet             Run go vet"
	@echo "  make check           Run all checks (fmt, vet, lint)"
	@echo "  make ci              Run CI pipeline"
	@echo "  make dev             Run with hot reload (requires air)"
	@echo ""
	@echo "$(BOLD)Installation:$(NC)"
	@echo "  make install-local   Install to ~/.local/bin"
	@echo "  make install-global  Install to /usr/local/bin (requires sudo)"
	@echo "  make install-shell   Install shell integration"
	@echo "  make uninstall-local Uninstall local installation"
	@echo "  make uninstall-global Uninstall global installation"
	@echo ""
	@echo "$(BOLD)Docker:$(NC)"
	@echo "  make docker          Build Docker image"
	@echo "  make docker-push     Push Docker image to registry"
	@echo ""
	@echo "$(BOLD)Release (GoReleaser):$(NC)"
	@echo "  make goreleaser-check      Check GoReleaser configuration"
	@echo "  make goreleaser-snapshot   Build snapshot with GoReleaser"
	@echo "  make goreleaser-release    Create full release with GoReleaser"
	@echo ""
	@echo "$(BOLD)Legacy Release:$(NC)"
	@echo "  make release         Create release artifacts (legacy)"
	@echo "  make release-check   Check release prerequisites"
	@echo "  make completions     Generate shell completions"
	@echo "  make man             Generate man pages"
	@echo ""
	@echo "$(BOLD)Other:$(NC)"
	@echo "  make clean           Clean build artifacts"
	@echo "  make deps            Download dependencies"
	@echo "  make run             Build and run"
	@echo "  make help            Show this help"

# GoReleaser targets
GORELEASER_CMD := goreleaser

# Check GoReleaser configuration
goreleaser-check:
	@if command -v $(GORELEASER_CMD) >/dev/null 2>&1; then \
		echo "$(BLUE)Checking GoReleaser configuration...$(NC)"; \
		$(GORELEASER_CMD) check; \
		echo "$(GREEN)✓ GoReleaser configuration is valid$(NC)"; \
	else \
		echo "$(YELLOW)⚠ goreleaser not installed$(NC)"; \
		echo "$(BLUE)Install from: https://goreleaser.com/install/$(NC)"; \
		exit 1; \
	fi

# Build snapshot with GoReleaser (no release, for testing)
goreleaser-snapshot: clean
	@if command -v $(GORELEASER_CMD) >/dev/null 2>&1; then \
		echo "$(BLUE)Building snapshot with GoReleaser...$(NC)"; \
		$(GORELEASER_CMD) release --snapshot --clean; \
		echo "$(GREEN)✓ Snapshot built in dist/$(NC)"; \
	else \
		echo "$(YELLOW)⚠ goreleaser not installed$(NC)"; \
		echo "$(BLUE)Install from: https://goreleaser.com/install/$(NC)"; \
		exit 1; \
	fi

# Create full release with GoReleaser (requires GITHUB_TOKEN)
goreleaser-release: clean
	@if command -v $(GORELEASER_CMD) >/dev/null 2>&1; then \
		echo "$(BLUE)Creating release with GoReleaser...$(NC)"; \
		$(GORELEASER_CMD) release --clean; \
		echo "$(GREEN)✓ Release created$(NC)"; \
	else \
		echo "$(YELLOW)⚠ goreleaser not installed$(NC)"; \
		echo "$(BLUE)Install from: https://goreleaser.com/install/$(NC)"; \
		exit 1; \
	fi

# Skip publish (for testing release process)
goreleaser-release-skip-publish: clean
	@if command -v $(GORELEASER_CMD) >/dev/null 2>&1; then \
		echo "$(BLUE)Testing release process (skip publish)...$(NC)"; \
		$(GORELEASER_CMD) release --clean --skip=publish; \
		echo "$(GREEN)✓ Release test complete$(NC)"; \
	else \
		echo "$(YELLOW)⚠ goreleaser not installed$(NC)"; \
		echo "$(BLUE)Install from: https://goreleaser.com/install/$(NC)"; \
		exit 1; \
	fi
