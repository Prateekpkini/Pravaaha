# ╔══════════════════════════════════════════════════════════╗
# ║     Pravaaha — Low-Bandwidth Telemedicine Gateway       ║
# ║     Build System                                        ║
# ╚══════════════════════════════════════════════════════════╝
#
# Produces statically linked Go binaries for Linux (AMD64 + ARM).
# CGO is disabled to use the pure-Go SQLite driver (modernc.org/sqlite).
#
# Targets:
#   make build-all      — Build client + server for all platforms
#   make build-client   — Build client for current platform
#   make build-server   — Build server for current platform
#   make test           — Run all tests
#   make clean          — Remove build artifacts
#   make mock           — Run client mock data generator

# Output directory for compiled binaries.
BIN_DIR := bin

# Go build flags for small, static binaries.
# -s: omit symbol table
# -w: omit DWARF debug info
LDFLAGS := -ldflags="-s -w"

# Disable CGO for pure-Go SQLite and static linking.
export CGO_ENABLED=0

# ── Default target ──
.PHONY: all
all: build-all

# ── Build for the current platform ──
.PHONY: build-client
build-client:
	@echo "Building client..."
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/pravaaha-client ./cmd/client
	@echo "✅ $(BIN_DIR)/pravaaha-client"

.PHONY: build-server
build-server:
	@echo "Building server..."
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/pravaaha-server ./cmd/server
	@echo "✅ $(BIN_DIR)/pravaaha-server"

# ── Cross-compile for Linux AMD64 ──
.PHONY: build-client-amd64
build-client-amd64:
	@echo "Building client (linux/amd64)..."
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BIN_DIR)/pravaaha-client-linux-amd64 ./cmd/client
	@echo "✅ $(BIN_DIR)/pravaaha-client-linux-amd64"

.PHONY: build-server-amd64
build-server-amd64:
	@echo "Building server (linux/amd64)..."
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BIN_DIR)/pravaaha-server-linux-amd64 ./cmd/server
	@echo "✅ $(BIN_DIR)/pravaaha-server-linux-amd64"

# ── Cross-compile for Linux ARM (e.g., Raspberry Pi) ──
.PHONY: build-client-arm
build-client-arm:
	@echo "Building client (linux/arm)..."
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=arm GOARM=7 go build $(LDFLAGS) -o $(BIN_DIR)/pravaaha-client-linux-arm ./cmd/client
	@echo "✅ $(BIN_DIR)/pravaaha-client-linux-arm"

.PHONY: build-server-arm
build-server-arm:
	@echo "Building server (linux/arm)..."
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=arm GOARM=7 go build $(LDFLAGS) -o $(BIN_DIR)/pravaaha-server-linux-arm ./cmd/server
	@echo "✅ $(BIN_DIR)/pravaaha-server-linux-arm"

# ── Build all platform variants ──
.PHONY: build-all
build-all: build-client-amd64 build-server-amd64 build-client-arm build-server-arm
	@echo ""
	@echo "══════════════════════════════════════════════"
	@echo "  All binaries built successfully:"
	@ls -lh $(BIN_DIR)/
	@echo "══════════════════════════════════════════════"

# ── Windows builds (for local development) ──
.PHONY: build-windows
build-windows:
	@echo "Building for Windows..."
	@mkdir -p $(BIN_DIR)
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BIN_DIR)/pravaaha-client.exe ./cmd/client
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BIN_DIR)/pravaaha-server.exe ./cmd/server
	@echo "✅ Windows builds complete"

# ── Tests ──
.PHONY: test
test:
	@echo "Running tests..."
	go test -v -count=1 -race ./pkg/...
	@echo "✅ All tests passed"

# ── Run mock data generator ──
.PHONY: mock
mock: build-client
	$(BIN_DIR)/pravaaha-client mock

# ── Dependency management ──
.PHONY: deps
deps:
	go mod tidy
	go mod download

# ── Clean ──
.PHONY: clean
clean:
	@echo "Cleaning..."
	rm -rf $(BIN_DIR)
	rm -f pravaaha_client.db pravaaha_server.db
	rm -rf data/
	@echo "✅ Clean"

# ── Help ──
.PHONY: help
help:
	@echo ""
	@echo "Pravaaha Telemedicine Gateway — Build Targets"
	@echo "═══════════════════════════════════════════════"
	@echo "  build-all          Build all platform binaries"
	@echo "  build-client       Build client (current platform)"
	@echo "  build-server       Build server (current platform)"
	@echo "  build-client-amd64 Build client for Linux AMD64"
	@echo "  build-server-amd64 Build server for Linux AMD64"
	@echo "  build-client-arm   Build client for Linux ARM"
	@echo "  build-server-arm   Build server for Linux ARM"
	@echo "  build-windows      Build both for Windows"
	@echo "  test               Run all tests"
	@echo "  mock               Build & run mock data generator"
	@echo "  deps               Tidy & download Go dependencies"
	@echo "  clean              Remove build artifacts"
	@echo "  help               Show this message"
	@echo ""
