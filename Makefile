BIN?=claudex
INSTALL_DIR?=/usr/local/bin

.PHONY: all build image clean install uninstall test test-integration

all: build image

# Build the CLI binary
build:
	go mod tidy
	go build -o $(BIN) ./cmd/claudex

# Build/update the Docker image
image: build
	./$(BIN) build

# Run unit tests for library/CLI packages (skip root package)
test-unit:
	go test ./internal/... ./cmd/...

# Run integration tests (requires Docker and environment setup)
test-integration:
	go test -tags=integration ./...

# Full test run, including root package
test:
	go test ./...

# Force rebuild the Docker image directly
rebuild-image:
	docker build --no-cache -t claudex .

# Install binary to system directory
install: build
	@echo "Installing $(BIN) to $(INSTALL_DIR)"
	@if [ -w "$(INSTALL_DIR)" ]; then \
		cp $(BIN) $(INSTALL_DIR)/; \
	else \
		sudo cp $(BIN) $(INSTALL_DIR)/; \
	fi
	@echo "✅ Installed $(BIN) to $(INSTALL_DIR)"

# Uninstall binary from system directory
uninstall:
	@echo "Removing $(BIN) from $(INSTALL_DIR)"
	@if [ -w "$(INSTALL_DIR)" ]; then \
		rm -f $(INSTALL_DIR)/$(BIN); \
	else \
		sudo rm -f $(INSTALL_DIR)/$(BIN); \
	fi
	@echo "✅ Removed $(BIN) from $(INSTALL_DIR)"

# Clean up build artifacts
clean:
	rm -f $(BIN)
	docker rmi claudex 2>/dev/null || true
