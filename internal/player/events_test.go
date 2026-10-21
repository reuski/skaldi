// SPDX-License-Identifier: AGPL-3.0-or-later

package player

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/reuski/skaldi/internal/bootstrap"
	"github.com/reuski/skaldi/internal/resolver"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &bootstrap.Config{
		CacheDir:   t.TempDir(),
		BinDir:     t.TempDir(),
		DataDir:    t.TempDir(),
		MpvSocket:  filepath.Join(t.TempDir(), "mpv.sock"),
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
	}
	m := NewManager(cfg, logger)
	t.Cleanup(m.Stop)
	return m
}

func TestEndFileEventRecordsPlaybackFailure(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	c := NewIPCClient("unused", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := c.installConn(clientConn); err != nil {
		t.Fatalf("installConn failed: %v", err)
	}
	t.Cleanup(func() {
		_ = serverConn.Close()
		c.Close()
	})

	m := newTestManager(t)
	failing := MpvPlaylistEntry{Filename: "https://www.youtube.com/watch?v=broken", ID: 7}
	playing := MpvPlaylistEntry{Filename: "https://www.youtube.com/watch?v=next", ID: 8}
	m.State.StoreMetadata(failing.Filename, resolver.Track{Title: "Broken Song", WebpageURL: failing.Filename, Source: resolver.SourceYouTube})
	m.State.StoreMetadata(playing.Filename, resolver.Track{Title: "Next Song", WebpageURL: playing.Filename, Source: resolver.SourceYouTube})
	m.State.SetPlaylist([]MpvPlaylistEntry{failing, playing})
	m.State.SetPlaylistPos(1)

	line := `{"event":"end-file","reason":"error","error":"Failed to open stream","playlist_entry_id":7}` + "\n"
	if _, err := serverConn.Write([]byte(line)); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	select {
	case ev := <-c.Events:
		m.handleEvent(ev)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the decoded end-file event")
	}

	failure := m.State.Snapshot().LastFailure
	if failure == nil {
		t.Fatal("LastFailure = nil, want a recorded failure")
	}
	if failure.EntryID != 7 || failure.Title != "Broken Song" {
		t.Fatalf("failure = %#v, want entry 7 titled Broken Song", failure)
	}

	select {
	case broadcast := <-m.StateUpdates:
		if broadcast.LastFailure == nil || broadcast.LastFailure.Occurrence != failure.Occurrence {
			t.Fatalf("broadcast = %#v, want the recorded failure", broadcast.LastFailure)
		}
		data, err := json.Marshal(broadcast)
		if err != nil {
			t.Fatalf("Marshal failed: %v", err)
		}
		if strings.Contains(string(data), "Failed to open stream") {
			t.Fatalf("broadcast leaked mpv error text: %s", data)
		}
	default:
		t.Fatal("expected a broadcast snapshot for the failure")
	}
}

func TestEndFileEventIgnoresNonErrorReasons(t *testing.T) {
	for _, reason := range []string{"eof", "stop", "quit", "redirect", ""} {
		m := newTestManager(t)
		m.handleEvent(Event{Event: "end-file", Reason: reason, PlaylistEntryID: 1})

		if snap := m.State.Snapshot(); snap.LastFailure != nil {
			t.Fatalf("reason %q recorded failure %#v, want none", reason, snap.LastFailure)
		}
		select {
		case <-m.StateUpdates:
			t.Fatalf("reason %q broadcast a snapshot, want none", reason)
		default:
		}
	}
}
