// SPDX-License-Identifier: AGPL-3.0-or-later

// Package player manages the mpv media player process and IPC communication.
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

	"github.com/reuski/skaldi/internal/bootstrap"
	"github.com/reuski/skaldi/internal/history"
)

type Manager struct {
	cfg     *bootstrap.Config
	logger  *slog.Logger
	ipc     *IPCClient
	history *history.Logger

	cmd *exec.Cmd

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
		history:      history.New(cfg.DataDir, logger),
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

const metadataTTL = 5 * time.Minute

func (m *Manager) StartMetadataGC(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(metadataTTL)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cutoff := time.Now().Add(-metadataTTL)
				m.State.PruneMetadataBefore(cutoff)
			}
		}
	}()
}

const dailyClearRetry = 30 * time.Second

func (m *Manager) StartDailyPlaylistClear(ctx context.Context) {
	go func() {
		var lastDate string
		pendingClear := false

		for {
			now := time.Now()
			today := now.Local().Format("2006-01-02")

			if lastDate != "" && lastDate != today {
				pendingClear = true
			}
			lastDate = today

			var wait time.Duration
			if pendingClear {
				if m.clearIfIdle() {
					pendingClear = false
					wait = time.Until(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1))
				} else {
					wait = dailyClearRetry
				}
			} else {
				wait = time.Until(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1))
			}

			if wait < time.Second {
				wait = time.Second
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}()
}

func (m *Manager) clearIfIdle() bool {
	snap := m.State.Snapshot()

	if snap.Status != StatusIdle {
		m.logger.Debug("Skipping daily playlist clear: still playing")
		return false
	}

	if len(snap.Upcoming) > 0 || snap.NowPlaying != nil {
		m.logger.Debug("Skipping daily playlist clear: upcoming tracks remain")
		return false
	}

	m.logger.Debug("Clearing daily playlist (idle, empty queue)")
	if _, err := m.ipc.Exec("playlist-clear"); err != nil {
		m.logger.Error("Failed to clear playlist for daily reset", "error", err)
	}
	m.State.mu.Lock()
	m.State.recentPlayed = nil
	m.State.currentItem = nil
	m.State.mu.Unlock()
	return true
}

func (m *Manager) Run(ctx context.Context) error {
	defer m.CleanupTempFiles()
	m.StartEventLoop(ctx)
	m.StartMetadataGC(ctx)
	m.StartDailyPlaylistClear(ctx)

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
		_, _ = m.ipc.Exec("quit")
		m.ipc.Close()
	}
	if m.history != nil {
		m.history.Close()
	}
	close(m.StateUpdates)
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
		_ = cmd.Process.Kill()
		return err
	}

	if err := m.ipc.Connect(); err != nil {
		_ = cmd.Process.Kill()
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

func (m *Manager) Exec(args ...any) (any, error) {
	return m.ipc.Exec(args...)
}

func (m *Manager) PlayIndex(targetIdx int) error {
	_, err := m.ipc.Exec("playlist-play-index", targetIdx)
	return err
}
