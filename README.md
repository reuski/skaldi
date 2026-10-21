# Skaldi

![CI](https://github.com/reuski/skaldi/actions/workflows/ci.yml/badge.svg) ![Release](https://img.shields.io/github/v/release/reuski/skaldi?style=flat-square) ![Go Report Card](https://goreportcard.com/badge/github.com/reuski/skaldi?style=flat-square)

Self-hosted network jukebox: one Go binary, one embedded web UI, zero external Go dependencies. `mpv` plays locally; browsers on your LAN control the queue.

Supported sources are YouTube and YouTube Music links and search, tracks from a configured OpenSubsonic library, and local file uploads. Other URLs are rejected. Queue reordering, history, volume, mute, SSE state sync and mDNS (`skaldi.local`) are built in.

## Requirements

- `mpv`, `ffmpeg` (and `avahi` for mDNS on Linux)
- Go `1.27.1+` to build from source

```bash
brew install mpv ffmpeg                  # macOS
sudo pacman -S mpv ffmpeg avahi          # Arch
sudo apt install mpv ffmpeg avahi-utils  # Debian/Ubuntu
```

CI and releases resolve the latest Go patch release. The macOS 11 amd64 release artifact is the one exception: `just release-build-legacy-darwin` builds it with the last compatible toolchain (Go 1.24) from `go.legacy.mod`.

## Quick start

Build with `go build -o skaldi ./cmd/skaldi` and run `./skaldi`. It listens on `http://localhost:8080` and logs the LAN URL at startup; the first run downloads the managed runtimes, so it needs network access. `skaldi version` and `skaldi update` are also available, and `GET /health` returns JSON with `status`, `version`, `playback`, `now_playing` and `queue`.

## Configuration

Env overrides `config.json`; `config.json` overrides defaults. The file lives at `$XDG_CONFIG_HOME/skaldi/config.json` or `$SKALDI_CONFIG`.

| Setting     | Env                  | `config.json`      | Default |
| ----------- | -------------------- | ------------------ | ------- |
| Listen port | `SKALDI_PORT`        | `server.port`      | `8080`  |
| Provision   | `SKALDI_PROVISION=0` | `provision: false` | on      |

With `SKALDI_PROVISION=0`, `yt-dlp` and `bun` must be on `PATH` (next to `mpv`/`ffmpeg`). Skaldi shims them directly, downloads nothing, and leaves their versions to your package manager.

## Managed runtimes

| Runtime  | Upstream                       | Channel               |
| -------- | ------------------------------ | --------------------- |
| `yt-dlp` | `yt-dlp/yt-dlp-nightly-builds` | nightly               |
| `bun`    | `oven-sh/bun`                  | latest normal release |

Both install into `~/.cache/skaldi/bin/`. Nightly `yt-dlp` is upstream's recommendation because streaming-site changes break stable first; expect the occasional ~40 MB download. Startup checks each upstream once and independently — a failure on one never hides a new version of the other. There is no background updater and no configurable source.

If a check or download fails because the network or upstream is unavailable, Skaldi falls back to the last check recorded for the same channel, then to the runtime recorded in `versions.json`. A reused runtime is re-verified before use: `yt-dlp` must keep its unpacked layout and pass a startup `--version` performance check, `bun` must pass a bounded `--version` probe. Startup logs a warning because the latest version is no longer guaranteed.

Provisioning fails fast when nothing usable remains: no recorded runtime, a missing or broken binary, a failed validation, a corrupt archive, a malformed release record, or a version that cannot be recorded. A failed update never advances the recorded version.

## NixOS

The flake ships a package and a NixOS module, runnable with `nix run github:reuski/skaldi`. The packaged binary is wrapped with `mpv`, `ffmpeg`, `yt-dlp`, `bun` and `avahi` from the store and runs with `SKALDI_PROVISION=0`, so it never downloads anything at runtime. The package uses nixpkgs' latest Go selectors; `flake.lock` pins a reproducible `nixos-unstable` snapshot that is refreshed by daily update PRs (`nix flake update` locally).

```nix
{
  inputs.skaldi.url = "github:reuski/skaldi";
  imports = [ skaldi.nixosModules.default ];

  services.skaldi = {
    enable = true;
    openFirewall = true;
    settings.server.port = 8080;
  };
  services.avahi.enable = true; # skaldi.local
}
```

The service runs under a hardened `DynamicUser` in the `audio` group. PipeWire/PulseAudio may need `systemd.services.skaldi.environment` (e.g. `PULSE_SERVER`) or `serviceConfig.SupplementaryGroups`.

## OpenSubsonic

Optional. Searches a Subsonic/OpenSubsonic library such as Navidrome alongside YouTube.

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

`token_file` holds the Subsonic password. If it is missing/empty or any field is incomplete, OpenSubsonic stays silently disabled.

## Security

Built for trusted networks: no authentication, and the HTTP server listens on all interfaces. Do not expose it directly to the internet; keep `token_file` readable only by the service user.

## License

AGPL-3.0. See [LICENSE](./LICENSE).
