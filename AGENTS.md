# Skaldi

Development guidelines for the Skaldi network jukebox.

## Philosophy

- **Zero external Go dependencies** - stdlib only
- **Self-contained** - single binary, embedded web UI
- **Fail fast during provisioning** - fatal errors on missing deps
- **Recover gracefully at runtime** - auto-restart, reconnect

## Stack

**Backend (Go 1.23)**
- Stdlib: `net/http`, `log/slog`, `os/exec`
- Concurrency: channels for state, `sync.RWMutex` for shared data
- Contexts required for all long-running operations

**Frontend**
- Single file: `web/index.html` via `//go:embed`
- Vanilla ES6, CSS variables, no build step
- `fetch` for commands, `EventSource` for state

**Managed Runtime Dependencies**
- `uv` - Python package manager
- `bun` - JS runtime for yt-dlp
- `yt-dlp` - Media resolver (installed via uv)
- `mpv` + `ffmpeg` - System dependencies

## Structure

```
cmd/skaldi/
    main.go
internal/
    bootstrap/    # Provisioning (uv, bun, yt-dlp)
    discovery/    # mDNS service registration
    player/       # mpv process & IPC
    resolver/     # yt-dlp metadata extraction
    server/       # HTTP handlers & SSE
web/
    fs.go         # Embed directive
    index.html    # Single-page UI
```

## Commands

```bash
just all      # lint, test, build
just build    # go build
just test     # go test -v -race ./internal/...
just lint     # gofmt, golangci-lint, go vet
just vuln     # govulncheck
```

## Invariants

1. **Shim**: mpv NEVER calls yt-dlp directly. Use generated shim at `bin/yt-dlp`
2. **Source of truth**: mpv's internal playlist is master state. Mirror via IPC, don't predict
3. **Idempotency**: Check existence/version before downloading in bootstrap
4. **Lint compliance**: All errors handled or explicitly ignored (`_ = err`)

## Patterns

### Adding IPC Commands

1. Add command wrapper in `internal/player/ipc.go`
2. Expose method in `internal/player/mpv.go`
3. Add handler in `internal/server/handlers.go`

### State Updates

- Use channels for state mutations (actor-like)
- SSE broadcaster sends snapshots to new clients, deltas to existing
- Metadata cache pruned after 5 minutes

### Error Handling

- Provisioning: `log.Fatal` on missing prerequisites
- Runtime: retry with backoff, don't crash
- Always use absolute paths for cache `bin/` directory
- Use `path/filepath` for cross-platform compatibility
