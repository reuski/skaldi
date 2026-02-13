# Skaldi

Skaldi is a self-hosting network jukebox delivered as a single Go binary. It transforms any Linux or macOS machine into a shared audio player controlled via a web interface accessible to devices on the local network.

The application automatically provisions and manages its runtime dependencies (yt-dlp, uv, Bun) and controls an `mpv` subprocess for audio playback, requiring zero manual configuration beyond system prerequisites.

## Features

- **Single Binary Deployment**: Self-contained Go binary with embedded web UI
- **Auto-Provisioning**: Automatically fetches and configures `uv` (Python package manager), `yt-dlp`, and `Bun` (JavaScript runtime)
- **Web Interface**: Mobile-first responsive frontend served via HTTP
- **Universal Queue**: Supports YouTube URLs and direct file uploads (up to 100MB)
- **Audio Normalization**: Real-time dynamic range compression via mpv's `dynaudnorm` filter
- **mDNS Discovery**: Auto-registers as `skaldi.local` on supported networks
- **Live State Sync**: Real-time playback state via Server-Sent Events (SSE)

## Prerequisites

System dependencies (must be installed manually):

- **mpv** (audio playback engine)
- **ffmpeg/ffprobe** (media decoding)
- **avahi-publish** (Linux mDNS, optional)
- **dns-sd** (macOS mDNS, built-in)

### Installation

**macOS**
```bash
brew install mpv ffmpeg
```

**Linux (Debian/Ubuntu)**
```bash
sudo apt install mpv ffmpeg avahi-utils
```

**Linux (Arch)**
```bash
sudo pacman -S mpv ffmpeg avahi
```

## Building

Skaldi uses Go 1.23+ with zero external dependencies.

```bash
go build -o skaldi ./cmd/skaldi
```

## Running

```bash
./skaldi
```

First run triggers provisioning (downloading uv, Bun, installing yt-dlp). Once ready:

```
[INFO] Skaldi ready at http://skaldi.local:8080
```

## Architecture

### Three-Tier Dependency Model

1. **System Tier**: `mpv` and `ffmpeg` (user-provided)
2. **Managed Tier**: `uv`, `yt-dlp`, `Bun` (auto-downloaded to cache)
3. **Embedded Tier**: Web frontend compiled into binary

### Directory Structure

```
cmd/skaldi/
    main.go              # Application entry point
internal/
    bootstrap/           # Provisioning and dependency management
        config.go        # Cache paths and configuration
        extract.go       # Archive extraction (tar.gz, zip)
        fetch.go         # HTTP download utilities
        latest.go        # GitHub API version fetching
        platform.go      # OS/architecture detection
        preflight.go     # Prerequisite checks (mpv, ffmpeg)
        provision.go     # Main provisioning orchestration
        state.go         # Version state persistence
    discovery/           # mDNS service registration
        mdns.go          # Avahi (Linux) / Bonjour (macOS)
    player/              # mpv integration
        events.go        # IPC event handling
        ipc.go           # Unix socket IPC client
        mpv.go           # Process management and lifecycle
        state.go         # In-memory playback state
    resolver/            # URL resolution
        ytdlp.go         # yt-dlp metadata extraction
    server/              # HTTP API and SSE
        handlers.go      # REST endpoints
        server.go        # HTTP server setup
        sse.go           # Server-Sent Events broadcaster
web/
    fs.go                # Embedded file system
    index.html           # Single-page frontend
```

### Critical Invariants

**The Shim**: `mpv` must NEVER invoke `yt-dlp` directly. A generated shell script (`bin/yt-dlp`) wraps the real yt-dlp and forces Bun as the JavaScript runtime via `--js-runtimes`. This ensures consistent performance without requiring Node.js.

**IPC Source of Truth**: The `mpv` internal playlist is authoritative. The Go application mirrors state via IPC property observation (`observe_property`) rather than predicting state.

**Idempotency**: Provisioning checks existing versions before downloading. Cache invalidation triggers re-download.

### Cache Layout

```
~/.cache/skaldi/          # Linux
~/Library/Caches/skaldi/  # macOS
├── bin/
│   ├── uv                # Python package manager
│   ├── bun               # JavaScript runtime
│   └── yt-dlp            # Shim script (not the real binary)
├── uv-bin/
│   └── yt-dlp            # Actual yt-dlp installed by uv
├── versions.json         # Installed version state
└── mpv.sock              # mpv IPC socket
```

### Platform Support

| Platform | Architecture | Status |
|----------|-------------|--------|
| Linux    | amd64       | Supported |
| Linux    | arm64       | Supported (Raspberry Pi) |
| macOS    | amd64       | Supported (Intel) |
| macOS    | arm64       | Supported (Apple Silicon) |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/` | Serve web interface |
| GET | `/events` | SSE stream for state updates |
| POST | `/queue` | Add YouTube URL to queue |
| DELETE | `/queue/{index}` | Remove item from queue |
| POST | `/playback` | Control playback (play/pause/skip/previous) |
| POST | `/upload` | Upload local audio file |

## Development

### Testing

```bash
go test ./internal/...
```

### Code Conventions

- **Go**: Pass `context.Context` to all long-running operations
- **Frontend**: Vanilla ES6, no build step, CSS variables for theming
- **Error Handling**: Fail fast during provisioning, recover gracefully during runtime
- **Logging**: Structured logging via `log/slog`

### Adding IPC Commands

1. Add command wrapper in `internal/player/ipc.go`
2. Expose method in `internal/player/mpv.go`
3. Add handler in `internal/server/handlers.go`
