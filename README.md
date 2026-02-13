# Skaldi — Network Jukebox

Skaldi is a self-hosted network jukebox in a single Go binary. It turns any Linux or macOS machine into a shared audio player that users control via a web interface on their local network.

It automatically provisions its own complex runtime dependencies (yt-dlp, Python, JavaScript runtime) and manages an `mpv` subprocess for playback, ensuring a consistent, zero-config experience.

## Features

- **Single Binary:** No complex install scripts. The binary manages its own environment.
- **Auto-Provisioning:** Automatically fetches and configures `uv` (for Python tools), `yt-dlp`, and `Bun` (for fast YouTube JS execution).
- **Web Interface:** Embedded, mobile-friendly frontend for queuing tracks from any device on the LAN.
- **Universal Queue:** Supports YouTube URLs and direct file uploads.
- **Audio Normalization:** Real-time dynamic normalization to balance volume between different sources.
- **Shim Architecture:** Transparently handles yt-dlp's runtime requirements without user intervention.

## Prerequisites

Skaldi requires the following audio playback tools to be installed on the host system:

- **mpv** (Audio engine)
- **ffmpeg / ffprobe** (Decoding and probing)

### Installation

**macOS**
```bash
brew install mpv ffmpeg
```

**Linux (Debian/Ubuntu)**
```bash
sudo apt install mpv ffmpeg
```

**Linux (Arch)**
```bash
sudo pacman -S mpv ffmpeg
```

## Getting Started

### Building from Source

Skaldi is written in Go and requires no external Go modules.

```bash
# Clone the repository
git clone https://github.com/reuski/skaldi.git
cd skaldi

# Build the binary
go build -o skaldi ./cmd/jukebox
```

### Running

Simply run the binary:

```bash
./skaldi
```

On the first run, Skaldi will perform a provisioning sequence (fetching uv, Bun, and installing yt-dlp). This may take a minute. Once ready, it will display the local network URL:

```
[INFO] Jukebox ready. Listening at http://192.168.1.50:8080
```

Open this URL on any device connected to the same network to start queuing music.

## Architecture

Skaldi uses a unique 3-tier dependency model:

1.  **System Tier:** `mpv` and `ffmpeg` (provided by user/OS).
2.  **Managed Tier:** `uv`, `yt-dlp`, and `Bun`. Skaldi downloads these into a private cache directory (`~/.cache/jukebox` on Linux, `~/Library/Caches/jukebox` on macOS) and links them together using a custom shim.
3.  **Embedded Tier:** The web frontend is compiled directly into the Go binary.

### The Shim
To handle YouTube's modern JavaScript challenges efficiently, Skaldi generates a wrapper script (shim) that forces `yt-dlp` to use the managed `Bun` runtime. This provides significantly faster startup times compared to standard Node.js or Deno configurations, without requiring the user to configure paths manually.

## Platform Support

- **macOS:** AMD64 (Intel), ARM64 (Apple Silicon)
- **Linux:** AMD64, ARM64 (Raspberry Pi compatible)
