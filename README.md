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

Requires Go 1.26 or later.

Primary CI and release builds track the latest stable Go automatically.

```bash
go build -o skaldi ./cmd/skaldi
./skaldi
```

First run auto-installs `uv`, `bun`, `yt-dlp` to `~/.cache/skaldi/`.

## macOS Compatibility

Mainline builds use the latest stable Go toolchain and support macOS 12 Monterey or newer.

Release builds also publish separate legacy assets for macOS 11 Big Sur:

- `skaldi-darwin-amd64-macos11`
- `skaldi-darwin-arm64-macos11`

Those legacy darwin assets are built with Go 1.24.13 only during the release build path, so day-to-day development and CI stay on the latest Go.

The legacy darwin recipe builds against `go.legacy.mod`, which keeps the compatibility floor separate from the primary `go.mod`.

## Features

- **Queue**: YouTube URLs, direct file uploads (drag-drop or paste)
- **Search**: YouTube + YouTube Music with autocomplete
- **External Library**: OpenSubsonic personal library integration
- **Sync**: Real-time SSE state updates
- **Discovery**: Auto-registers as `skaldi.local` via mDNS
- **Audio**: Dynamic range compression via `dynaudnorm`

## Config File (OpenSubsonic)

Create `config.json` at:

- Linux/macOS default: `~/.config/skaldi/config.json`
- Or `${XDG_CONFIG_HOME}/skaldi/config.json`

Example:

```json
{
  "opensubsonic": {
    "enabled": true,
    "library_id": "personal",
    "base_url": "https://navidrome.example.com",
    "username": "alice",
    "token": "server_token_secret",
    "timeout_ms": 2500
  }
}
```

Notes:

- Missing/empty config disables external search silently.
- Invalid enabled config fails fast on startup.
- External cover art is deferred in v1; placeholder thumbnails are shown.

## API

| Method | Path                               | Description                                              |
| ------ | ---------------------------------- | -------------------------------------------------------- |
| GET    | `/`                                | Web UI                                                   |
| GET    | `/events`                          | SSE stream                                               |
| GET    | `/suggest?q={query}`               | Autocomplete suggestions                                 |
| GET    | `/search?q={query}&mode=typeahead` | Text suggestions + external track hits                   |
| GET    | `/search?q={query}&mode=full`      | Merged track search (OpenSubsonic + YT Music + YouTube) |
| POST   | `/queue`                           | Add URL `{"url":"..."}`                                  |
| DELETE | `/queue/{index}`                   | Remove item                                              |
| POST   | `/playback`                        | Control `{"action":"pause\\|resume\\|skip\\|previous\\|play"}` |
| POST   | `/upload`                          | File upload (multipart/form-data)                        |

`/search` response shape:

```json
{
  "suggestions": ["query text", "another"],
  "tracks": []
}
```

## Security

No authentication. Exposing to the internet allows arbitrary uploads and RCE via media parsers. Run only on trusted networks.

## Development

```bash
just all    # lint, test, build
just lint   # gofmt, golangci-lint, go vet
just test   # go test -v -race ./internal/...
just vuln   # govulncheck
just release-build  # latest artifacts + legacy macOS 11 darwin artifacts
```

See [`AGENTS.md`](./AGENTS.md) for architecture guidelines.

## License

AGPL-3.0 - See [LICENSE](./LICENSE)
