package player

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"skaldi/internal/bootstrap"
)

type Manager struct {
	cfg    *bootstrap.Config
	logger *slog.Logger
	ipc    *IPCClient

	cmd    *exec.Cmd
	cancel context.CancelFunc

	State        *State
	StateUpdates chan Snapshot

	tempFiles   map[string]bool
	tempFilesMu sync.Mutex

	stopping atomic.Bool
}

func NewManager(cfg *bootstrap.Config, logger *slog.Logger) *Manager {
	return &Manager{
		cfg:          cfg,
		logger:       logger,
		ipc:          NewIPCClient(cfg.MpvSocket, logger),
		State:        NewState(),
		StateUpdates: make(chan Snapshot, 100),
		tempFiles:    make(map[string]bool),
	}
}

func (m *Manager) RegisterTempFile(path string) {
	m.tempFilesMu.Lock()
	defer m.tempFilesMu.Unlock()
	m.tempFiles[path] = true
}

func (m *Manager) CleanupTempFiles() {
	m.tempFilesMu.Lock()
	defer m.tempFilesMu.Unlock()

	for path := range m.tempFiles {
		os.Remove(path)
	}
	m.tempFiles = make(map[string]bool)
}

func (m *Manager) checkTempFiles(entries []MpvPlaylistEntry) {
	m.tempFilesMu.Lock()
	defer m.tempFilesMu.Unlock()

	if len(m.tempFiles) == 0 {
		return
	}

	inPlaylist := make(map[string]bool)
	for _, entry := range entries {
		inPlaylist[entry.Filename] = true
	}

	for path := range m.tempFiles {
		if !inPlaylist[path] {
			os.Remove(path)
			delete(m.tempFiles, path)
		}
	}
}

func (m *Manager) Run(ctx context.Context) error {
	defer m.CleanupTempFiles()
	m.StartEventLoop(ctx)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if m.stopping.Load() {
			return nil
		}

		if err := m.start(ctx); err != nil {
			if m.stopping.Load() {
				return nil
			}
			m.logger.Error("Failed to start mpv", "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
				continue
			}
		}

		if err := m.cmd.Wait(); err != nil {
			if !m.stopping.Load() {
				m.logger.Warn("mpv exited unexpectedly", "error", err)
			}
		}

		if m.stopping.Load() {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
			continue
		}
	}
}

func (m *Manager) Stop() {
	m.stopping.Store(true)
	if m.ipc != nil {
		m.ipc.Exec("quit")
		m.ipc.Close()
	}
}

func (m *Manager) start(ctx context.Context) error {
	if _, err := os.Stat(m.cfg.MpvSocket); err == nil {
		os.Remove(m.cfg.MpvSocket)
	}

	shimPath := m.cfg.ShimPath()
	jsRuntime := fmt.Sprintf("js-runtimes=bun:%s", m.cfg.BunPath())

	args := []string{
		"--idle=yes",
		"--no-video",
		"--no-terminal",
		fmt.Sprintf("--input-ipc-server=%s", m.cfg.MpvSocket),
		"--ytdl-format=bestaudio/best",
		"--af=dynaudnorm",
		fmt.Sprintf("--script-opts=ytdl_hook-ytdl_path=%s", shimPath),
		fmt.Sprintf("--ytdl-raw-options=%s", jsRuntime),
	}

	cmd := exec.CommandContext(ctx, "mpv", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	m.logger.Debug("Starting mpv", "args", args)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start mpv: %w", err)
	}
	m.cmd = cmd

	if err := m.waitForSocket(ctx); err != nil {
		cmd.Process.Kill()
		return err
	}

	if err := m.ipc.Connect(); err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("failed to connect IPC: %w", err)
	}

	m.RegisterObservers()
	return nil
}

func (m *Manager) waitForSocket(ctx context.Context) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := os.Stat(m.cfg.MpvSocket); err == nil {
				return nil
			}
			if m.cmd.ProcessState != nil && m.cmd.ProcessState.Exited() {
				return fmt.Errorf("mpv exited prematurely")
			}
		}
	}
}

func (m *Manager) Wait() error {
	return m.cmd.Wait()
}

func (m *Manager) Exec(args ...interface{}) (interface{}, error) {
	return m.ipc.Exec(args...)
}
