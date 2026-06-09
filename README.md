# Skaldi

![CI](https://github.com/reuski/skaldi/actions/workflows/ci.yml/badge.svg) ![Release](https://img.shields.io/github/v/release/reuski/skaldi?style=flat-square) ![Go Report Card](https://goreportcard.com/badge/github.com/reuski/skaldi?style=flat-square)

Self-hosted network jukebox. One Go binary, one embedded web UI, zero external Go dependencies.

Runs `mpv` locally, serves a browser UI on your LAN, and provisions self-contained `yt-dlp` + `bun` runtimes into `~/.cache/skaldi/` on first run. No system Python or package manager required.

## Features

- Queue URLs or upload local files
- Search YouTube and YouTube Music
- Optional OpenSubsonic library search
- Real-time state sync over SSE
- Queue reordering, history, volume, mute
- mDNS advertising at `skaldi.local`

## Requirements

- `mpv`, `ffmpeg`
- Go `1.26+` to build from source

```bash
brew install mpv ffmpeg                  # macOS
sudo pacman -S mpv ffmpeg avahi          # Arch
sudo apt install mpv ffmpeg avahi-utils  # Debian/Ubuntu
```

## Quick Start

```bash
go build -o skaldi ./cmd/skaldi
./skaldi
```

Listens on `http://localhost:8080` (LAN URL logged on startup). First run needs network access to provision `yt-dlp` + `bun`.

## CLI

```bash
skaldi
skaldi version
skaldi update
```

## Health endpoint

`GET /health` returns JSON for uptime probes and dashboards:

```json
{ "status": "ok", "version": "v1.5.4", "playback": "playing", "now_playing": "…", "queue": 4 }
```

## Configuration

Env overrides config; config overrides defaults. Config at `$XDG_CONFIG_HOME/skaldi/config.json` (or `SKALDI_CONFIG`).

| Setting | Env | `config.json` | Default |
|---|---|---|---|
| Listen port | `SKALDI_PORT` | `server.port` | `8080` |
| Provision | `SKALDI_PROVISION=0` | `provision: false` | on |

**Provisioning bypass** — set `SKALDI_PROVISION=0` and put `yt-dlp` + `bun` on `PATH` (alongside `mpv`, `ffmpeg`). Skaldi uses them directly and skips all downloads.

## NixOS

Flake ships a self-contained package and a NixOS module. The packaged binary is wrapped with `mpv`, `ffmpeg`, `yt-dlp`, `bun`, `avahi` from the store and runs with `SKALDI_PROVISION=0` — never downloads anything.

```bash
nix run github:reuski/skaldi
```

```nix
{
  inputs.skaldi.url = "github:reuski/skaldi";

  # in your nixosConfiguration modules:
  imports = [ skaldi.nixosModules.default ];

  services.skaldi = {
    enable = true;
    openFirewall = true;
    settings = {
      server.port = 8080;
      # opensubsonic = { enabled = true; library_id = "personal"; ... };
    };
  };

  services.avahi.enable = true; # for skaldi.local
}
```

Runs under a hardened `DynamicUser`, joined to the `audio` group for ALSA. PipeWire/PulseAudio may need extra wiring — override `systemd.services.skaldi.environment` (e.g. `PULSE_SERVER`) or `serviceConfig.SupplementaryGroups`.

## OpenSubsonic

Optional. Search a Subsonic/OpenSubsonic library (e.g. Navidrome) alongside YouTube.
Enable via `config.json`:

```json
{
  "opensubsonic": {
    "enabled": true,
    "library_id": "personal",
    "base_url": "https://navidrome.example.com",
    "username": "alice",
    "token_file": "/run/secrets/navidrome-password",
    "timeout_ms": 2500
  }
}
```

`token_file` points at a file containing the user's Subsonic password. If the
file is missing/empty or any field is incomplete, OpenSubsonic is silently disabled.

## Security

Designed for trusted networks. No authentication — do not expose directly to the internet.

## License

AGPL-3.0. See [LICENSE](./LICENSE).
