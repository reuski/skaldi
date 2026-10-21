// SPDX-License-Identifier: AGPL-3.0-or-later

package player

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/reuski/skaldi/internal/resolver"
)

func TestNewState(t *testing.T) {
	s := NewState()

	if s.metadata == nil {
		t.Error("metadata map should be initialized")
	}

	if len(s.playlist) != 0 {
		t.Error("playlist should be empty")
	}

	if s.playlistPos != -1 {
		t.Errorf("playlistPos = %d, want -1", s.playlistPos)
	}
}

func TestState_StoreMetadata(t *testing.T) {
	s := NewState()

	track := resolver.Track{
		Title:    "Test Track",
		Duration: 180.5,
		Uploader: "Test Uploader",
	}

	s.StoreMetadata("https://example.com/track", track)

	s.mu.RLock()
	stored, ok := s.metadata["https://example.com/track"]
	s.mu.RUnlock()

	if !ok {
		t.Error("Track should be stored in metadata")
	}

	if stored.Title != "Test Track" {
		t.Errorf("Title = %q, want %q", stored.Title, "Test Track")
	}
}

func TestState_SetIdle(t *testing.T) {
	s := NewState()

	s.SetIdle(true)

	if !s.idleActive {
		t.Error("idleActive should be true")
	}

	s.SetIdle(false)

	if s.idleActive {
		t.Error("idleActive should be false")
	}
}

func TestState_SetIdle_DoesNotResetPlaylistPos(t *testing.T) {
	s := NewState()
	s.playlistPos = 5

	s.SetIdle(true)

	if s.playlistPos != 5 {
		t.Errorf("playlistPos = %d, want 5", s.playlistPos)
	}
}

func TestState_SetPaused(t *testing.T) {
	s := NewState()

	s.SetPaused(true)

	if !s.paused {
		t.Error("paused should be true")
	}

	s.SetPaused(false)

	if s.paused {
		t.Error("paused should be false")
	}
}

func TestState_SetTimePos(t *testing.T) {
	s := NewState()

	s.SetTimePos(123.45)

	if s.timePos != 123.45 {
		t.Errorf("timePos = %f, want 123.45", s.timePos)
	}
}

func TestState_SetDuration(t *testing.T) {
	s := NewState()

	s.SetDuration(300.0)

	if s.duration != 300.0 {
		t.Errorf("duration = %f, want 300.0", s.duration)
	}
}

func TestState_SetPlaylist(t *testing.T) {
	s := NewState()

	entries := []MpvPlaylistEntry{
		{Filename: "track1.mp3", ID: 1},
		{Filename: "track2.mp3", ID: 2},
		{Filename: "track3.mp3", ID: 3},
	}

	s.SetPlaylist(entries)

	if len(s.playlist) != 3 {
		t.Errorf("playlist length = %d, want 3", len(s.playlist))
	}
}

func TestState_SetPlaylistPos(t *testing.T) {
	s := NewState()
	s.SetPlaylist([]MpvPlaylistEntry{
		{Filename: "track1.mp3", ID: 1},
		{Filename: "track2.mp3", ID: 2},
		{Filename: "track3.mp3", ID: 3},
	})
	s.timePos = 100.0
	s.duration = 200.0

	item := s.SetPlaylistPos(2)

	if s.playlistPos != 2 {
		t.Errorf("playlistPos = %d, want 2", s.playlistPos)
	}
	if s.timePos != 100.0 {
		t.Errorf("timePos should be preserved, got %f", s.timePos)
	}
	if s.duration != 200.0 {
		t.Errorf("duration should be preserved, got %f", s.duration)
	}
	if item == nil {
		t.Error("expected non-nil currentItem")
	}
}

func TestState_SetPlaylistPos_SamePos_ReturnsNilWhenEmpty(t *testing.T) {
	s := NewState()
	s.timePos = 100.0
	s.duration = 200.0

	item := s.SetPlaylistPos(2)

	if s.timePos != 100.0 {
		t.Errorf("timePos should remain 100.0 when pos unchanged, got %f", s.timePos)
	}
	if s.duration != 200.0 {
		t.Errorf("duration should remain 200.0 when pos unchanged, got %f", s.duration)
	}
	if item != nil {
		t.Error("expected nil currentItem for empty playlist at position")
	}
}

