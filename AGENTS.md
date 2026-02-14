# Skaldi Agent Protocol

## 1. Project Identity

**Skaldi** is a self-provisioning network jukebox delivered as a single Go binary.
**Core Philosophy:** Zero external Go dependencies, rigorous self-containment, and robust runtime management.

## 2. Tech Stack & Constraints

### Backend (Go)

- **Stdlib Only:** Strictly NO external Go modules (no `go get`). Use standard `net/http`, `encoding/json`, `os/exec`.
- **Concurrency:** Use channels for state management (Actor-like model for `player/state.go`).
- **Logging:** Use `log/slog` (structured logging) from the standard library.
- **Error Handling:** Fail fast during provisioning (fatal). Recover gracefully during runtime (retry/reconnect).
- **Linting:** CI enforces `golangci-lint`. Run `make lint` locally before pushing.

### Frontend (Web)

- **Single File:** `web/index.html` embedded via `go:embed`.
- **No Build Step:** Vanilla ES6 JavaScript, CSS Variables. No npm, no bundler.
- **Communication:** `fetch` for commands, `EventSource` (SSE) for state updates.

### Runtime Dependencies (Managed)

The agent must respect the auto-provisioning architecture:

- **uv:** Used to install `yt-dlp`.
- **Bun:** Used as the JS runtime for `yt-dlp`.
- **Shim:** A generated shell script acts as the interface between `mpv` and `yt-dlp`.

## 3. Directory Structure & Responsibilities

```text
skaldi/
├── cmd/skaldi/
│   └── main.go                 # Application entry point
├── internal/
│   ├── bootstrap/              # Provisioning (uv, bun, shim)
│   ├── discovery/              # mDNS service registration
│   ├── player/                 # mpv process & IPC
│   ├── resolver/               # yt-dlp metadata extraction
│   └── server/                 # HTTP & SSE handlers
├── web/
│   ├── fs.go                   # Embedded filesystem
│   └── index.html              # Single-page web UI
├── .github/workflows/
│   ├── ci.yml                  # CI: lint, test, build, vuln check
│   └── release.yml             # CD: cross-compile releases
├── Makefile                    # Local development tasks
└── go.mod                      # Module definition (stdlib only)
```

## 4. Development Workflows

### Build & Verification

- **All:** `make all` (lint, test, build)
- **Build:** `make build` - compiles binary
- **Run:** `./skaldi` (First run triggers provisioning)
- **Test:** `make test` - runs unit tests
- **Lint:** `make lint` - runs golangci-lint and go vet
- **Vulnerability:** `make vuln` - runs govulncheck

### CI/CD Pipeline

CI triggers on every push and pull request. Release builds trigger on version tags (`v*`).

**Note:** Install [golangci-lint](https://golangci-lint.run/usage/install/) locally to catch issues before CI.

### Critical Invariants (DO NOT BREAK)

1. **The Shim:** `mpv` must NEVER call `yt-dlp` directly. It must point to the generated shim script to ensure Bun is loaded correctly.
2. **IPC Source of Truth:** The `mpv` internal playlist is the master state. The Go app mirrors this state via IPC events (`observe_property`), it does not predict it.
3. **Idempotency:** Provisioning steps in `internal/bootstrap` must check existence/versions before downloading.
4. **Lint Compliance:** All code must pass `golangci-lint`. CI fails on unhandled errors (`errcheck`).

## 5. Coding Standards

### Go

- **Contexts:** Pass `context.Context` to all long-running operations (IPC, Subprocesses).
- **OS Agnostic:** Use `path/filepath` for all file system operations. Handle CRLF/LF if necessary, but prefer LF.
- **Command Execution:** Always use absolute paths for the `bin/` directory in the cache.
- **Error Handling:** Always handle or explicitly ignore errors with `_ =`. CI will fail on unchecked errors.

### JavaScript (Frontend)

- **State:** Store local state sparingly. Render based on SSE snapshots.
- **Mobile First:** CSS grid/flexbox. Touch targets > 44px.

## 6. Common Tasks

### Adding a new IPC command

1. Add command wrapper in `internal/player/ipc.go`.
2. Expose method in `internal/player/mpv.go`.
3. Add handler in `internal/server/handlers.go`.

### Updating Provisioning Logic

1. Check `internal/bootstrap/` files.
2. Update `internal/bootstrap/provision.go`.
3. Ensure backward compatibility or clean cache invalidation.

### Releasing

1. Tag: `git tag v0.1.0`
2. Push: `git push origin v0.1.0`
3. GitHub Actions automatically builds and releases binaries for all platforms.
