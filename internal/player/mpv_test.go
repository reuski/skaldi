// SPDX-License-Identifier: AGPL-3.0-or-later

package player

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func writeFakeMpv(t *testing.T, script string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "mpv")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	return path
}

func fakeMpvOnPath(t *testing.T, script string) {
	t.Helper()

	path := writeFakeMpv(t, script)
	t.Setenv("PATH", filepath.Dir(path)+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func readPID(t *testing.T, path string) int {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the child pid failed: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parsing the child pid failed: %v (%q)", err, data)
	}
	return pid
}

func attachFakeIPC(t *testing.T, m *Manager) {
	t.Helper()

	client, server := net.Pipe()
	attachIPC(t, m.ipc, client)

	responder := make(chan struct{})
	go func() {
		defer close(responder)
		scanner := bufio.NewScanner(server)
		for scanner.Scan() {
			var cmd Command
			if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil || cmd.RequestID == 0 {
				continue
			}
			_, _ = fmt.Fprintf(server, "{\"request_id\":%d,\"error\":\"success\"}\n", cmd.RequestID)
		}
	}()

	t.Cleanup(func() {
		_ = server.Close()
		<-responder
	})
}

func TestStartReapsChildThatExitsBeforeSocket(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	fakeMpvOnPath(t, "#!/bin/sh\necho $$ > "+pidFile+"\nexit 3\n")

	m := newTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), socketReadyTimeout+5*time.Second)
	defer cancel()

	start := time.Now()
	if _, err := m.start(ctx); err == nil {
		t.Fatal("start succeeded although mpv exited before its socket existed")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("start took %s, want a prompt failure after the child exited", elapsed)
	}

	// Only an unreaped zombie still accepts a signal.
	pid := readPID(t, pidFile)
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("mpv process %d still exists, want it reaped", pid)
	} else if !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("kill(%d, 0) = %v, want ESRCH", pid, err)
	}
}