func TestState_SetPlaylistPos_SamePos_ReturnsCurrentItem(t *testing.T) {
	s := NewState()
	s.SetPlaylist([]MpvPlaylistEntry{
		{Filename: "track1.mp3", ID: 1},
	})
	s.SetPlaylistPos(0)

	item := s.SetPlaylistPos(0)
	if item == nil {
		t.Fatal("expected non-nil currentItem on same position")
	}
	if item.Index != 0 {
		t.Errorf("Index = %d, want 0", item.Index)
	}
}

func TestState_PruneMetadata(t *testing.T) {
	s := NewState()

	s.StoreMetadata("track1.mp3", resolver.Track{Title: "Track 1"})
	s.StoreMetadata("track2.mp3", resolver.Track{Title: "Track 2"})
	s.StoreMetadata("track3.mp3", resolver.Track{Title: "Track 3"})

	s.SetPlaylist([]MpvPlaylistEntry{
		{Filename: "track1.mp3"},
		{Filename: "track3.mp3"},
	})

	s.PruneMetadata()

	s.mu.RLock()
	_, hasTrack1 := s.metadata["track1.mp3"]
	_, hasTrack2 := s.metadata["track2.mp3"]
	_, hasTrack3 := s.metadata["track3.mp3"]
	s.mu.RUnlock()

	if !hasTrack1 {
		t.Error("track1.mp3 should still be in metadata")
	}

	if hasTrack2 {
		t.Error("track2.mp3 should have been pruned")
	}

	if !hasTrack3 {
		t.Error("track3.mp3 should still be in metadata")
	}
}

func TestState_Snapshot_Status(t *testing.T) {
	tests := []struct {
		name       string
		idleActive bool
		paused     bool
		wantStatus PlaybackStatus
	}{
		{
			name:       "idle",
			idleActive: true,
			paused:     false,
			wantStatus: StatusIdle,
		},
		{
			name:       "paused",
			idleActive: false,
			paused:     true,
			wantStatus: StatusPaused,
		},
		{
			name:       "playing",
			idleActive: false,
			paused:     false,
			wantStatus: StatusPlaying,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewState()
			s.idleActive = tc.idleActive
			s.paused = tc.paused

			snap := s.Snapshot()

			if snap.Status != tc.wantStatus {
				t.Errorf("Status = %v, want %v", snap.Status, tc.wantStatus)
			}
		})
	}
}

