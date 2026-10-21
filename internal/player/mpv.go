// SPDX-License-Identifier: AGPL-3.0-or-later

// Package player manages the mpv media player process and IPC communication.
package player

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
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

	State        *State
	StateUpdates chan Snapshot

	tempFiles   map[string]bool
	tempFilesMu sync.Mutex

	playbackStatePath string
	volumeMu          sync.Mutex
	preferAOVolume    atomic.Bool
	preferAOMute      atomic.Bool

	stopCh   chan struct{}
	stopOnce sync.Once

	publishMu       sync.Mutex
	stateUpdatesOff bool

	loopMu   sync.Mutex
	loopWG   sync.WaitGroup
	loopsOff bool
}

func NewManager(cfg *bootstrap.Config, logger *slog.Logger) *Manager {
	playbackStatePath := cfg.PlaybackStatePath()
	startupVolume := loadStartupVolume(playbackStatePath, logger)

	return &Manager{
		cfg:               cfg,
		logger:            logger,
		ipc:               NewIPCClient(cfg.MpvSocket, logger),
		history:           history.New(cfg.DataDir, logger),
		State:             NewStateWithVolume(startupVolume),
		StateUpdates:      make(chan Snapshot, 100),
		tempFiles:         make(map[string]bool),
		playbackStatePath: playbackStatePath,
		stopCh:            make(chan struct{}),
	}
}

func (m *Manager) stopRequested() bool {
	select {
	case <-m.stopCh:
		return true
	default:
		return false
	}
}

// Register under the stop lock so Add cannot race Wait.
func (m *Manager) startLoop(ctx context.Context, run func(context.Context)) {
	m.loopMu.Lock()
	if m.loopsOff {
		m.loopMu.Unlock()
		return
	}
	m.loopWG.Add(1)
	m.loopMu.Unlock()

	go func() {
		defer m.loopWG.Done()
		run(ctx)
	}()
}

func (m *Manager) stopLoops() {
	m.loopMu.Lock()
	m.loopsOff = true
	m.loopMu.Unlock()
	m.loopWG.Wait()
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
		_ = os.Remove(path)
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
			_ = os.Remove(path)
			delete(m.tempFiles, path)
		}
	}
}

const metadataTTL = 5 * time.Minute

func (m *Manager) StartMetadataGC(ctx context.Context) {
	m.startLoop(ctx, func(ctx context.Context) {
		ticker := time.NewTicker(metadataTTL)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-m.stopCh:
				return
			case <-ticker.C:
				cutoff := time.Now().Add(-metadataTTL)
				m.State.PruneMetadataBefore(cutoff)
			}
		}
	})
}

const rotateRetry = 30 * time.Second

func (m *Manager) StartDailyRotation(ctx context.Context) {
	m.startLoop(ctx, func(ctx context.Context) {
		for {
			if !m.sleep(ctx, untilNextMidnight(time.Now())) {
				return
			}
			for !m.rotateIfStale() {
				if !m.sleep(ctx, rotateRetry) {
					return
				}
			}
		}
	})
}

func untilNextMidnight(now time.Time) time.Duration {
	t := now.Local()
	next := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).AddDate(0, 0, 1)
	return time.Until(next)
}

func (m *Manager) sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-m.stopCh:
		return false
	case <-timer.C:
		return true
	}
}

func (m *Manager) rotateIfStale() bool {
	if m.State.PlaylistActive() {
		return false
	}

	m.logger.Debug("Rotating stale session: clearing playlist and history")
	if _, err := m.ipc.Exec("stop"); err != nil {
		m.logger.Error("Failed to clear stale playlist", "error", err)
		return false
	}
	m.State.ResetPlayed()
	m.history.Rotate("daily")
	return true
}

func (m *Manager) Run(ctx context.Context) error {
	defer m.CleanupTempFiles()
	m.StartEventLoop(ctx)
	m.StartMetadataGC(ctx)
	m.StartDailyRotation(ctx)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if m.stopRequested() {
			return nil
		}

		proc, err := m.start(ctx)
		if err != nil {
			if m.stopRequested() {
				return nil
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			m.logger.Error("Failed to start mpv", "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-m.stopCh:
				return nil
			case <-time.After(2 * time.Second):
				continue
			}
		}

		if err := proc.wait(); err != nil {
			if !m.stopRequested() {
				m.logger.Warn("mpv exited unexpectedly", "error", err)
			}
		}

		if m.stopRequested() {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.stopCh:
			return nil
		case <-time.After(1 * time.Second):
			continue
		}
	}
}

func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		close(m.stopCh)

		if m.ipc != nil {
			_, _ = m.ipc.Exec("quit")
			m.ipc.Close()
		}

		// Join publishers before closing history and StateUpdates.
		m.stopLoops()

		if m.history != nil {
			m.history.Close()
		}

		m.closeStateUpdates()
	})
}

const socketReadyTimeout = 15 * time.Second

// One goroutine owns Wait, including failed-start reaping.
type mpvProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

func newMpvProcess(cmd *exec.Cmd) *mpvProcess {
	p := &mpvProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	return p
}