func TestWaitForSocketReturnsWhenChildExits(t *testing.T) {
	script := writeFakeMpv(t, "#!/bin/sh\nexit 7\n")
	cmd := exec.Command(script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	proc := newMpvProcess(cmd)

	m := newTestManager(t)
	err := m.waitForSocket(context.Background(), proc)
	if err == nil {
		t.Fatal("waitForSocket succeeded although the child exited without a socket")
	}
	if !strings.Contains(err.Error(), "before its IPC socket was ready") {
		t.Fatalf("error = %v, want the premature-exit error", err)
	}
}

func TestWaitForSocketReturnsOnCancellation(t *testing.T) {
	script := writeFakeMpv(t, "#!/bin/sh\nwhile true; do sleep 1; done\n")
	cmd := exec.Command(script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	proc := newMpvProcess(cmd)

	m := newTestManager(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- m.waitForSocket(ctx, proc)
	}()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waitForSocket = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waitForSocket kept waiting after cancellation")
	}

	proc.kill()
}

func TestRunRetriesAfterChildExitsBeforeSocket(t *testing.T) {
	countFile := filepath.Join(t.TempDir(), "count")
	fakeMpvOnPath(t, "#!/bin/sh\nn=0\nif [ -f \""+countFile+"\" ]; then n=$(cat \""+countFile+"\"); fi\nn=$((n+1))\necho \"$n\" > \""+countFile+"\"\nexit 4\n")

	m := newTestManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- m.Run(ctx)
	}()

	deadline := time.Now().Add(8 * time.Second)
	for {
		data, _ := os.ReadFile(countFile)
		if count, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && count >= 2 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("mpv was started %q times, want a retry after it exited before its socket", data)
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

func TestPublishSnapshotReplacesOldestWhenFull(t *testing.T) {
	m := newTestManager(t)

	for i := 0; i < cap(m.StateUpdates); i++ {
		m.State.RecordPlaybackFailure(i + 1)
		m.broadcastSnapshot()
	}
	if got := len(m.StateUpdates); got != cap(m.StateUpdates) {
		t.Fatalf("queued snapshots = %d, want the buffer full at %d", got, cap(m.StateUpdates))
	}

	newest := cap(m.StateUpdates) + 1
	m.State.RecordPlaybackFailure(newest)
	m.broadcastSnapshot()

	if got := len(m.StateUpdates); got != cap(m.StateUpdates) {
		t.Fatalf("queued snapshots = %d, want the bounded capacity %d", got, cap(m.StateUpdates))
	}

	occurrences := make([]uint64, 0, cap(m.StateUpdates))
	for range cap(m.StateUpdates) {
		snap := <-m.StateUpdates
		if snap.LastFailure == nil {
			t.Fatal("queued snapshot lost the playback failure")
		}
		occurrences = append(occurrences, snap.LastFailure.Occurrence)
	}

	if got := occurrences[len(occurrences)-1]; got != uint64(newest) {
		t.Fatalf("newest queued occurrence = %d, want %d", got, newest)
	}
	if got := occurrences[0]; got != 2 {
		t.Fatalf("oldest queued occurrence = %d, want 2 (occurrence 1 replaced)", got)
	}
}

func TestPublishSnapshotSurvivesConcurrentConsumerDrain(t *testing.T) {
	m := newTestManager(t)
	m.StateUpdates = make(chan Snapshot, 1)

	const rounds = 20000

	var latest atomic.Uint64
	var consumer sync.WaitGroup
	consumer.Add(1)
	go func() {
		defer consumer.Done()
		for snap := range m.StateUpdates {
			if snap.LastFailure != nil {
				latest.Store(snap.LastFailure.Occurrence)
			}
		}
	}()

	var publishers sync.WaitGroup
	publishers.Add(1)
	go func() {
		defer publishers.Done()
		for i := uint64(1); i <= rounds; i++ {
			m.State.RecordPlaybackFailure(int(i))
			m.broadcastSnapshot()
		}
	}()
	publishers.Wait()

	deadline := time.Now().Add(5 * time.Second)
	for latest.Load() != rounds {
		if time.Now().After(deadline) {
			m.closeStateUpdates()
			consumer.Wait()
			t.Fatalf("newest snapshot dropped: consumer saw occurrence %d of %d", latest.Load(), rounds)
		}
		time.Sleep(time.Millisecond)
	}

	m.closeStateUpdates()
	consumer.Wait()
}

func TestStopClosesStateUpdatesAndIgnoresLatePublication(t *testing.T) {
	m := newTestManager(t)
	attachFakeIPC(t, m)

	for i := 0; i < cap(m.StateUpdates); i++ {
		m.State.RecordPlaybackFailure(i + 1)
		m.broadcastSnapshot()
	}

	m.Stop()

	drained := 0
	for range m.StateUpdates {
		drained++
	}
	if drained != cap(m.StateUpdates) {
		t.Fatalf("queued snapshots before close = %d, want %d", drained, cap(m.StateUpdates))
	}

	m.State.RecordPlaybackFailure(1)
	m.broadcastSnapshot()
	m.handleEvent(Event{Event: "end-file", Reason: "error", PlaylistEntryID: 1})
	if err := m.SetVolume(0.5); err == nil {
		t.Fatal("SetVolume succeeded after Stop, want a closed-connection error")
	}
	if err := m.ToggleMute(); err == nil {
		t.Fatal("ToggleMute succeeded after Stop, want a closed-connection error")
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Stop()
		}()
	}
	wg.Wait()
}

func TestStopConcurrentWithPublication(t *testing.T) {
	m := newTestManager(t)
	attachFakeIPC(t, m)

	for i := 0; i < cap(m.StateUpdates); i++ {
		m.State.RecordPlaybackFailure(i + 1)
		m.broadcastSnapshot()
	}

	stopPublishing := make(chan struct{})
	var publishers sync.WaitGroup

	for range 3 {
		publishers.Add(1)
		go func() {
			defer publishers.Done()
			for {
				select {
				case <-stopPublishing:
					return
				default:
				}
				m.handleEvent(Event{Event: "end-file", Reason: "error", PlaylistEntryID: 2})
			}
		}()
	}

	publishers.Add(1)
	go func() {
		defer publishers.Done()
		for {
			select {
			case <-stopPublishing:
				return
			default:
			}
			_ = m.SetVolume(0.5)
			_ = m.SetMuted(true)
		}
	}()

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		m.Stop()
	}()

	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop blocked while producers were publishing")
	}

	close(stopPublishing)
	publishers.Wait()

	for range m.StateUpdates {
	}

	m.State.RecordPlaybackFailure(99)
	m.broadcastSnapshot()
	if got := len(m.StateUpdates); got != 0 {
		t.Fatalf("snapshots published after Stop = %d, want 0", got)
	}
}