func TestState_PlaylistActive(t *testing.T) {
	tests := []struct {
		name    string
		idle    bool
		paused  bool
		playing bool
		want    bool
	}{
		{name: "playing", playing: true, want: true},
		{name: "idle", idle: true, playing: true},
		{name: "paused", paused: true, playing: true},
		{name: "not playing"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewState()
			s.SetPlaylist([]MpvPlaylistEntry{{Filename: "track.mp3", Playing: tc.playing}})
			s.SetIdle(tc.idle)
			s.SetPaused(tc.paused)

			if got := s.PlaylistActive(); got != tc.want {
				t.Fatalf("PlaylistActive() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestState_Snapshot_EmptyPlaylist(t *testing.T) {
	s := NewState()

	snap := s.Snapshot()

	if snap.CurrentIdx != -1 {
		t.Errorf("CurrentIdx = %d, want -1 for empty playlist", snap.CurrentIdx)
	}

	if snap.NowPlaying != nil {
		t.Error("NowPlaying should be nil for empty playlist")
	}

	if len(snap.Queue) != 0 {
		t.Error("Queue should be empty")
	}

	if len(snap.History) != 0 {
		t.Error("History should be empty")
	}

	if len(snap.Upcoming) != 0 {
		t.Error("Upcoming should be empty")
	}
}

func TestState_Snapshot_WithPlaylist(t *testing.T) {
	s := NewState()

	s.SetPlaylist([]MpvPlaylistEntry{
		{Filename: "track1.mp3", ID: 1},
		{Filename: "track2.mp3", ID: 2, Current: true},
		{Filename: "track3.mp3", ID: 3},
	})
	s.SetPlaylistPos(1)

	s.StoreMetadata("track1.mp3", resolver.Track{Title: "Track 1", Duration: 100})
	s.StoreMetadata("track2.mp3", resolver.Track{Title: "Track 2", Duration: 200})
	s.StoreMetadata("track3.mp3", resolver.Track{Title: "Track 3", Duration: 300})

	s.SetTimePos(50.0)
	s.SetDuration(200.0)

	snap := s.Snapshot()

	if len(snap.Queue) != 3 {
		t.Errorf("Queue length = %d, want 3", len(snap.Queue))
	}

	if snap.CurrentIdx != 1 {
		t.Errorf("CurrentIdx = %d, want 1", snap.CurrentIdx)
	}

	if snap.NowPlaying == nil {
		t.Fatal("NowPlaying should not be nil")
	}

	if snap.NowPlaying.Title != "Track 2" {
		t.Errorf("NowPlaying.Title = %q, want %q", snap.NowPlaying.Title, "Track 2")
	}

	if len(snap.History) != 0 {
		t.Errorf("History length = %d, want 0", len(snap.History))
	}

	if len(snap.Upcoming) != 1 {
		t.Errorf("Upcoming length = %d, want 1", len(snap.Upcoming))
	}

	if snap.CurrentTime != 50.0 {
		t.Errorf("CurrentTime = %f, want 50.0", snap.CurrentTime)
	}

	if snap.Duration != 200.0 {
		t.Errorf("Duration = %f, want 200.0", snap.Duration)
	}
}

func TestState_Snapshot_UsesMetadataWebpageURLAsFilename(t *testing.T) {
	s := NewState()
	streamURL := "https://demo.example.com/rest/stream.view?u=a&t=hash&s=salt&id=track-1"
	opaqueURL := resolver.BuildSubsonicURI("personal", "track-1")

	s.SetPlaylist([]MpvPlaylistEntry{{Filename: streamURL, ID: 1}})
	s.StoreMetadata(streamURL, resolver.Track{
		Title:      "Track 1",
		WebpageURL: opaqueURL,
	})

	snap := s.Snapshot()
	if len(snap.Queue) != 1 {
		t.Fatalf("queue length = %d, want 1", len(snap.Queue))
	}
	if snap.Queue[0].Filename != opaqueURL {
		t.Fatalf("queue filename = %q, want %q", snap.Queue[0].Filename, opaqueURL)
	}
}

func TestState_Snapshot_MetadataLookup(t *testing.T) {
	s := NewState()

	s.SetPlaylist([]MpvPlaylistEntry{
		{Filename: "https://example.com/track1"},
	})

	s.StoreMetadata("https://example.com/track1", resolver.Track{
		Title:     "Test Track",
		Duration:  180.0,
		Uploader:  "Test Artist",
		Thumbnail: "https://example.com/thumb.jpg",
	})

	snap := s.Snapshot()

	if len(snap.Queue) != 1 {
		t.Fatalf("Queue length = %d, want 1", len(snap.Queue))
	}

	item := snap.Queue[0]
	if item.Title != "Test Track" {
		t.Errorf("Title = %q, want %q", item.Title, "Test Track")
	}

	if item.Duration != 180.0 {
		t.Errorf("Duration = %f, want 180.0", item.Duration)
	}

	if item.Metadata == nil {
		t.Error("Metadata should be populated")
	}
}

func TestComputeDelta_MetadataChange(t *testing.T) {
	s := NewState()
	s.SetPlaylist([]MpvPlaylistEntry{
		{Filename: "track1.mp3", ID: 1},
	})
	s.SetPlaylistPos(0)
	s.StoreMetadata("track1.mp3", resolver.Track{Title: "Title 1", Duration: 100})

	prev := s.Snapshot()

	s.StoreMetadata("track1.mp3", resolver.Track{Title: "Title 2", Duration: 100})
	curr := s.Snapshot()

	if curr.Version == prev.Version {
		t.Error("Version should have incremented")
	}

	delta := ComputeDelta(prev, curr)
	if delta != nil {
		t.Error("ComputeDelta should return nil (full snapshot) when queue content changes")
	}
}

func TestState_Snapshot_IdleShowsLastPlayedInsteadOfUpcoming(t *testing.T) {
	s := NewState()
	s.SetPlaylist([]MpvPlaylistEntry{
		{Filename: "track1.mp3", ID: 1},
		{Filename: "track2.mp3", ID: 2},
		{Filename: "track3.mp3", ID: 3},
		{Filename: "track4.mp3", ID: 4},
	})

	for i := 1; i <= 4; i++ {
		filename := "track" + string(rune('0'+i)) + ".mp3"
		title := "Track " + string(rune('0'+i))
		s.StoreMetadata(filename, resolver.Track{Title: title})
	}

	s.SetPlaylistPos(0)
	s.SetPlaylistPos(1)
	s.SetPlaylistPos(2)
	s.SetPlaylistPos(3)
	s.SetIdle(true)
	s.SetPlaylistPos(-1)

	snap := s.Snapshot()
	if snap.CurrentIdx != -1 {
		t.Fatalf("CurrentIdx = %d, want -1", snap.CurrentIdx)
	}
	if snap.NowPlaying != nil {
		t.Fatal("NowPlaying should be nil when idle")
	}
	if len(snap.Upcoming) != 0 {
		t.Fatalf("Upcoming length = %d, want 0", len(snap.Upcoming))
	}
	if len(snap.History) != 3 {
		t.Fatalf("History length = %d, want 3", len(snap.History))
	}

	want := []string{"Track 2", "Track 3", "Track 4"}
	for i, title := range want {
		if snap.History[i].Title != title {
			t.Fatalf("History[%d].Title = %q, want %q", i, snap.History[i].Title, title)
		}
	}
}

func TestState_SetPlaylistPos_DoesNotDuplicateHistoryOnSamePosition(t *testing.T) {
	s := NewState()
	s.SetPlaylist([]MpvPlaylistEntry{
		{Filename: "track1.mp3", ID: 1},
		{Filename: "track2.mp3", ID: 2},
	})
	s.StoreMetadata("track1.mp3", resolver.Track{Title: "Track 1"})
	s.StoreMetadata("track2.mp3", resolver.Track{Title: "Track 2"})

	s.SetPlaylistPos(0)
	s.SetPlaylistPos(0)
	s.SetPlaylistPos(1)
	s.SetPlaylistPos(1)
	s.SetIdle(true)
	s.SetPlaylistPos(-1)

	snap := s.Snapshot()
	if len(snap.History) != 2 {
		t.Fatalf("History length = %d, want 2", len(snap.History))
	}
	if snap.History[0].Title != "Track 1" {
		t.Fatalf("History[0].Title = %q, want %q", snap.History[0].Title, "Track 1")
	}
	if snap.History[1].Title != "Track 2" {
		t.Fatalf("History[1].Title = %q, want %q", snap.History[1].Title, "Track 2")
	}
}

func TestComputeDelta_CurrentIndexChangeReturnsFullSnapshot(t *testing.T) {
	prev := Snapshot{
		Version:    1,
		Status:     StatusPlaying,
		CurrentIdx: 0,
		Queue:      []QueueItem{{ID: 1, Index: 0, Filename: "track1.mp3"}, {ID: 2, Index: 1, Filename: "track2.mp3"}},
		NowPlaying: &QueueItem{ID: 1, Index: 0, Filename: "track1.mp3"},
		Upcoming:   []QueueItem{{ID: 2, Index: 1, Filename: "track2.mp3"}},
	}
	curr := Snapshot{
		Version:    2,
		Status:     StatusPlaying,
		CurrentIdx: 1,
		Queue:      []QueueItem{{ID: 1, Index: 0, Filename: "track1.mp3"}, {ID: 2, Index: 1, Filename: "track2.mp3"}},
		History:    []QueueItem{{ID: 1, Index: 0, Filename: "track1.mp3"}},
		NowPlaying: &QueueItem{ID: 2, Index: 1, Filename: "track2.mp3"},
	}

	if delta := ComputeDelta(prev, curr); delta != nil {
		t.Fatal("ComputeDelta should return nil when current item changes")
	}
}

func TestState_Snapshot_ReindexesRecentHistory(t *testing.T) {
	s := NewState()
	s.SetPlaylist([]MpvPlaylistEntry{
		{Filename: "track1.mp3", ID: 1},
		{Filename: "track2.mp3", ID: 2},
		{Filename: "track3.mp3", ID: 3},
	})
	s.StoreMetadata("track1.mp3", resolver.Track{Title: "Track 1"})
	s.StoreMetadata("track2.mp3", resolver.Track{Title: "Track 2"})
	s.StoreMetadata("track3.mp3", resolver.Track{Title: "Track 3"})

	s.SetPlaylistPos(0)
	s.SetPlaylistPos(1)
	s.SetPlaylist([]MpvPlaylistEntry{
		{Filename: "track2.mp3", ID: 2},
		{Filename: "track3.mp3", ID: 3},
		{Filename: "track1.mp3", ID: 1},
	})

	snap := s.Snapshot()
	if len(snap.History) != 1 {
		t.Fatalf("History length = %d, want 1", len(snap.History))
	}
	if snap.History[0].Index != 2 {
		t.Fatalf("History[0].Index = %d, want 2", snap.History[0].Index)
	}
}

func TestSnapshotRedactsSubsonicStreamURL(t *testing.T) {
	s := NewState()
	const secret = "salt-secret"
	stream := "https://music.example/rest/stream.view?id=track-1&u=alice&t=" + secret
	opaque := "skaldi+subsonic://personal/track-1"
	track := resolver.Track{
		ID:         "track-1",
		Title:      "Song",
		Artist:     "Artist",
		URL:        stream,
		WebpageURL: opaque,
		Source:     resolver.SourceSubsonic,
	}
	s.StoreMetadata(stream, track)
	s.SetPlaylist([]MpvPlaylistEntry{{Filename: stream, ID: 1}})
	s.SetPlaylistPos(0)

	snap := s.Snapshot()
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("serialized snapshot leaked the stream token: %s", data)
	}
	if snap.NowPlaying == nil || snap.NowPlaying.Metadata == nil {
		t.Fatal("NowPlaying metadata missing")
	}
	if snap.NowPlaying.Metadata.URL != opaque {
		t.Fatalf("metadata URL = %q, want opaque ref %q", snap.NowPlaying.Metadata.URL, opaque)
	}
	if len(snap.Queue) != 1 || snap.Queue[0].Metadata == nil || snap.Queue[0].Metadata.URL != opaque {
		t.Fatalf("queue metadata not redacted: %#v", snap.Queue)
	}

	s.RecordPlaybackFailure(9)
	failure := s.Snapshot().LastFailure
	if failure == nil || failure.EntryID != 9 || failure.Title != "" {
		t.Fatalf("LastFailure = %#v, want entry 9 with no title", failure)
	}
	data, err = json.Marshal(s.Snapshot())
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "stream.view") {
		t.Fatalf("serialized failure leaked the stream URL: %s", data)
	}
}

func TestSnapshotEqualityTracksPlaybackFailure(t *testing.T) {
	s := NewState()
	before := s.Snapshot()

	s.RecordPlaybackFailure(1)
	after := s.Snapshot()

	if SnapshotsEqual(before, after) {
		t.Fatal("SnapshotsEqual = true after a failure, want false")
	}
	if delta := ComputeDelta(before, after); delta != nil {
		t.Fatalf("ComputeDelta = %#v, want nil (full snapshot) when the failure changes", delta)
	}

	s.RecordPlaybackFailure(1)
	repeat := s.Snapshot()
	if repeat.LastFailure.Occurrence == after.LastFailure.Occurrence {
		t.Fatalf("occurrence = %d, want a new occurrence for a repeated failure", repeat.LastFailure.Occurrence)
	}
	if SnapshotsEqual(after, repeat) {
		t.Fatal("SnapshotsEqual = true for a repeated failure, want false")
	}
}
