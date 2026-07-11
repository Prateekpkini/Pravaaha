# Pravaaha — Low-Bandwidth Telemedicine Gateway

A Go-based telemedicine data gateway designed for **< 64 kbps bandwidth** and **> 20% packet loss** environments. Transmits patient vitals and medical images from field workers to a specialist hospital over a custom **UDP + Reed-Solomon FEC** protocol.

## Architecture

```
┌──────────────────────┐          UDP + FEC           ┌──────────────────────┐
│   FIELD CLIENT       │  ◄─────────────────────────► │   HOSPITAL SERVER    │
│                      │    < 64 kbps, > 20% loss     │                      │
│  ┌────────────────┐  │                              │  ┌────────────────┐  │
│  │  CLI Interface  │  │   512-byte chunks            │  │  UDP Receiver   │  │
│  └───────┬────────┘  │   4+1 Reed-Solomon FEC        │  └───────┬────────┘  │
│          │           │   NACK-only retransmission     │          │           │
│  ┌───────▼────────┐  │                              │  ┌───────▼────────┐  │
│  │ SQLite Queue   │  │                              │  │ FEC Reassembler │  │
│  │ (Store&Forward)│  │                              │  └───────┬────────┘  │
│  └───────┬────────┘  │                              │          │           │
│          │           │                              │  ┌───────▼────────┐  │
│  ┌───────▼────────┐  │                              │  │ SQLite + Disk  │  │
│  │  UDP Sender    │  │                              │  │  (Vitals+Images)│  │
│  │  (Rate Limited)│  │                              │  └───────┬────────┘  │
│  └────────────────┘  │                              │          │           │
│                      │                              │  ┌───────▼────────┐  │
│                      │                              │  │ HTTP Dashboard │  │
│                      │                              │  │   :8080        │  │
│                      │                              │  └────────────────┘  │
└──────────────────────┘                              └──────────────────────┘
```

## Key Design Decisions

| Feature | Implementation | Rationale |
|---------|---------------|-----------|
| **Serialization** | CBOR (integer keys) | ~30 bytes per vitals record, no codegen |
| **FEC** | Reed-Solomon 4+1 | ~95% of groups self-heal at 20% loss |
| **Chunk Size** | 512 bytes | Under 576-byte minimum MTU, no fragmentation |
| **Retransmission** | NACK-only | Minimizes upstream bandwidth |
| **Rate Limiting** | ~6 KB/s | Fits in 64 kbps with ACK headroom |
| **SQLite** | `modernc.org/sqlite` | Pure Go, no CGO, static binary |
| **Offline** | Store-and-forward queue | Survives total connectivity loss |

## Quick Start

### Prerequisites

- Go 1.22+
- GNU Make (or run `go build` directly)

### Build

```bash
# Build for current platform
make build-client
make build-server

# Cross-compile for Linux (AMD64 + ARM)
make build-all

# Windows
make build-windows
```

### Run

**Terminal 1 — Start the server:**
```bash
./bin/pravaaha-server -udp-port 9000 -http-port 8080
```

**Terminal 2 — Run the client with mock data:**
```bash
./bin/pravaaha-client -server 127.0.0.1 -port 9000 mock
```

**Terminal 3 — View the dashboard:**
Open `http://localhost:8080` in your browser.

### Client Commands

```bash
# Generate and queue mock vitals + test image
pravaaha-client mock

# Queue a vitals record
pravaaha-client send-vitals -patient P-1001 -hr 72 -spo2 98 -sys 120 -dia 80 -temp 36.6

# Queue an image file
pravaaha-client send-image /path/to/xray.jpg -patient P-1001

# Check queue status
pravaaha-client status
```

### Server Flags

```bash
pravaaha-server \
  -udp-port 9000 \
  -http-port 8080 \
  -db pravaaha_server.db \
  -image-dir data/images
```

## Project Structure

```
Pravaaha/
├── cmd/
│   ├── client/main.go       # Client CLI + background sender
│   └── server/main.go       # Server UDP + HTTP dashboard
├── pkg/
│   ├── protocol/
│   │   ├── messages.go      # CBOR message types & serialization
│   │   ├── fec.go           # Reed-Solomon 4+1 FEC encode/decode
│   │   ├── chunker.go       # Image chunking + FEC group reassembly
│   │   └── udp.go           # UDP transport, rate limiting, ACK/NACK
│   ├── storage/
│   │   ├── queue.go         # Client store-and-forward SQLite queue
│   │   └── server_db.go     # Server vitals + image metadata SQLite
│   └── dashboard/
│       ├── server.go        # Minimal HTTP dashboard handler
│       └── templates/
│           └── index.html   # Auto-refreshing dashboard UI
├── go.mod
├── go.sum
└── Makefile
```

## Protocol Wire Format

Every UDP datagram has the format: `[1-byte type][CBOR payload]`

| Type | Value | Direction | Description |
|------|-------|-----------|-------------|
| `TypeVitals` | 1 | Client→Server | Patient vital signs |
| `TypeChunk` | 2 | Client→Server | Image data/parity chunk |
| `TypeAck` | 3 | Server→Client | FEC group acknowledged |
| `TypeNack` | 4 | Server→Client | Unrecoverable group |
| `TypeVitalsAck` | 5 | Server→Client | Vitals receipt confirmed |

## Testing

```bash
make test
```

## License

MIT