func (p *mpvProcess) wait() error {
	<-p.done
	return p.err
}

func (p *mpvProcess) kill() {
	_ = p.cmd.Process.Kill()
	<-p.done
}

func (m *Manager) start(ctx context.Context) (*mpvProcess, error) {
	if _, err := os.Stat(m.cfg.MpvSocket); err == nil {
		_ = os.Remove(m.cfg.MpvSocket)
	}

	shimPath := m.cfg.ShimPath()
	jsRuntime := fmt.Sprintf("js-runtimes=bun:%s", m.cfg.BunPath())

	startupVolume := m.State.Snapshot().Volume
	args := []string{
		"--idle=yes",
		"--no-video",
		"--no-terminal",
		fmt.Sprintf("--input-ipc-server=%s", m.cfg.MpvSocket),
		"--ytdl-format=bestaudio/best",
		"--af=dynaudnorm",
		"--audio-client-name=skaldi",
		"--gapless-audio=yes",
		"--audio-stream-silence=yes",
		"--volume-max=100",
		volumeArg(startupVolume),
		fmt.Sprintf("--script-opts=ytdl_hook-ytdl_path=%s", shimPath),
		fmt.Sprintf("--ytdl-raw-options=%s", jsRuntime),
	}
	if runtime.GOOS == "linux" {
		args = append(args, "--ao=pipewire,pulse,alsa")
	}

	cmd := exec.CommandContext(ctx, "mpv", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	m.logger.Debug("Starting mpv", "args", args)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start mpv: %w", err)
	}
	proc := newMpvProcess(cmd)

	if err := m.waitForSocket(ctx, proc); err != nil {
		proc.kill()
		return nil, err
	}

	if err := m.ipc.Connect(); err != nil {
		proc.kill()
		return nil, fmt.Errorf("failed to connect IPC: %w", err)
	}

	m.RegisterObservers()
	return proc, nil
}

func (m *Manager) waitForSocket(ctx context.Context, proc *mpvProcess) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(socketReadyTimeout)
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-proc.done:
			if err := proc.err; err != nil {
				return fmt.Errorf("mpv exited before its IPC socket was ready: %w", err)
			}
			return fmt.Errorf("mpv exited before its IPC socket was ready")
		case <-deadline.C:
			return fmt.Errorf("timed out after %s waiting for the mpv IPC socket", socketReadyTimeout)
		case <-ticker.C:
			if _, err := os.Stat(m.cfg.MpvSocket); err == nil {
				return nil
			}
		}
	}
}

func (m *Manager) Exec(args ...any) (any, error) {
	return m.ipc.Exec(args...)
}

func (m *Manager) SetVolume(volume float64) error {
	volume = clampVolume(volume)

	m.volumeMu.Lock()
	defer m.volumeMu.Unlock()

	if err := m.setPreferredVolume(volume); err != nil {
		return err
	}

	m.State.SetVolume(volume)
	saveStartupVolume(m.playbackStatePath, volume, m.logger)
	m.broadcastSnapshot()
	return nil
}

func (m *Manager) ToggleMute() error {
	return m.SetMuted(!m.State.Snapshot().Muted)
}

func (m *Manager) SetMuted(muted bool) error {
	m.volumeMu.Lock()
	defer m.volumeMu.Unlock()

	if err := m.setPreferredMute(muted); err != nil {
		return err
	}

	m.State.SetMuted(muted)
	m.broadcastSnapshot()
	return nil
}

func (m *Manager) setPreferredVolume(volume float64) error {
	if _, err := m.ipc.Exec("set_property", "ao-volume", volume); err == nil {
		m.preferAOVolume.Store(true)
		_, _ = m.ipc.Exec("set_property", "volume", 100.0)
		return nil
	}

	_, err := m.ipc.Exec("set_property", "volume", volume)
	return err
}

func (m *Manager) setPreferredMute(muted bool) error {
	if _, err := m.ipc.Exec("set_property", "ao-mute", muted); err == nil {
		m.preferAOMute.Store(true)
		_, _ = m.ipc.Exec("set_property", "mute", false)
		return nil
	}

	_, err := m.ipc.Exec("set_property", "mute", muted)
	return err
}

func (m *Manager) activateAOVolume() {
	m.volumeMu.Lock()
	defer m.volumeMu.Unlock()

	volume := m.State.Snapshot().Volume
	if _, err := m.ipc.Exec("set_property", "ao-volume", volume); err != nil {
		m.logger.Debug("Failed to apply output volume", "error", err)
		return
	}
	_, _ = m.ipc.Exec("set_property", "volume", 100.0)
}

func (m *Manager) activateAOMute() {
	m.volumeMu.Lock()
	defer m.volumeMu.Unlock()

	muted := m.State.Snapshot().Muted
	if _, err := m.ipc.Exec("set_property", "ao-mute", muted); err != nil {
		m.logger.Debug("Failed to apply output mute", "error", err)
		return
	}
	_, _ = m.ipc.Exec("set_property", "mute", false)
}

func (m *Manager) PlayIndex(targetIdx int) error {
	_, err := m.ipc.Exec("playlist-play-index", targetIdx)
	return err
}
