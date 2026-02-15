package player

import (
	"testing"

	"skaldi/internal/resolver"
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

func TestState_SetIdle_ResetsPlaylistPos(t *testing.T) {
	s := NewState()
	s.playlistPos = 5

	s.SetIdle(true)

	if s.playlistPos != -1 {
		t.Errorf("playlistPos = %d, want -1", s.playlistPos)
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
	s.timePos = 100.0
	s.duration = 200.0

	s.SetPlaylistPos(2)

	if s.playlistPos != 2 {
		t.Errorf("playlistPos = %d, want 2", s.playlistPos)
	}

	if s.timePos != 100.0 {
		t.Errorf("timePos should be preserved, got %f", s.timePos)
	}

	if s.duration != 200.0 {
		t.Errorf("duration should be preserved, got %f", s.duration)
	}
}

func TestState_SetPlaylistPos_SamePos(t *testing.T) {
	s := NewState()
	s.playlistPos = 2
	s.timePos = 100.0
	s.duration = 200.0

	s.SetPlaylistPos(2)

	if s.timePos != 100.0 {
		t.Errorf("timePos should remain 100.0 when pos unchanged, got %f", s.timePos)
	}

	if s.duration != 200.0 {
		t.Errorf("duration should remain 200.0 when pos unchanged, got %f", s.duration)
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

	if len(snap.History) != 1 {
		t.Errorf("History length = %d, want 1", len(snap.History))
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
