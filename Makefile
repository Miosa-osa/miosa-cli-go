## sdks/cli/Makefile — miosa CLI build, test, install
MODULE     := github.com/Miosa-osa/miosa-cli-go
BINARY     := miosa
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS    := -X '$(MODULE)/commands.cliVersion=$(VERSION)'
BUILD_DIR  := dist

.PHONY: build build-all test lint install clean integration help

## ─── Help ────────────────────────────────────────────────────────────────────
help:
	@echo "miosa CLI targets"
	@echo ""
	@echo "  build       Build for the current platform → dist/miosa"
	@echo "  build-all   Cross-compile for darwin/linux × amd64/arm64"
	@echo "  test        Run unit tests"
	@echo "  integration Run integration tests (requires MIOSA_API_KEY)"
	@echo "  lint        Run go vet + staticcheck"
	@echo "  install     Copy dist/miosa → ~/.local/bin/miosa"
	@echo "  clean       Remove dist/"

## ─── Build ───────────────────────────────────────────────────────────────────
build:
	@mkdir -p $(BUILD_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/miosa

build-all:
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin  GOARCH=amd64  go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-darwin-amd64  ./cmd/miosa
	GOOS=darwin  GOARCH=arm64  go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-darwin-arm64  ./cmd/miosa
	GOOS=linux   GOARCH=amd64  go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-amd64   ./cmd/miosa
	GOOS=linux   GOARCH=arm64  go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-arm64   ./cmd/miosa
	@echo "Binaries in $(BUILD_DIR)/"

## ─── Test ────────────────────────────────────────────────────────────────────
test:
	go test -race -v ./...

integration:
	@if [ -z "$(MIOSA_API_KEY)" ]; then \
		echo "MIOSA_API_KEY not set — skipping integration tests"; \
		exit 0; \
	fi
	go test -race -v -tags integration ./...

## ─── Lint ────────────────────────────────────────────────────────────────────
lint:
	go vet ./...
	@if command -v staticcheck >/dev/null 2>&1; then \
		staticcheck ./...; \
	else \
		echo "staticcheck not installed (go install honnef.co/go/tools/cmd/staticcheck@latest)"; \
	fi

## ─── Install ─────────────────────────────────────────────────────────────────
install: build
	@mkdir -p $(HOME)/.local/bin
	cp $(BUILD_DIR)/$(BINARY) $(HOME)/.local/bin/$(BINARY)
	@echo "Installed $(HOME)/.local/bin/$(BINARY)"
	@echo "Make sure $(HOME)/.local/bin is in your PATH."

## ─── Clean ───────────────────────────────────────────────────────────────────
clean:
	rm -rf $(BUILD_DIR)
