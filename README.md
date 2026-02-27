# Skaldi

![CI](https://github.com/reuski/skaldi/actions/workflows/ci.yml/badge.svg) ![Release](https://img.shields.io/github/v/release/reuski/skaldi?style=flat-square) ![Go Report Card](https://goreportcard.com/badge/github.com/reuski/skaldi?style=flat-square)

Self-hosting network jukebox. Single Go binary, web UI, auto-provisions dependencies.

## Install

```bash
# macOS
brew install mpv ffmpeg

# Arch
sudo pacman -S mpv ffmpeg avahi

# Debian/Ubuntu
sudo apt install mpv ffmpeg avahi-utils
```

## Build & Run

```bash
go build -o skaldi ./cmd/skaldi
./skaldi
```

First run auto-installs `uv`, `bun`, `yt-dlp` to `~/.cache/skaldi/`.

## Features

- **Queue**: YouTube URLs, direct file uploads (drag-drop or paste)
- **Search**: YouTube + YouTube Music with autocomplete
- **Sync**: Real-time SSE state updates
- **Discovery**: Auto-registers as `skaldi.local` via mDNS
- **Audio**: Dynamic range compression via `dynaudnorm`

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/` | Web UI |
| GET | `/events` | SSE stream |
| GET | `/suggest?q={query}` | Autocomplete suggestions |
| GET | `/search?q={query}` | Search YouTube/Music |
| POST | `/queue` | Add URL `{"url":"..."}` |
| DELETE | `/queue/{index}` | Remove item |
| POST | `/playback` | Control `{"action":"pause|resume|skip|previous|play"}` |
| POST | `/upload` | File upload (multipart/form-data) |

## Security

No authentication. Exposing to the internet allows arbitrary uploads and RCE via media parsers. Run only on trusted networks.

## Development

```bash
just all    # lint, test, build
just lint   # gofmt, golangci-lint, go vet
just test   # go test -v -race ./internal/...
just vuln   # govulncheck
```

See [`AGENTS.md`](./AGENTS.md) for architecture guidelines.

## License

AGPL-3.0 - See [LICENSE](./LICENSE)
