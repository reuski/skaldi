// SPDX-License-Identifier: AGPL-3.0-or-later

package player

import (
	"sync"
	"time"

	"skaldi/internal/resolver"
)

type PlaybackStatus string

const (
	StatusIdle    PlaybackStatus = "idle"
	StatusPlaying PlaybackStatus = "playing"
	StatusPaused  PlaybackStatus = "paused"
)

type QueueItem struct {
	Index    int             `json:"index"`
	Filename string          `json:"filename"`
	Title    string          `json:"title,omitempty"`
	Duration float64         `json:"duration,omitempty"`
	Metadata *resolver.Track `json:"metadata,omitempty"`
}

type Snapshot struct {
	Version     uint64         `json:"v"`
	Status      PlaybackStatus `json:"status"`
	CurrentTime float64        `json:"current_time"`
	Duration    float64        `json:"duration"`
	Queue       []QueueItem    `json:"queue"`
	History     []QueueItem    `json:"history"`
	Upcoming    []QueueItem    `json:"upcoming"`
	CurrentIdx  int            `json:"current_index"`
	NowPlaying  *QueueItem     `json:"now_playing,omitempty"`
}

type Delta struct {
	Version     uint64          `json:"v"`
	CurrentTime *float64        `json:"current_time,omitempty"`
	Duration    *float64        `json:"duration,omitempty"`
	Status      *PlaybackStatus `json:"status,omitempty"`
	CurrentIdx  *int            `json:"current_index,omitempty"`
}

type State struct {
	mu sync.RWMutex

	version     uint64
	idleActive  bool
	paused      bool
	timePos     float64
	duration    float64
	playlist    []MpvPlaylistEntry
	playlistPos int

	metadata    map[string]resolver.Track
	metaAddedAt map[string]time.Time
}

type MpvPlaylistEntry struct {
	Filename string `json:"filename"`
	Current  bool   `json:"current,omitempty"`
	Playing  bool   `json:"playing,omitempty"`
	ID       int    `json:"id"`
}

func NewState() *State {
	return &State{
		metadata:    make(map[string]resolver.Track),
		metaAddedAt: make(map[string]time.Time),
		playlist:    []MpvPlaylistEntry{},
		playlistPos: -1,
	}
}

func (s *State) StoreMetadata(url string, track resolver.Track) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metadata[url] = track
	s.metaAddedAt[url] = time.Now()
	s.version++
}

func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := StatusPlaying
	if s.idleActive {
		status = StatusIdle
	} else if s.paused {
		status = StatusPaused
	}

	queue := make([]QueueItem, len(s.playlist))
	history := []QueueItem{}
	upcoming := []QueueItem{}
	currentIdx := -1
	var nowPlaying *QueueItem

	if s.playlistPos >= 0 && s.playlistPos < len(s.playlist) {
		currentIdx = s.playlistPos
	}

	for i, entry := range s.playlist {
		item := QueueItem{
			Index:    i,
			Filename: entry.Filename,
		}

		if track, ok := s.metadata[entry.Filename]; ok {
			item.Title = track.Title
			item.Duration = track.Duration
			item.Metadata = &track
		}

		queue[i] = item

		if i < currentIdx {
			history = append(history, item)
		} else if i > currentIdx {
			upcoming = append(upcoming, item)
		} else if i == currentIdx {
			nowPlaying = &item
		}
	}

	return Snapshot{
		Version:     s.version,
		Status:      status,
		CurrentTime: s.timePos,
		Duration:    s.duration,
		Queue:       queue,
		History:     history,
		Upcoming:    upcoming,
		CurrentIdx:  currentIdx,
		NowPlaying:  nowPlaying,
	}
}

func (s *State) SetIdle(idle bool) {
	s.mu.Lock()
	s.idleActive = idle
	s.version++
	s.mu.Unlock()
}

func (s *State) SetPaused(paused bool) {
	s.mu.Lock()
	if s.paused != paused {
		s.paused = paused
		s.version++
	}
	s.mu.Unlock()
}

func (s *State) SetTimePos(t float64) {
	s.mu.Lock()
	s.timePos = t
	s.mu.Unlock()
}

func (s *State) SetDuration(d float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.duration = d

	if s.playlistPos >= 0 && s.playlistPos < len(s.playlist) {
		filename := s.playlist[s.playlistPos].Filename
		if track, ok := s.metadata[filename]; ok {
			track.Duration = d
			s.metadata[filename] = track
		}
	}
}

func (s *State) SetPlaylist(entries []MpvPlaylistEntry) {
	s.mu.Lock()
	s.playlist = entries
	s.version++
	s.mu.Unlock()
}

func (s *State) SetPlaylistPos(pos int) {
	s.mu.Lock()
	if pos != s.playlistPos {
		s.version++
	}
	s.playlistPos = pos
	s.mu.Unlock()
}

func (s *State) PruneMetadata() {
	s.PruneMetadataBefore(time.Time{})
}

func (s *State) PruneMetadataBefore(cutoff time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	inPlaylist := make(map[string]struct{}, len(s.playlist))
	for _, entry := range s.playlist {
		inPlaylist[entry.Filename] = struct{}{}
	}

	for key := range s.metadata {
		if _, ok := inPlaylist[key]; !ok {
			if cutoff.IsZero() || s.metaAddedAt[key].Before(cutoff) {
				delete(s.metadata, key)
				delete(s.metaAddedAt, key)
			}
		}
	}
}

func queueChanged(a, b []QueueItem) bool {
	if len(a) != len(b) {
		return true
	}
	for i := range a {
		if a[i].Filename != b[i].Filename {
			return true
		}
		if a[i].Title != b[i].Title {
			return true
		}
		if a[i].Duration != b[i].Duration {
			return true
		}
	}
	return false
}

func ComputeDelta(prev, curr Snapshot) *Delta {
	if prev.Version == 0 {
		return nil
	}

	if curr.Version != prev.Version {
		if queueChanged(prev.Queue, curr.Queue) {
			return nil
		}

		delta := &Delta{Version: curr.Version}
		delta.CurrentTime = &curr.CurrentTime
		delta.Duration = &curr.Duration
		delta.Status = &curr.Status
		delta.CurrentIdx = &curr.CurrentIdx
		return delta
	}

	if curr.CurrentTime == prev.CurrentTime && curr.Duration == prev.Duration {
		return nil
	}

	delta := &Delta{Version: curr.Version}
	if curr.CurrentTime != prev.CurrentTime {
		delta.CurrentTime = &curr.CurrentTime
	}
	if curr.Duration != prev.Duration {
		delta.Duration = &curr.Duration
	}
	return delta
}
