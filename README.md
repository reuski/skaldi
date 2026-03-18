# Skaldi

![CI](https://github.com/reuski/skaldi/actions/workflows/ci.yml/badge.svg) ![Release](https://img.shields.io/github/v/release/reuski/skaldi?style=flat-square) ![Go Report Card](https://goreportcard.com/badge/github.com/reuski/skaldi?style=flat-square)

Skaldi is a self-hosted network jukebox: one Go binary, one embedded web UI, no external Go dependencies.

It runs `mpv` locally, exposes a browser UI on your LAN, and provisions `uv`, `bun`, and `yt-dlp` into your cache directory on first run.

## Features

- Queue URLs or upload local files
- Search YouTube and YouTube Music
- Optional OpenSubsonic library search
- Real-time state sync over SSE
- Queue reordering, history, volume, and mute controls
- mDNS advertising at `skaldi.local` when available

## Requirements

- `mpv`
- `ffmpeg`
- Go `1.26+` to build from source

Install the system packages first:

```bash
# macOS
brew install mpv ffmpeg

# Arch
sudo pacman -S mpv ffmpeg avahi

# Debian/Ubuntu
sudo apt install mpv ffmpeg avahi-utils
```

## Quick Start

```bash
go build -o skaldi ./cmd/skaldi
./skaldi
```

Skaldi listens on `http://localhost:8080` and also logs a LAN URL on startup. The first run needs network access to provision `uv`, `bun`, and `yt-dlp` under `~/.cache/skaldi/`.

## OpenSubsonic

OpenSubsonic is optional. If you want it, create `~/.config/skaldi/config.json` or `${XDG_CONFIG_HOME}/skaldi/config.json`:

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

If the config is missing or disabled, Skaldi starts normally without OpenSubsonic. If the config is invalid, Skaldi disables that source and logs a warning.

## Development

```bash
just all
just lint
just test
just build
just vuln
just release-build
```

`just release-build` produces the standard release artifacts plus separate macOS 11 legacy Darwin binaries built through `go.legacy.mod`.

## Security

Skaldi is designed for trusted networks. There is no authentication, and exposing it directly to the internet is unsafe.

## License

AGPL-3.0. See [LICENSE](./LICENSE).
