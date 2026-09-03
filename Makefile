# ActionD Makefile
# Build and release automation with ldflags version injection.

VERSION ?= $(shell sed -n 's/^[[:space:]]*var[[:space:]][[:space:]]*Version[[:space:]]*= "\(.*\)"/\1/p' internal/version/version.go)
BUILD_DATE := $(shell date +%Y-%m-%d)
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
# -X must target internal/version.Version (a constant-initialized var); the
# main package copies it at init, so injecting main.Version would be a no-op.
LDFLAGS := -s -w -X github.com/JoeGlenn1213/actiond/internal/version.Version=$(VERSION) -X main.BuildDate=$(BUILD_DATE) -X main.GitCommit=$(GIT_COMMIT)
# SQLite is the pure-Go modernc.org/sqlite build; CGO is not required and
# would block cross-compilation (no cgo cross toolchains on default hosts).
CGO_ENABLED ?= 0

BINARY_NAME := actiond
DIST_DIR := dist
HOST_PLATFORM := $(shell go env GOOS)/$(shell go env GOARCH)
RELEASE_PLATFORMS ?= $(HOST_PLATFORM)

.PHONY: all build clean test lint release

all: build

build:
	@echo "Building ActionD v$(VERSION)..."
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=$(CGO_ENABLED) go build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME) ./cmd/actiond/
	@echo "✓ Built: $(DIST_DIR)/$(BINARY_NAME)"

release: clean
	@echo "Building ActionD v$(VERSION) release artifacts..."
	@echo "Using CGO_ENABLED=$(CGO_ENABLED)"
	@echo "Target platforms: $(RELEASE_PLATFORMS)"
	@if [ "$(CGO_ENABLED)" = "1" ] && [ "$(RELEASE_PLATFORMS)" != "$(HOST_PLATFORM)" ]; then \
		echo "error: CGO-enabled release builds default to the host platform ($(HOST_PLATFORM))."; \
		echo "set RELEASE_PLATFORMS=$(HOST_PLATFORM) or provide a compatible cross-compilation toolchain."; \
		exit 1; \
	fi
	@mkdir -p $(DIST_DIR)
	@for platform in $(RELEASE_PLATFORMS); do \
		GOOS=$$(echo $$platform | cut -d/ -f1); \
		GOARCH=$$(echo $$platform | cut -d/ -f2); \
		output=$(DIST_DIR)/$(BINARY_NAME)-$(VERSION)-$$GOOS-$$GOARCH; \
		if [ "$$GOOS" = "windows" ]; then output=$$output.exe; fi; \
		echo "Building $$output..."; \
		CGO_ENABLED=$(CGO_ENABLED) GOOS=$$GOOS GOARCH=$$GOARCH go build -ldflags="$(LDFLAGS)" -o $$output ./cmd/actiond/; \
	done
	@echo "✓ Release builds complete"

test:
	@echo "Running tests..."
	go test ./... -v

lint:
	@echo "Linting..."
	golangci-lint run ./...

clean:
	@echo "Cleaning..."
	@rm -rf $(DIST_DIR)
	@go clean
	@echo "✓ Clean complete"
