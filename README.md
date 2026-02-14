# Skaldi

Skaldi is a self-hosting network jukebox delivered as a single Go binary. It transforms any Linux or macOS machine into a shared audio player controlled via a web interface.

![CI](https://github.com/reuski/skaldi/actions/workflows/ci.yml/badge.svg)

## Features

- **Single Binary**: Self-contained Go binary with embedded web UI
- **Auto-Provisioning**: Manages `uv`, `yt-dlp`, and `Bun`
- **Web Interface**: Mobile-first responsive frontend
- **Universal Queue**: Supports YouTube URLs and direct file uploads
- **Audio Normalization**: Real-time dynamic range compression via `dynaudnorm`
- **mDNS Discovery**: Auto-registers as `skaldi.local`
- **Live State Sync**: Real-time SSE updates

## Requirements

- **mpv**
- **ffmpeg**
- **avahi** (Linux) or **dns-sd** (macOS)

## Installation

### macOS

```bash
brew install mpv ffmpeg
```

### Linux

```bash
# Debian/Ubuntu
sudo apt install mpv ffmpeg avahi-utils

# Arch
sudo pacman -S mpv ffmpeg avahi
```

## Build

```bash
go build -o skaldi ./cmd/skaldi
```

## Run

```bash
./skaldi
```

## Architecture

### Dependencies

1. **System**: `mpv`, `ffmpeg`
2. **Managed**: `uv`, `yt-dlp`, `Bun`
3. **Embedded**: Web frontend

### Structure

```text
cmd/skaldi/
    main.go
internal/
    bootstrap/
    discovery/
    player/
    resolver/
    server/
web/
    index.html
```

### API

| Method | Path             | Description      |
| ------ | ---------------- | ---------------- |
| GET    | `/`              | Web interface    |
| GET    | `/events`        | SSE stream       |
| POST   | `/queue`         | Add URL          |
| DELETE | `/queue/{index}` | Remove item      |
| POST   | `/playback`      | Control playback |
| POST   | `/upload`        | Upload file      |
