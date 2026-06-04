# Skaldi — Agent Guide

Network jukebox. Single Go binary, embedded web UI.

## Principles

- Zero external Go deps — stdlib only.
- Self-contained — one binary, embedded UI.
- Performance and UX — stream useful results first; fail provisioning on runtime regressions.
- Provisioning: fail fast (fatal on missing deps).
- Runtime: recover gracefully (auto-restart, reconnect).

## Stack

**Backend** — Go 1.26, stdlib only.
- `net/http`, `log/slog`, `os/exec`.
- Channels for state mutation; `sync.RWMutex` for shared reads.
- `context.Context` on every long-running operation.

**Frontend** — `web/index.html`, embedded via `//go:embed`.
- Vanilla ES6, CSS variables, no build step.
- `fetch` for commands, `EventSource` for state.

**Managed runtime deps** (provisioned to `~/.cache/skaldi/bin/`)
- `yt-dlp` — media resolver.
- `bun` — JS runtime for yt-dlp signature/nsig challenges.

**System deps** (from `PATH`)
- `mpv`, `ffmpeg`, `avahi` (Linux mDNS).

## Config & env

| Setting | Env | `config.json` | Default |
|---|---|---|---|
| Listen port | `SKALDI_PORT` | `server.port` | `8080` |
| Config path | `SKALDI_CONFIG` | — | `$XDG_CONFIG_HOME/skaldi/config.json` |
| Provision | `SKALDI_PROVISION=0` | `provision: false` | on |

`SKALDI_PROVISION=0` skips downloads; resolves `yt-dlp`/`bun` from `PATH` and writes the shim against them. Used by the Nix package (`flake.nix`, `nix/`).

## Structure

```
cmd/skaldi/main.go
internal/
    bootstrap/   # provisioning (yt-dlp, bun), config
    discovery/   # mDNS registration
    history/     # per-session JSONL playback logs
    player/      # mpv process & IPC
    resolver/    # yt-dlp metadata extraction
    server/      # HTTP handlers & SSE
web/
    fs.go        # embed directive
    index.html   # single-page UI
```

## Commands

```bash
just all      # lint, test, build
just build    # go build
just test     # go test -v -race ./internal/...
just lint     # gofmt-check, golangci-lint, go vet
just vuln     # govulncheck
```

## Invariants

1. **Shim** — mpv never calls yt-dlp directly; it calls the generated `bin/yt-dlp`.
2. **Source of truth** — mpv's internal playlist is master state. Mirror via IPC; never predict.
3. **Idempotency** — check existence/version before downloading in bootstrap.
4. **Lint** — every error handled or explicitly ignored (`_ = err`).
5. **Paths** — absolute paths for `bin/`; `path/filepath` for cross-platform joins.

## Patterns

**Add IPC command**
1. Wrapper in `internal/player/ipc.go`.
2. Method in `internal/player/mpv.go`.
3. Handler in `internal/server/handlers.go`.

**State**
- Mutate via channels (actor model).
- SSE: snapshot to new clients, deltas to existing.
- Metadata cache pruned after 5 min.

**Errors**
- Provisioning → `log.Fatal` on missing prerequisites.
- Runtime → retry with backoff, never crash.
